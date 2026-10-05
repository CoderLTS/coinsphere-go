package workflowmigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/service"
	"coinsphere/backend/internal/testdb"
	"coinsphere/backend/plugin/sdk"
	"coinsphere/backend/workflow/graph"
	"github.com/pressly/goose/v3"
)

func descriptor(id string, kind sdk.NodeKind, fields string) sdk.NodeDescriptor {
	return sdk.NodeDescriptor{Type: id, Version: "1.0.0", Kind: kind, OutputSchema: json.RawMessage(`{"type":"object","properties":` + fields + `}`), ConfigSchema: json.RawMessage(`{"type":"object","properties":{"token":{"type":"string","x-coinsphere-secret":true}}}`)}
}
func converter() Converter {
	return Converter{Catalog: map[string]sdk.NodeDescriptor{
		"core.manual":                   descriptor("core.manual", sdk.NodeKindTrigger, `{"triggeredAt":{"type":"string"}}`),
		"core.constant":                 descriptor("core.constant", sdk.NodeKindAction, `{"value":{"type":"string"}}`),
		"core.end":                      descriptor("core.end", sdk.NodeKindAction, `{}`),
		"core.loop":                     descriptor("core.loop", sdk.NodeKindAction, `{"value":{"type":"object"}}`),
		"core.loop_item":                descriptor("core.loop_item", sdk.NodeKindAction, `{"value":{"type":"object"},"iteration":{"type":"integer"}}`),
		"core.loop_end":                 descriptor("core.loop_end", sdk.NodeKindAction, `{"value":{"type":"object"}}`),
		"example.flow.action":           descriptor("example.flow.action", sdk.NodeKindAction, `{"ready":{"type":"boolean"},"triggered":{"type":"boolean"},"entered":{"type":"boolean"},"summary":{"type":"string"},"businessKey":{"type":"string"}}`),
		"official.notification.compose": descriptor("official.notification.compose", sdk.NodeKindAction, `{"subjectKey":{"type":"string"},"message":{"type":"string"}}`),
		"official.notification.in_app":  descriptor("official.notification.in_app", sdk.NodeKindAction, `{}`),
	}, NodePlugins: map[string]string{"example.flow.action": "example.flow", "official.notification.compose": "official.notification", "official.notification.in_app": "official.notification"}, Mappings: Mappings{NodeVersions: map[string]NodeVersionMapping{"example.flow.action@1.0.0": {NodeType: "example.flow.action", NodeVersion: "1.0.0"}}}}
}

func TestLegacyConversionRequiresProofForConflictingCEL(t *testing.T) {
	raw := json.RawMessage(`{"schemaVersion":2,"entryPoints":{"realtime":"manual","job":"manual"},"nodes":[{"nodeInstanceId":"manual","nodeType":"core.manual","nodeVersion":"1.0.0","config":{},"position":{"x":0,"y":0}},{"nodeInstanceId":"a","nodeType":"core.constant","nodeVersion":"1.0.0","config":{"value":"a"},"position":{"x":1,"y":1}},{"nodeInstanceId":"b","nodeType":"core.constant","nodeVersion":"1.0.0","config":{"value":"b"},"position":{"x":2,"y":2}},{"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"inputBindings":{"x":{"kind":"cel","expression":"input.value"}},"position":{"x":3,"y":3}}],"edges":[{"edgeId":"ma","sourceNodeInstanceId":"manual","sourcePort":"out","targetNodeInstanceId":"a","targetPort":"in"},{"edgeId":"ab","sourceNodeInstanceId":"a","sourcePort":"out","targetNodeInstanceId":"b","targetPort":"in"},{"edgeId":"be","sourceNodeInstanceId":"b","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}]}`)
	c := converter()
	if _, err := c.Convert(42, raw); err == nil {
		t.Fatal("conflicting flattened fields must not be guessed")
	}
	c.Mappings.Expressions = map[string]ExpressionMapping{"42/end/binding/x": {Sources: map[string]graph.Binding{"value": {Kind: "field", NodeInstanceID: "b", FieldPath: []string{"value"}}}}}
	got, err := c.Convert(42, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Graph.SchemaVersion != 3 || got.Graph.EntryPoints["main"] != "manual" || got.Graph.EntryPoints["job"] != "manual" {
		t.Fatal("generic entry mapping lost an entry")
	}
	value, err := graph.Resolve(got.Graph.Nodes[3], nil, graph.Context{Nodes: map[string]map[string]any{"a": {"value": "a"}, "b": {"value": "b"}}})
	if err != nil || value["x"] != "b" {
		t.Fatal("explicit collision mapping did not select its source")
	}
	c.Mappings.Expressions["42/end/binding/x"] = ExpressionMapping{Expression: `nodes["b"].value`}
	if _, err := c.Convert(42, raw); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationAndLoopSecretMapping(t *testing.T) {
	raw := json.RawMessage(`{"schemaVersion":1,"nodes":[{"nodeInstanceId":"manual","nodeType":"core.manual","nodeVersion":"1.0.0","config":{},"position":{"x":0,"y":0}},{"nodeInstanceId":"condition","nodeType":"example.flow.action","nodeVersion":"1.0.0","config":{},"position":{"x":1,"y":0}},{"nodeInstanceId":"notify","nodeType":"official.notification.in_app","nodeVersion":"1.0.0","config":{},"inputBindings":{"subjectKey":{"kind":"condition_subject","sources":[{"nodeInstanceId":"condition","branch":"true"}]},"message":{"kind":"condition_message","sources":[{"nodeInstanceId":"condition","branch":"true"}]}},"position":{"x":2,"y":0}},{"nodeInstanceId":"loop","nodeType":"core.loop","nodeVersion":"1.0.0","config":{"maxIterations":3,"timeoutSeconds":30,"exitCondition":"input.iteration > 1","body":{"schemaVersion":1,"nodes":[{"nodeInstanceId":"item","nodeType":"core.loop_item","nodeVersion":"1.0.0","config":{},"position":{"x":0,"y":0}},{"nodeInstanceId":"request","nodeType":"example.flow.action","nodeVersion":"1.0.0","config":{},"position":{"x":1,"y":0}},{"nodeInstanceId":"end","nodeType":"core.loop_end","nodeVersion":"1.0.0","config":{},"position":{"x":2,"y":0}}],"edges":[]}},"position":{"x":3,"y":0}}],"edges":[{"edgeId":"mc","sourceNodeInstanceId":"manual","sourcePort":"out","targetNodeInstanceId":"condition","targetPort":"in"},{"edgeId":"cn","sourceNodeInstanceId":"condition","sourcePort":"true","targetNodeInstanceId":"notify","targetPort":"in"}]}`)
	c := converter()
	got, err := c.Convert(7, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeIDs["loop.request"] != "loop.request" || got.SecretFields["loop.request"]["token"] != "token" {
		t.Fatal("Loop secret must use its real expanded runtime identity")
	}
	if len(got.Graph.Nodes) != 5 || got.Graph.Nodes[2].InputBindings["message"].Kind != "field" {
		t.Fatal("legacy notification composition was not made explicit")
	}
	if !strings.Contains(got.Graph.Edges[1].Condition, `nodes["condition"].ready`) {
		t.Fatal("old ready gate was lost")
	}
	for _, sample := range []struct {
		output map[string]any
		allow  bool
	}{{map[string]any{}, true}, {map[string]any{"ready": true}, true}, {map[string]any{"ready": false}, false}, {map[string]any{"ready": "optional-non-boolean"}, true}} {
		value, err := graph.Evaluate(got.Graph.Edges[1].Condition, graph.Context{Nodes: map[string]map[string]any{"condition": sample.output}})
		if err != nil || value != sample.allow {
			t.Fatal("migration changed optional legacy ready semantics", sample.output, value, err)
		}
	}
	var old legacyGraph
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	old.Nodes[2].InputBindings["active"] = legacyBinding{Binding: graph.Binding{Kind: "condition_entry"}, Sources: []legacySource{{NodeInstanceID: "condition", Branch: "true"}}}
	withIncoming, _ := json.Marshal(old)
	if _, err := c.Convert(7, withIncoming); err == nil {
		t.Fatal("composition silently changed incoming semantics")
	}
	c.Mappings.Expressions = map[string]ExpressionMapping{"7/notify/binding/active": {Expression: `nodes["condition"].entered`}}
	explicit, err := c.Convert(7, withIncoming)
	if err != nil || explicit.Graph.Nodes[2].InputBindings["active"].Expression != `nodes["condition"].entered` {
		t.Fatal("explicit expression did not resolve composition dependency", err)
	}
	c.Mappings.NodeVersions = nil
	if _, err := c.Convert(7, raw); err == nil {
		t.Fatal("external node versions must be mapped explicitly")
	}
}

func migrationRow(t *testing.T, value map[string]any) row {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var r row
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPlanCredentialAndResultOwnershipBoundaries(t *testing.T) {
	c := converter()
	desc := c.Catalog["example.flow.action"]
	desc.ConfigSchema = json.RawMessage(`{"type":"object","properties":{"token":{"type":"string","x-coinsphere-secret":true}},"required":["token"]}`)
	c.Catalog[desc.Type] = desc
	g := json.RawMessage(`{"schemaVersion":2,"entryPoints":{"realtime":"manual"},"nodes":[{"nodeInstanceId":"manual","nodeType":"core.manual","nodeVersion":"1.0.0","config":{},"position":{"x":0,"y":0}},{"nodeInstanceId":"task","nodeType":"example.flow.action","nodeVersion":"1.0.0","config":{},"position":{"x":100,"y":0}},{"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":200,"y":0}}],"edges":[{"edgeId":"mt","sourceNodeInstanceId":"manual","sourcePort":"out","targetNodeInstanceId":"task","targetPort":"in"},{"edgeId":"te","sourceNodeInstanceId":"task","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}]}`)
	source := Snapshot{Identity: "synthetic-source", Fingerprint: "synthetic-fingerprint", tables: map[string][]row{
		"users":              {migrationRow(t, map[string]any{"id": 1, "is_active": true})},
		"roles":              {migrationRow(t, map[string]any{"id": 1})},
		"workflows":          {migrationRow(t, map[string]any{"id": 7, "active_revision_id": 11, "created_by": 1})},
		"workflow_revisions": {migrationRow(t, map[string]any{"id": 11, "workflow_id": 7, "graph_json": g})},
		"result_views":       {migrationRow(t, map[string]any{"id": 41, "plugin_id": "example.flow", "page_key": "tasks", "status": "active", "created_by": 1, "scope_json": map[string]any{"workflowId": 7, "nodeId": "task"}, "filters_json": map[string]any{}, "allowed_actions": []string{"ack"}})},
	}}
	page := sdk.ResultPageDescriptor{PageKey: "tasks", PermissionCode: "plugins.example.flow.read", ScopeSchema: json.RawMessage(`{"type":"object","properties":{"workflowId":{"type":"integer"},"nodeId":{"type":"string"}},"required":["workflowId","nodeId"]}`), FilterSchema: json.RawMessage(`{"type":"object"}`), Actions: []string{"ack"}, ActionPermissions: map[string]string{"ack": "plugins.example.flow.ack"}, Resources: func(raw json.RawMessage) ([]sdk.WorkflowResource, error) {
		var r struct {
			WorkflowID int64  `json:"workflowId"`
			NodeID     string `json:"nodeId"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		return []sdk.WorkflowResource{{WorkflowID: r.WorkflowID, NodeInstanceID: r.NodeID}}, nil
	}}
	catalog := Catalog{Converter: c, Pages: map[string]sdk.ResultPageDescriptor{"example.flow/tasks": page}}
	plan := BuildPlan(source, "synthetic-target", catalog, c.Mappings)
	if err := plan.Validate(); err != nil {
		t.Fatal(plan.Issues, err)
	}
	if len(plan.Dependencies) != 1 || plan.Dependencies[0].Code != "required_secret" {
		t.Fatal("missing credential was not reported", plan.Dependencies)
	}
	view := source.tables["result_views"][0]
	for _, id := range []string{"manual", "missing"} {
		put(view, "scope_json", map[string]any{"workflowId": 7, "nodeId": id})
		if err := BuildPlan(source, "synthetic-target", catalog, c.Mappings).Validate(); err == nil {
			t.Fatal("view accepted foreign or absent node", id)
		}
	}
	put(view, "scope_json", map[string]any{"workflowId": 7, "nodeId": "task"})
	for _, kind := range []string{"user", "role"} {
		table := "result_view_" + kind + "_grants"
		source.tables[table] = []row{migrationRow(t, map[string]any{"view_id": 41, kind + "_id": 99})}
		if err := BuildPlan(source, "synthetic-target", catalog, c.Mappings).Validate(); err == nil {
			t.Fatal("missing grant principal was accepted", kind)
		}
		delete(source.tables, table)
	}
	put(view, "created_by", 99)
	if err := BuildPlan(source, "synthetic-target", catalog, c.Mappings).Validate(); err == nil {
		t.Fatal("missing view creator was accepted")
	}
}

func TestPostgresImportIsAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	sourceDB, _ := testdb.Open(t, false)
	legacy, err := goose.NewProvider(goose.DialectPostgres, sourceDB, os.DirFS("../migration/sql"), goose.WithTableName("schema_migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Up(ctx); err != nil {
		t.Fatal(err)
	}
	sourceTx, err := sourceDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceTx.Rollback()
	for _, q := range []string{
		`INSERT INTO roles(id,code) VALUES(1,'R_SUPER')`,
		`INSERT INTO users(id,username) VALUES(1,'synthetic-owner')`,
		`INSERT INTO user_roles(id,user_id,role_id) VALUES(1,1,1)`,
		`INSERT INTO workflows(id,name,mode,status,main_trigger_node_id,created_by) VALUES(7,'Synthetic workflow','batch','inactive','manual',1)`,
		`INSERT INTO workflow_revisions(id,workflow_id,revision_number,graph_json,node_versions,main_trigger_node_id,created_by) VALUES(20,7,1,'{"schemaVersion":1,"nodes":[{"nodeInstanceId":"manual","nodeType":"core.manual","nodeVersion":"1.0.0","config":{},"position":{"x":0,"y":0}},{"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":100,"y":0}}],"edges":[{"edgeId":"end-edge","sourceNodeInstanceId":"manual","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}]}','{}','manual',1)`,
		`UPDATE workflows SET active_revision_id=20 WHERE id=7`,
		`INSERT INTO workflow_runtimes(workflow_id) VALUES(7)`,
		`INSERT INTO ai_model_configs(id,display_name,base_url,model_name,api_key_ciphertext,created_by,updated_by) VALUES(5,'Synthetic model','https://example.invalid','synthetic','opaque-synthetic',1,1)`,
	} {
		if _, err := sourceTx.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := sourceTx.Commit(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadSource(ctx, sourceDB, "synthetic-source")
	if err != nil {
		t.Fatal(err)
	}
	target, gdb := testdb.Open(t, true)
	t.Run("isolated legacy recovery point", func(t *testing.T) {
		containerID := os.Getenv("COINSPHERE_TEST_POSTGRES_CONTAINER")
		if containerID == "" {
			t.Skip("PostgreSQL 16 recovery tools are not configured")
		}
		restored, _ := testdb.Open(t, false)
		var sourceName, restoredName string
		if err := sourceDB.QueryRowContext(ctx, "SELECT current_database()").Scan(&sourceName); err != nil {
			t.Fatal(err)
		}
		if err := restored.QueryRowContext(ctx, "SELECT current_database()").Scan(&restoredName); err != nil {
			t.Fatal(err)
		}
		backup, err := os.Create(filepath.Join(t.TempDir(), "synthetic-legacy.dump"))
		if err != nil {
			t.Fatal(err)
		}
		defer backup.Close()
		dump := exec.CommandContext(ctx, "docker", "exec", containerID, "pg_dump", "-U", "coinsphere_test", "-d", sourceName, "-Fc")
		dump.Stdout = backup
		if err := dump.Run(); err != nil {
			t.Fatalf("isolated PostgreSQL 16 backup failed: %v", err)
		}
		if _, err := backup.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		restore := exec.CommandContext(ctx, "docker", "exec", "-i", containerID, "pg_restore", "-U", "coinsphere_test", "-d", restoredName, "--exit-on-error", "--no-owner")
		restore.Stdin = backup
		if err := restore.Run(); err != nil {
			t.Fatalf("isolated PostgreSQL 16 restore failed: %v", err)
		}
		recovered, err := ReadSource(ctx, restored, "synthetic-source")
		if err != nil || recovered.Version != snapshot.Version || Digest(recovered.tables) != Digest(snapshot.tables) {
			t.Fatal("legacy recovery point lost schema or workflow/configuration facts", err)
		}
	})
	app := service.NewApp(gdb, &config.AppConfig{Auth: config.AuthConfig{SecretKey: "synthetic-target-key", PasswordIterations: 1}}, sdk.NewRegistry())
	catalog := Catalog{Converter: Converter{Catalog: app.WorkflowNodeCatalog(), NodePlugins: map[string]string{}, Validate: app.ValidateWorkflowGraph}, Permissions: map[string]sdk.PermissionDescriptor{}}
	identity, err := TargetIdentity(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildPlan(snapshot, identity, catalog, Mappings{})
	if err := plan.Validate(); err != nil {
		t.Fatalf("cannot build synthetic plan: %v %v", err, plan.Issues)
	}
	if _, err := Apply(ctx, target, snapshot, plan, catalog, func(string) (string, error) { return "", errors.New("synthetic failure") }); err == nil {
		t.Fatal("injected mid-import failure was ignored")
	}
	for _, table := range []string{"users", "workflows", "workflow_migration_batches"} {
		var count int
		if err := target.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failure left partial %s assets", table)
		}
	}
	result, err := Apply(ctx, target, snapshot, plan, catalog, func(string) (string, error) { return "opaque-target-synthetic", nil })
	if err != nil || !result.Verified {
		t.Fatalf("apply failed: %v", err)
	}
	result, err = Apply(ctx, target, snapshot, plan, catalog, func(string) (string, error) { t.Error("completed batch should not rebind secrets"); return "", nil })
	if err != nil || !result.AlreadyApplied {
		t.Fatalf("batch was not idempotent: %v", err)
	}
	if _, err := Verify(ctx, target, plan); err != nil {
		t.Fatal(err)
	}
	unchanged, err := ReadSource(ctx, sourceDB, "synthetic-source")
	if err != nil || unchanged.Fingerprint != snapshot.Fingerprint {
		t.Fatal("import changed the independently recoverable source", err)
	}
	var status string
	var graphJSON string
	if err := target.QueryRow("SELECT w.status,r.graph_json::text FROM workflows w JOIN workflow_revisions r ON r.id=w.draft_revision_id WHERE w.id=7").Scan(&status, &graphJSON); err != nil || status != "inactive" || !strings.Contains(graphJSON, `"schemaVersion": 3`) {
		t.Fatal("import did not create inactive generation 4 definition")
	}
	if _, err := target.Exec("UPDATE workflows SET name='Human edit' WHERE id=7"); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(ctx, target, plan); err == nil {
		t.Fatal("verification must detect target edits")
	}
	changed := snapshot
	changed.Fingerprint = Digest("changed")
	if _, err := Apply(ctx, target, changed, plan, catalog, nil); err == nil {
		t.Fatal("changed source was accepted")
	}
	raw, _ := json.Marshal(plan)
	if strings.Contains(string(raw), "opaque-synthetic") || strings.Contains(string(raw), "synthetic-owner") {
		t.Fatal("plan exposed secret or identity data")
	}
}

func TestImportPluginVersionMustMatchCompiledCatalog(t *testing.T) {
	ctx := context.Background()
	database, _ := testdb.Open(t, true)
	catalog := Catalog{Plugins: []sdk.PluginDescriptor{{ID: "example.business", Version: "1.0.0"}}}
	if _, err := database.Exec("INSERT INTO plugin_installations(plugin_id,version,schema_name,source_path,status) VALUES('example.business','1.1.0','plugin_example_business','synthetic','installed')"); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"1.1.0", "1.0.0"} {
		if _, err := database.Exec("UPDATE plugin_installations SET version=$1 WHERE plugin_id='example.business'", version); err != nil {
			t.Fatal(err)
		}
		tx, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			t.Fatal(err)
		}
		err = insertReference(ctx, tx, catalog, "example.business", "workflow", "7")
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		if (version == "1.0.0") != (err == nil) {
			t.Fatal("import ignored exact installed/compiled plugin version", version, err)
		}
	}
}
