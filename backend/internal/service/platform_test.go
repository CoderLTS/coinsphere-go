package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/testdb"
	"coinsphere/backend/plugin/sdk"
	workflowgraph "coinsphere/backend/workflow/graph"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type testAction func(context.Context, sdk.ActionRequest) (sdk.ActionResult, error)

func (f testAction) Execute(ctx context.Context, r sdk.ActionRequest) (sdk.ActionResult, error) {
	return f(ctx, r)
}

// A non-financial plugin exercises every extension through the real registry.
func businessPlugin(t *testing.T, registry *sdk.Registry, action testAction) {
	t.Helper()
	empty := json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`)
	err := registry.RegisterPlugin(sdk.PluginDescriptor{ID: "test.business", Name: "Business", Version: "1.0.0", Contributes: []string{"nodes", "apiRoutes", "resultPages", "runPanels"}, Permissions: []sdk.PermissionDescriptor{{Code: "plugins.test.business.read", Title: "Read"}, {Code: "plugins.test.business.execute", Title: "Execute"}}}, sdk.Host{}, func(r sdk.Registrar, _ sdk.Host) error {
		for _, kind := range []string{"task", "state", "external"} {
			desc := sdk.NodeDescriptor{Type: "test.business." + kind, Version: "1.0.0", Kind: sdk.NodeKindAction, Title: kind, Description: "Synthetic business node", Category: "business", Color: "#2563eb", Icon: "task", Width: 220, Height: 72, Pool: sdk.PoolStream, State: sdk.StateStateless, SideEffect: sdk.SideEffectNone, ConfigSchema: empty, InputSchema: empty, OutputSchema: empty, UISchema: json.RawMessage(`{"ui:order":[]}`), ExecutionPermissions: []string{"plugins.test.business.execute"}}
			if kind == "state" {
				desc.State = sdk.StatePersistent
			}
			if kind == "external" {
				desc.SideEffect = sdk.SideEffectExternal
			}
			if err := r.Action(desc, action); err != nil {
				return err
			}
		}
		if err := r.RunPanel(sdk.RunPanelDescriptor{PanelKey: "task", Title: "Task", NodeTypes: []string{"test.business.task"}, ComponentEntry: "./TaskPanel.vue"}); err != nil {
			return err
		}
		resources := func(raw json.RawMessage) ([]sdk.WorkflowResource, error) {
			var s struct {
				WorkflowID int64  `json:"workflowId"`
				NodeID     string `json:"nodeId"`
			}
			if err := json.Unmarshal(raw, &s); err != nil {
				return nil, err
			}
			return []sdk.WorkflowResource{{WorkflowID: s.WorkflowID, NodeInstanceID: s.NodeID}}, nil
		}
		if err := r.ResultPage(sdk.ResultPageDescriptor{PageKey: "tasks", Title: "Tasks", ComponentEntry: "./TaskResults.vue", PermissionCode: "plugins.test.business.read", Actions: []string{"ack"}, ActionPermissions: map[string]string{"ack": "plugins.test.business.execute"}, ScopeSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"workflowId":{"type":"integer","minimum":1},"nodeId":{"type":"string"}},"required":["workflowId","nodeId"],"additionalProperties":false}`), FilterSchema: empty, Resources: resources, ValidateScope: func(ctx context.Context, tx *gorm.DB, raw json.RawMessage) error {
			refs, err := resources(raw)
			if err != nil {
				return err
			}
			var revision db.WorkflowRevision
			if err := tx.WithContext(ctx).Joins("JOIN workflows w ON w.draft_revision_id=workflow_revisions.id").Where("w.id=?", refs[0].WorkflowID).First(&revision).Error; err != nil {
				return err
			}
			var graph workflowGraph
			if err := json.Unmarshal([]byte(revision.GraphJSON), &graph); err != nil {
				return err
			}
			for _, node := range graph.Nodes {
				if node.NodeInstanceID == refs[0].NodeInstanceID && node.NodeType == "test.business.task" {
					return nil
				}
			}
			return ErrNotFound
		}}); err != nil {
			return err
		}
		for _, desc := range []sdk.RouteDescriptor{{Scope: sdk.ScopeWorkflow, Method: http.MethodGet, Pattern: "/summary", PermissionCode: "plugins.test.business.read"}, {Scope: sdk.ScopeResult, Method: http.MethodPost, Pattern: "/ack", Action: "ack", PermissionCode: "plugins.test.business.execute"}} {
			if err := r.Route(desc, func(c *gin.Context, scope sdk.RouteScope) { c.JSON(http.StatusOK, gin.H{"scoped": scope != nil}) }); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func platformFixture(t *testing.T, action testAction) (*App, context.Context, *Principal) {
	t.Helper()
	_, database := testdb.Open(t, true)
	registry := sdk.NewRegistry()
	if action == nil {
		action = func(context.Context, sdk.ActionRequest) (sdk.ActionResult, error) {
			return sdk.ActionResult{Output: json.RawMessage(`{}`)}, nil
		}
	}
	businessPlugin(t, registry, action)
	app := NewApp(database, &config.AppConfig{Auth: config.AuthConfig{SecretKey: "synthetic-platform-key", PasswordIterations: 1}}, registry)
	app.ArtifactRoot = t.TempDir()
	for _, sql := range []string{`INSERT INTO roles(id,code) VALUES(1,'R_SUPER'),(2,'R_USER')`, `INSERT INTO users(id,username) VALUES(1,'synthetic-owner'),(2,'synthetic-reader')`, `INSERT INTO user_roles(user_id,role_id) VALUES(1,1),(2,2)`, `INSERT INTO plugin_installations(plugin_id,version,schema_name,source_path,status) VALUES('test.business','1.0.0','plugin_test_business','synthetic','installed')`} {
		if err := database.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := app.SyncCapabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, err := app.buildPrincipal(1)
	if err != nil {
		t.Fatal(err)
	}
	return app, WithPrincipal(context.Background(), p), p
}

func testGraph(kind string) workflowGraph {
	nodes := []workflowGraphNode{{NodeInstanceID: "manual", NodeType: "core.manual", NodeVersion: "1.0.0", Config: json.RawMessage(`{}`), Position: &workflowgraph.Position{}}}
	if kind != "" {
		nodes = append(nodes, workflowGraphNode{NodeInstanceID: "task", NodeType: "test.business." + kind, NodeVersion: "1.0.0", Config: json.RawMessage(`{}`), Position: &workflowgraph.Position{X: 260}})
	}
	nodes = append(nodes, workflowGraphNode{NodeInstanceID: "end", NodeType: "core.end", NodeVersion: "1.0.0", Config: json.RawMessage(`{}`), Position: &workflowgraph.Position{X: 520}})
	g := workflowGraph{SchemaVersion: 3, EntryPoints: map[string]string{"main": "manual"}, Nodes: nodes}
	for i := 1; i < len(nodes); i++ {
		g.Edges = append(g.Edges, workflowGraphEdge{EdgeID: fmt.Sprint("e", i), SourceNodeInstanceID: nodes[i-1].NodeInstanceID, SourcePort: "out", TargetNodeInstanceID: nodes[i].NodeInstanceID, TargetPort: "in"})
	}
	return g
}

func createTestWorkflow(t *testing.T, a *App, ctx context.Context, p *Principal, g workflowGraph) WorkflowDetail {
	t.Helper()
	w, err := a.CreateWorkflow(ctx, WorkflowCreatePayload{Name: "Synthetic", Graph: mustJSON(g), MaxConcurrentRuns: 4}, p)
	if err != nil {
		t.Fatal(err)
	}
	return w
}
func queueTestRun(t *testing.T, a *App, ctx context.Context, p *Principal, w WorkflowDetail) WorkflowRunView {
	t.Helper()
	r, err := a.CreateWorkflowRun(ctx, w.ID, WorkflowRunCreatePayload{RevisionID: *w.DraftRevisionID}, p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func claimTestRun(t *testing.T, a *App) db.WorkflowRun {
	t.Helper()
	r, ok, err := a.claimWorkflowRun(context.Background(), time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("claim: %v, %v", ok, err)
	}
	return r
}
func countTestRows(t *testing.T, a *App, table, where string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := a.DB.Table(table).Where(where, args...).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDraftPublicationAndAtomicConflicts(t *testing.T) {
	a, ctx, p := platformFixture(t, nil)
	g := testGraph("")
	w := createTestWorkflow(t, a, ctx, p, g)
	first := *w.DraftRevisionID
	if _, err := a.PublishWorkflowRevision(ctx, w.ID, WorkflowPublishPayload{RevisionID: first}, p); err != nil {
		t.Fatal(err)
	}
	queueTestRun(t, a, ctx, p, w)
	for i := 0; i < 12; i++ {
		r, err := a.SaveWorkflowRevision(ctx, w.ID, WorkflowRevisionSavePayload{ExpectedDraftRevisionID: *w.DraftRevisionID, Graph: mustJSON(g)}, p)
		if err != nil {
			t.Fatal(err)
		}
		w.DraftRevisionID = &r.ID
	}
	latest, err := a.GetWorkflow(ctx, w.ID)
	if err != nil || *latest.PublishedRevisionID != first {
		t.Fatal("saving changed publication", err)
	}
	if countTestRows(t, a, "workflow_revisions", "id=?", first) != 1 {
		t.Fatal("referenced oldest revision was pruned")
	}
	before := countTestRows(t, a, "workflow_revisions", "workflow_id=?", w.ID)
	_, err = a.SaveWorkflowRevision(ctx, w.ID, WorkflowRevisionSavePayload{ExpectedDraftRevisionID: *w.DraftRevisionID, Graph: mustJSON(g), Metadata: &WorkflowUpdatePayload{Name: ""}}, p)
	if err == nil || countTestRows(t, a, "workflow_revisions", "workflow_id=?", w.ID) != before {
		t.Fatal("invalid atomic metadata left a revision")
	}
	start := make(chan struct{})
	outcomes := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, err := a.SaveWorkflowRevision(ctx, w.ID, WorkflowRevisionSavePayload{ExpectedDraftRevisionID: *w.DraftRevisionID, Graph: mustJSON(g)}, p)
			outcomes <- err
		}()
	}
	close(start)
	x, y := <-outcomes, <-outcomes
	if !((x == nil && errors.Is(y, ErrConflict)) || (y == nil && errors.Is(x, ErrConflict))) {
		t.Fatalf("parallel saves: %v %v", x, y)
	}
	if _, err := a.PublishWorkflowRevision(ctx, w.ID, WorkflowPublishPayload{RevisionID: first, ExpectedPublishedRevisionID: 0}, p); !errors.Is(err, ErrConflict) {
		t.Fatal("stale publish was accepted", err)
	}
}

func TestLeaseFencingAndRecoveryPolicies(t *testing.T) {
	for _, kind := range []string{"task", "external"} {
		t.Run(kind, func(t *testing.T) {
			a, ctx, p := platformFixture(t, nil)
			w := createTestWorkflow(t, a, ctx, p, testGraph(kind))
			queueTestRun(t, a, ctx, p, w)
			run := claimTestRun(t, a)
			var revision db.WorkflowRevision
			if err := a.DB.First(&revision, run.RevisionID).Error; err != nil {
				t.Fatal(err)
			}
			node := testGraph(kind).Nodes[1]
			now := time.Now().UTC()
			rn := db.WorkflowRunNode{RunID: run.ID, NodeInstanceID: node.NodeInstanceID, NodeType: node.NodeType, NodeVersion: node.NodeVersion, Status: RunStatusRunning, ExecutionPool: "stream", Attempt: 1, OperationKey: workflowOperationKey(run.ID, node.NodeInstanceID, 0), InputSummary: "{}", OutputSummary: "{}", StartedAt: now, InvocationStarted: true}
			if err := a.DB.Create(&rn).Error; err != nil {
				t.Fatal(err)
			}
			if err := a.DB.Model(&run).Update("lease_expires_at", now.Add(-time.Second)).Error; err != nil {
				t.Fatal(err)
			}
			if err := a.commitWorkflowNodeSuccess(ctx, rn, run, revision, node, rn.OperationKey, 0, json.RawMessage(`{}`), json.RawMessage(`{"value":1}`), nil, now); err == nil {
				t.Fatal("expired executor committed")
			}
			if err := a.recoverExpiredRuns(ctx); err != nil {
				t.Fatal(err)
			}
			var recovered db.WorkflowRun
			if err := a.DB.First(&recovered, run.ID).Error; err != nil {
				t.Fatal(err)
			}
			if kind == "external" {
				if recovered.Status != RunStatusFailed || recovered.ErrorCategory == nil || *recovered.ErrorCategory != string(sdk.ErrorUnknownResult) {
					t.Fatal("unsafe invocation was repeated")
				}
				if _, err := a.ApplyWorkflowRunAction(ctx, run.ID, WorkflowRunActionPayload{Action: "retry"}); !errors.Is(err, ErrConflict) {
					t.Fatal("unknown effect was retried", err)
				}
			} else {
				if recovered.Status != RunStatusQueued {
					t.Fatal("pure computation was not recovered")
				}
				next := claimTestRun(t, a)
				if next.LeaseToken == nil || *next.LeaseToken == *run.LeaseToken {
					t.Fatal("recovery reused a lease")
				}
				if err := a.commitWorkflowNodeSuccess(ctx, rn, run, revision, node, rn.OperationKey, 0, json.RawMessage(`{}`), nil, nil, now); err == nil {
					t.Fatal("replaced executor committed")
				}
			}
			if countTestRows(t, a, "workflow_run_checkpoints", "run_id=?", run.ID) != 0 || countTestRows(t, a, "workflow_node_states", "workflow_id=?", w.ID) != 0 {
				t.Fatal("stale execution left durable facts")
			}
		})
	}
}

func TestStateIsSerialAcrossPartitionsAndIsolatedByRevision(t *testing.T) {
	a, ctx, p := platformFixture(t, func(ctx context.Context, r sdk.ActionRequest) (sdk.ActionResult, error) {
		raw, err := r.State.Load(ctx)
		if err != nil {
			return sdk.ActionResult{}, err
		}
		var state struct {
			Count int `json:"count"`
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &state); err != nil {
				return sdk.ActionResult{}, err
			}
		}
		state.Count++
		if err := r.State.Save(ctx, mustJSON(state)); err != nil {
			return sdk.ActionResult{}, err
		}
		return sdk.ActionResult{Output: json.RawMessage(`{}`)}, nil
	})
	g := testGraph("state")
	w := createTestWorkflow(t, a, ctx, p, g)
	for _, partition := range []string{"a", "b"} {
		r := queueTestRun(t, a, ctx, p, w)
		if err := a.DB.Model(&db.WorkflowRun{}).Where("id=?", r.ID).Update("partition_key", partition).Error; err != nil {
			t.Fatal(err)
		}
	}
	first := claimTestRun(t, a)
	if _, ok, err := a.claimWorkflowRun(ctx, time.Now().UTC()); err != nil || ok {
		t.Fatal("stateful partitions overlapped", err)
	}
	a.executeWorkflowRun(ctx, first)
	a.executeWorkflowRun(ctx, claimTestRun(t, a))
	var old db.WorkflowNodeState
	if err := a.DB.Where("workflow_id=?", w.ID).First(&old).Error; err != nil {
		t.Fatal(err)
	}
	if old.StateJSON != `{"count": 2}` {
		var value map[string]int
		if json.Unmarshal([]byte(old.StateJSON), &value) != nil || value["count"] != 2 {
			t.Fatal("state was not carried", old.StateJSON)
		}
	}
	r, err := a.SaveWorkflowRevision(ctx, w.ID, WorkflowRevisionSavePayload{ExpectedDraftRevisionID: *w.DraftRevisionID, Graph: mustJSON(g)}, p)
	if err != nil {
		t.Fatal(err)
	}
	w.DraftRevisionID = &r.ID
	queueTestRun(t, a, ctx, p, w)
	a.executeWorkflowRun(ctx, claimTestRun(t, a))
	var state db.WorkflowNodeState
	if err := a.DB.Where("workflow_id=? AND revision_id=?", w.ID, r.ID).First(&state).Error; err != nil {
		t.Fatal(err)
	}
	var value map[string]int
	if json.Unmarshal([]byte(state.StateJSON), &value) != nil || value["count"] != 1 {
		t.Fatal("new revision reused old state")
	}
}

func TestApprovalDecisionSupersessionExpiryAndCancel(t *testing.T) {
	a, ctx, p := platformFixture(t, nil)
	g := testGraph("")
	approval := workflowGraphNode{NodeInstanceID: "approval", NodeType: "core.human_approval", NodeVersion: "1.0.0", Config: json.RawMessage(`{"decisionMode":"human","taskType":"review","prompt":"Synthetic","expiresSeconds":60}`), Position: &workflowgraph.Position{X: 260}, InputBindings: map[string]workflowInputBinding{"businessKey": {Kind: "literal", Value: "synthetic-key"}}}
	g.Nodes = append(g.Nodes[:1], approval, g.Nodes[1])
	g.Edges = []workflowGraphEdge{{EdgeID: "ma", SourceNodeInstanceID: "manual", SourcePort: "out", TargetNodeInstanceID: "approval", TargetPort: "in"}, {EdgeID: "ae", SourceNodeInstanceID: "approval", SourcePort: "out", TargetNodeInstanceID: "end", TargetPort: "in"}}
	w := createTestWorkflow(t, a, ctx, p, g)
	makeTask := func() db.WorkflowHumanTask {
		queueTestRun(t, a, ctx, p, w)
		run := claimTestRun(t, a)
		a.executeWorkflowRun(ctx, run)
		var task db.WorkflowHumanTask
		if err := a.DB.Where("run_id=?", run.ID).First(&task).Error; err != nil {
			t.Fatal(err)
		}
		var status string
		if err := a.DB.Model(&db.WorkflowRun{}).Where("id=?", run.ID).Pluck("status", &status).Error; err != nil || status != RunStatusWaiting {
			t.Fatal("visible pending task has no durable wait", err)
		}
		return task
	}
	one := makeTask()
	if _, err := a.DecideWorkflowHumanTask(ctx, one.ID, WorkflowHumanTaskDecision{Action: "approve"}, p); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DecideWorkflowHumanTask(ctx, one.ID, WorkflowHumanTaskDecision{Action: "reject"}, p); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate decision succeeded", err)
	}
	a.executeWorkflowRun(ctx, claimTestRun(t, a))
	two := makeTask()
	three := makeTask()
	var old db.WorkflowHumanTask
	if err := a.DB.First(&old, two.ID).Error; err != nil || old.Status != "superseded" {
		t.Fatal("new business task did not supersede old wait", err)
	}
	if _, err := a.ApplyWorkflowRunAction(ctx, three.RunID, WorkflowRunActionPayload{Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.First(&old, three.ID).Error; err != nil {
		t.Fatal(err)
	}
	if old.Status != "superseded" {
		t.Fatal("cancel left a pending task")
	}
	if countTestRows(t, a, "plugin_references", "reference_type='run' AND reference_id=? AND active", strconv.FormatInt(three.RunID, 10)) != 0 {
		t.Fatal("cancel left active run references")
	}
	a.executeWorkflowRun(ctx, claimTestRun(t, a))
	expired := makeTask()
	if err := a.DB.Model(&expired).Update("expires_at", time.Now().UTC().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.expireWorkflowHumanTasks(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DecideWorkflowHumanTask(ctx, expired.ID, WorkflowHumanTaskDecision{Action: "approve"}, p); !errors.Is(err, ErrConflict) {
		t.Fatal("expired task was approved", err)
	}
}

func TestNonFinancialPluginScopesAndRevocation(t *testing.T) {
	a, ctx, p := platformFixture(t, nil)
	w := createTestWorkflow(t, a, ctx, p, testGraph("task"))
	other := createTestWorkflow(t, a, ctx, p, testGraph("task"))
	if _, err := a.ResolveWorkflowScope(ctx, "test.business", w.ID, "task", "plugins.test.business.read"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ResolveWorkflowScope(ctx, "test.business", w.ID, "end", "plugins.test.business.read"); !errors.Is(err, ErrNotFound) {
		t.Fatal("workflow scope accepted a foreign node", err)
	}
	view, err := a.CreateResultView(ctx, ResultViewCreatePayload{Name: "Tasks", PluginID: "test.business", PageKey: "tasks", Scope: mustJSON(map[string]any{"workflowId": w.ID, "nodeId": "task"}), Filters: json.RawMessage(`{}`), AllowedActions: []string{"ack"}, UserIDs: []int64{2}}, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"result_views.read", "plugins.test.business.read", "plugins.test.business.execute"} {
		if err := a.DB.Create(&db.RolePermission{RoleID: 2, PermissionCode: code}).Error; err != nil {
			t.Fatal(err)
		}
	}
	reader, err := a.buildPrincipal(2)
	if err != nil {
		t.Fatal(err)
	}
	reader.AccessTokenID = "synthetic-session"
	reader.AccessTokenExp = time.Now().UTC().Add(time.Hour)
	readCtx := WithPrincipal(context.Background(), reader)
	if _, err := a.GetWorkflow(readCtx, w.ID); err == nil {
		t.Fatal("result grant exposed workflow management")
	}
	if err := a.AuthorizeWorkflow(readCtx, other.ID, "workflows.read"); err == nil {
		t.Fatal("cross-workflow access succeeded")
	}
	scope, err := a.ResolveResultScope(readCtx, view.ID, "ack", reader)
	if err != nil || len(scope.Resources) != 1 || scope.Resources[0].NodeInstanceID != "task" {
		t.Fatal("custom result action did not retain its fixed scope", err)
	}
	if _, err := a.resultWorkflowActionContext(readCtx, scope, w.ID, "workflows.cancel"); err == nil {
		t.Fatal("node scope was widened to a whole run")
	}
	if _, err := a.SetResultViewStatus(ctx, view.ID, "inactive"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ResolveResultScope(readCtx, view.ID, "ack", reader); err == nil {
		t.Fatal("inactive result remained open")
	}
	if _, err := a.SetResultViewStatus(ctx, view.ID, "active"); err != nil {
		t.Fatal(err)
	}
	if err := a.LogoutAccessToken(reader); err != nil {
		t.Fatal(err)
	}
	restarted := NewApp(a.DB, a.Cfg, a.Plugins)
	if _, err := restarted.RevalidateSession(reader, "result_views.read"); err == nil {
		t.Fatal("logout vanished after restart")
	}
	if _, err := principalForTx(a.DB, reader); err == nil {
		t.Fatal("revoked session committed")
	}
	if _, err := a.RevokeResultView(ctx, view.ID, p); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetResultViewStatus(ctx, view.ID, "active"); !errors.Is(err, ErrConflict) {
		t.Fatal("revoked view was reopened", err)
	}
}

func TestLoopParentsDoNotExhaustChildPool(t *testing.T) {
	var entered atomic.Int32
	barrier := make(chan struct{})
	a, ctx, p := platformFixture(t, func(ctx context.Context, _ sdk.ActionRequest) (sdk.ActionResult, error) {
		if entered.Add(1) == 4 {
			close(barrier)
		}
		select {
		case <-barrier:
			return sdk.ActionResult{Output: json.RawMessage(`{}`)}, nil
		case <-ctx.Done():
			return sdk.ActionResult{}, ctx.Err()
		}
	})
	body := workflowGraph{SchemaVersion: 3, Nodes: []workflowGraphNode{{NodeInstanceID: "item", NodeType: "core.loop_item", NodeVersion: "1.0.0", Config: json.RawMessage(`{}`), Position: &workflowgraph.Position{}}, {NodeInstanceID: "work", NodeType: "test.business.task", NodeVersion: "1.0.0", Config: json.RawMessage(`{}`), Position: &workflowgraph.Position{X: 260}}, {NodeInstanceID: "done", NodeType: "core.loop_end", NodeVersion: "1.0.0", Config: json.RawMessage(`{}`), Position: &workflowgraph.Position{X: 520}, InputBindings: map[string]workflowInputBinding{"value": {Kind: "field", NodeInstanceID: "item", FieldPath: []string{"value"}}}}}, Edges: []workflowGraphEdge{{EdgeID: "iw", SourceNodeInstanceID: "item", SourcePort: "out", TargetNodeInstanceID: "work", TargetPort: "in"}, {EdgeID: "we", SourceNodeInstanceID: "work", SourcePort: "out", TargetNodeInstanceID: "done", TargetPort: "in"}}}
	g := testGraph("task")
	g.Nodes[1].NodeType = "core.loop"
	g.Nodes[1].Config = mustJSON(workflowLoopConfig{MaxIterations: 1, TimeoutSeconds: 20, ExitCondition: "input.iteration >= 1", Body: body})
	runs := []db.WorkflowRun{}
	for i := 0; i < 4; i++ {
		w := createTestWorkflow(t, a, ctx, p, g)
		queueTestRun(t, a, ctx, p, w)
		runs = append(runs, claimTestRun(t, a))
	}
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	for _, run := range runs {
		workers.Add(1)
		go func(run db.WorkflowRun) { defer workers.Done(); a.executeWorkflowRun(runCtx, run) }(run)
	}
	workers.Wait()
	if entered.Load() != 4 {
		t.Fatal("Loop parents held all child slots")
	}
	for _, run := range runs {
		var stored db.WorkflowRun
		if err := a.DB.First(&stored, run.ID).Error; err != nil || stored.Status != RunStatusSucceeded {
			t.Fatal("Loop failed", stored.Status, err)
		}
	}
	if len(a.streamSlots) != 0 {
		t.Fatal("Loop left occupied slots")
	}
}

func TestPrivilegeCeilingAndRoleWriteRollback(t *testing.T) {
	a, ctx, owner := platformFixture(t, nil)
	for _, code := range []string{"system.users.create", "system.users.update", "system.users.assign_roles"} {
		if err := a.DB.Create(&db.RolePermission{RoleID: 2, PermissionCode: code}).Error; err != nil {
			t.Fatal(err)
		}
	}
	p, err := a.buildPrincipal(2)
	if err != nil {
		t.Fatal(err)
	}
	before := countTestRows(t, a, "users", "TRUE")
	if _, err := a.CreateUser(UserUpsertPayload{Username: "synthetic-new", Nickname: "New", Password: "synthetic-password", RoleCodes: []string{"R_SUPER"}}, p); !errors.Is(err, ErrPermission) {
		t.Fatal("delegated user manager granted superuser", err)
	}
	if countTestRows(t, a, "users", "TRUE") != before {
		t.Fatal("failed grant left a user")
	}
	if _, err := a.UpdateUser(1, UserUpsertPayload{Username: owner.User.Username, Nickname: "Altered"}, p); !errors.Is(err, ErrPermission) {
		t.Fatal("delegated user manager changed protected account", err)
	}
	if err := a.DB.Exec(`CREATE FUNCTION synthetic_fail_roles() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic role failure'; END $$; CREATE TRIGGER synthetic_role_failure BEFORE INSERT ON user_roles FOR EACH ROW EXECUTE FUNCTION synthetic_fail_roles()`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := a.UpdateUser(2, UserUpsertPayload{Username: "synthetic-reader", Nickname: "Changed", RoleCodes: []string{"R_USER"}}, owner); err == nil {
		t.Fatal("injected role write failure ignored")
	}
	var user db.SystemUser
	if err := a.DB.First(&user, 2).Error; err != nil {
		t.Fatal(err)
	}
	if user.Nickname != "" || countTestRows(t, a, "user_roles", "user_id=2 AND role_id=2") != 1 {
		t.Fatal("role write failure partially updated user")
	}
	w := createTestWorkflow(t, a, ctx, owner, testGraph("task"))
	if err := a.DB.Create(&db.RolePermission{RoleID: 2, PermissionCode: "workflows.share"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.ReplaceWorkflowGrants(ctx, w.ID, []WorkflowGrant{{UserID: 2, Permissions: []string{"workflows.share"}}}); err != nil {
		t.Fatal(err)
	}
	readerCtx := WithPrincipal(context.Background(), p)
	if err := a.ReplaceWorkflowGrants(readerCtx, w.ID, []WorkflowGrant{{UserID: 2, Permissions: []string{"workflows.delete"}}}); !errors.Is(err, ErrPermission) {
		t.Fatal("share grant expanded beyond capability/resource ceiling", err)
	}
	var permissions string
	if err := a.DB.Table("workflow_user_grants").Where("workflow_id=?", w.ID).Pluck("permissions", &permissions).Error; err != nil {
		t.Fatal(err)
	}
	var codes []string
	if json.Unmarshal([]byte(permissions), &codes) != nil || len(codes) != 1 || codes[0] != "workflows.share" {
		t.Fatal("failed share grant removed old authorization")
	}
}

func TestErrorClassesAndDiagnosticReplaySuppressEffects(t *testing.T) {
	for _, kind := range []string{"task", "external"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			a, ctx, p := platformFixture(t, func(context.Context, sdk.ActionRequest) (sdk.ActionResult, error) {
				calls.Add(1)
				return sdk.ActionResult{}, &sdk.ExecutionError{Class: sdk.ErrorTransient, Err: errors.New("synthetic temporary failure")}
			})
			w := createTestWorkflow(t, a, ctx, p, testGraph(kind))
			r := queueTestRun(t, a, ctx, p, w)
			a.executeWorkflowRun(ctx, claimTestRun(t, a))
			var stored db.WorkflowRun
			if err := a.DB.First(&stored, r.ID).Error; err != nil {
				t.Fatal(err)
			}
			if kind == "task" {
				if stored.Status != RunStatusRetrying {
					t.Fatal("safe transient computation was not retried", stored.Status)
				}
			} else {
				if stored.Status != RunStatusFailed || stored.ErrorCategory == nil || *stored.ErrorCategory != string(sdk.ErrorUnknownResult) {
					t.Fatal("external temporary failure lost unknown outcome classification")
				}
				replay, err := a.ApplyWorkflowRunAction(ctx, r.ID, WorkflowRunActionPayload{Action: "replay"})
				if err != nil {
					t.Fatal(err)
				}
				a.executeWorkflowRun(ctx, claimTestRun(t, a))
				if calls.Load() != 1 {
					t.Fatal("diagnostic replay issued a new external call")
				}
				var diagnostic db.WorkflowRun
				if err := a.DB.First(&diagnostic, replay.ID).Error; err != nil || diagnostic.Status != RunStatusFailed {
					t.Fatal("missing side-effect checkpoint was silently invented", err)
				}
			}
		})
	}
}
