package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/migration"
	"coinsphere/backend/internal/security"
	"coinsphere/backend/plugin/official"
	"coinsphere/backend/plugin/sdk"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var workflowSchemaSequence atomic.Uint64

type workflowCountingAction struct{ calls atomic.Int32 }

func (a *workflowCountingAction) Execute(_ context.Context, _ sdk.ActionRequest) (sdk.ActionResult, error) {
	a.calls.Add(1)
	return sdk.ActionResult{Output: json.RawMessage(`{"value":"sent"}`)}, nil
}

type workflowBackpressureTrigger struct {
	runs    atomic.Int32
	fail    atomic.Bool
	started chan int32
	emitted chan string
}

func (t *workflowBackpressureTrigger) Run(ctx context.Context, _ sdk.TriggerRequest, emitter sdk.Emitter) error {
	run := t.runs.Add(1)
	t.started <- run
	if t.fail.Load() {
		return errors.New("trigger failure")
	}
	for _, id := range []string{"stream-1", "stream-2"} {
		event := cloudevents.NewEvent()
		event.SetID(id)
		event.SetSource("urn:test:stream")
		event.SetType("test.stream")
		event.SetTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		event.SetExtension("partitionkey", "stream-partition")
		if err := event.SetData(cloudevents.ApplicationJSON, map[string]any{"id": id}); err != nil {
			return err
		}
		if err := emitter.Emit(ctx, event); err != nil {
			return err
		}
		t.emitted <- fmt.Sprintf("%d:%s", run, id)
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestWorkflowRevisionAndLifecycleTransaction(t *testing.T) {
	app, database, ownerID := openWorkflowTestApp(t)
	principal := &Principal{User: &db.SystemUser{ID: ownerID}}
	workflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Lifecycle"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if workflow.Status != WorkflowStatusPaused || workflow.ActiveRevisionID <= 0 || workflow.Runtime.HealthSummary != "idle" {
		t.Fatalf("created workflow = %#v", workflow)
	}

	revision2, err := app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: workflow.ActiveRevisionID, Graph: jsonMessage(blankWorkflowGraph),
	}, principal)
	if err != nil || revision2.RevisionNumber != 2 {
		t.Fatalf("second revision = %#v, err = %v", revision2, err)
	}
	if _, err := app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: revision2.ID, Graph: jsonMessage(`{"schemaVersion":1}`),
	}, principal); err == nil {
		t.Fatal("invalid graph was saved")
	}

	results := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
				ExpectedActiveRevisionID: revision2.ID, Graph: jsonMessage(blankWorkflowGraph),
			}, principal)
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrConflict) {
			conflicted++
		} else {
			t.Fatalf("concurrent save error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent saves = success:%d conflict:%d", succeeded, conflicted)
	}
	detail, err := app.GetWorkflow(context.Background(), workflow.ID)
	if err != nil || detail.ActiveRevisionID == revision2.ID {
		t.Fatalf("active revision did not advance: %#v, err = %v", detail, err)
	}
	revisions, err := app.ListWorkflowRevisions(context.Background(), workflow.ID)
	if err != nil || len(revisions) != 3 {
		t.Fatalf("revisions = %#v, err = %v", revisions, err)
	}
	if _, err := database.Exec(`UPDATE workflow_revisions SET revision_number = 99 WHERE id = $1`, revision2.ID); err == nil {
		t.Fatal("database updated an immutable revision")
	}

	if detail, err = app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil || detail.Status != WorkflowStatusRunning {
		t.Fatalf("start = %#v, err = %v", detail, err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "archive"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("running archive error = %v", err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	if detail, err = app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "archive"}); err != nil || detail.Status != WorkflowStatusArchived || detail.ArchivedAt == "" {
		t.Fatalf("archive = %#v, err = %v", detail, err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("archived start error = %v", err)
	}
	if _, err := app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: detail.ActiveRevisionID, Graph: jsonMessage(blankWorkflowGraph),
	}, principal); !errors.Is(err, ErrConflict) {
		t.Fatalf("archived save error = %v", err)
	}
}

func TestQuantWorkflowTemplatesCreateAndValidate(t *testing.T) {
	app, _, ownerID := openWorkflowTestApp(t)
	principal := &Principal{User: &db.SystemUser{ID: ownerID}}
	for _, template := range []struct {
		key  string
		mode string
	}{
		{WorkflowTemplateQuantData, WorkflowModeStream},
		{WorkflowTemplateQuantLive, WorkflowModeEvent},
		{WorkflowTemplateBacktest, WorkflowModeBatch},
		{WorkflowTemplatePaper, WorkflowModeEvent},
	} {
		workflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: template.key, TemplateKey: template.key}, principal)
		if err != nil || workflow.Mode != template.mode {
			t.Fatalf("template %s workflow=%#v err=%v", template.key, workflow, err)
		}
		if _, err := app.GetWorkflowRevision(context.Background(), workflow.ID, workflow.ActiveRevisionID); err != nil {
			t.Fatalf("template %s revision: %v", template.key, err)
		}
	}
}

func TestWorkflowRevisionSecretsAreVersionedAndHidden(t *testing.T) {
	app, database, ownerID := openWorkflowTestApp(t)
	principal := &Principal{User: &db.SystemUser{ID: ownerID}}
	workflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Secrets"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	plain := "integration-secret"
	revision2, err := app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: workflow.ActiveRevisionID, Graph: jsonMessage(testPluginWorkflowGraph),
		SecretChanges: []WorkflowSecretChange{{NodeInstanceID: "transform", Field: "token", Value: &plain}},
	}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if !revision2.SecretFields["transform"]["token"] || strings.Contains(string(revision2.Graph), plain) {
		t.Fatalf("revision exposed or omitted secret status: %#v", revision2)
	}
	var encrypted string
	if err := database.QueryRow(`SELECT encrypted_value FROM workflow_secret_bindings WHERE revision_id = $1`, revision2.ID).Scan(&encrypted); err != nil || encrypted == "" || encrypted == plain {
		t.Fatalf("encrypted value = %q, err = %v", encrypted, err)
	}

	revision3, err := app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: revision2.ID, Graph: jsonMessage(testPluginWorkflowGraph),
	}, principal)
	if err != nil || !revision3.SecretFields["transform"]["token"] {
		t.Fatalf("carried secret = %#v, err = %v", revision3.SecretFields, err)
	}
	if _, err := app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: revision3.ID, Graph: jsonMessage(testPluginWorkflowGraph),
		SecretChanges: []WorkflowSecretChange{{NodeInstanceID: "transform", Field: "token", Remove: true}},
	}, principal); err == nil {
		t.Fatal("required secret was removed")
	}
	detail, err := app.GetWorkflow(context.Background(), workflow.ID)
	if err != nil || detail.ActiveRevisionID != revision3.ID {
		t.Fatalf("failed secret save changed active revision: %#v, err = %v", detail, err)
	}
	if _, err := database.Exec(`UPDATE workflow_secret_bindings SET encrypted_value = 'changed' WHERE revision_id = $1`, revision3.ID); err == nil {
		t.Fatal("database updated an immutable secret binding")
	}
}

func TestWorkflowBatchFixesRevisionAndResumesFromCheckpoint(t *testing.T) {
	app, database, ownerID := openWorkflowTestApp(t)
	principal := &Principal{User: &db.SystemUser{ID: ownerID}}
	workflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Batch"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	secret := "batch-secret"
	revision, err := app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: workflow.ActiveRevisionID, Graph: jsonMessage(testPluginWorkflowGraph),
		SecretChanges: []WorkflowSecretChange{{NodeInstanceID: "transform", Field: "token", Value: &secret}},
	}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	batch, err := app.CreateWorkflowBatch(context.Background(), workflow.ID, principal)
	if err != nil {
		t.Fatal(err)
	}
	newRevision, err := app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: revision.ID, Graph: jsonMessage(testPluginWorkflowGraph),
	}, principal)
	if err != nil || newRevision.ID == revision.ID {
		t.Fatalf("new revision = %#v, err = %v", newRevision, err)
	}
	claimed, ok, err := app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok || claimed.ID != batch.ID || claimed.RevisionID != revision.ID {
		t.Fatalf("claimed batch = %#v, ok = %t, err = %v", claimed, ok, err)
	}
	app.executeWorkflowBatch(context.Background(), claimed)
	finished, err := app.GetWorkflowBatch(context.Background(), batch.ID)
	if err != nil || finished.Status != BatchStatusSucceeded || finished.RevisionID != revision.ID {
		t.Fatalf("finished batch = %#v, err = %v", finished, err)
	}
	var checkpoints int64
	if err := database.QueryRow(`SELECT COUNT(*) FROM workflow_checkpoints WHERE batch_id = $1 AND revision_id = $2`, batch.ID, revision.ID).Scan(&checkpoints); err != nil || checkpoints != 3 {
		t.Fatalf("checkpoints = %d, err = %v", checkpoints, err)
	}
}

func TestWorkflowLoopPersistsEachIteration(t *testing.T) {
	app, database, ownerID := openWorkflowTestApp(t)
	principal := &Principal{User: &db.SystemUser{ID: ownerID}}
	workflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Loop"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: workflow.ActiveRevisionID, Graph: jsonMessage(testLoopWorkflowGraph),
	}, principal); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	batch, err := app.CreateWorkflowBatch(context.Background(), workflow.ID, principal)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok || claimed.ID != batch.ID {
		t.Fatalf("claimed batch = %#v, ok = %t, err = %v", claimed, ok, err)
	}
	app.executeWorkflowBatch(context.Background(), claimed)
	finished, err := app.GetWorkflowBatch(context.Background(), batch.ID)
	if err != nil || finished.Status != BatchStatusSucceeded {
		t.Fatalf("finished batch = %#v, err = %v", finished, err)
	}
	var runs, iterations int64
	if err := database.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT loop_iteration) FROM workflow_node_runs WHERE batch_id = $1 AND node_instance_id LIKE 'loop.%'`, batch.ID).Scan(&runs, &iterations); err != nil {
		t.Fatal(err)
	}
	if runs != 6 || iterations != 2 {
		t.Fatalf("loop runs = %d, iterations = %d", runs, iterations)
	}
	var output string
	if err := database.QueryRow(`SELECT output_json FROM workflow_checkpoints WHERE batch_id = $1 AND node_instance_id = 'loop' AND loop_iteration = 0`, batch.ID).Scan(&output); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Iterations int  `json:"iterations"`
		Exited     bool `json:"exited"`
	}
	if json.Unmarshal([]byte(output), &result) != nil || result.Iterations != 2 || !result.Exited {
		t.Fatalf("loop output = %s", output)
	}
}

func TestWorkflowWebhookAuthenticatesAndDeduplicates(t *testing.T) {
	app, database, ownerID := openWorkflowTestApp(t)
	principal := &Principal{User: &db.SystemUser{ID: ownerID}}
	workflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Webhook", TemplateKey: WorkflowTemplateWebhook}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("webhook without secret start error = %v", err)
	}
	secret := " webhook-test-secret "
	if _, err := app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: workflow.ActiveRevisionID, Graph: jsonMessage(webhookWorkflowGraph),
		SecretChanges: []WorkflowSecretChange{{NodeInstanceID: "webhook-trigger", Field: "secret", Value: &secret}},
	}, principal); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	data := map[string]any{"value": "accepted"}
	if _, err := app.PublishWorkflowWebhook(context.Background(), workflow.ID, "wrong", "event-1", "account-1", data); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong webhook secret error = %v", err)
	}
	first, err := app.PublishWorkflowWebhook(context.Background(), workflow.ID, secret, "event-1", "account-1", data)
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.PublishWorkflowWebhook(context.Background(), workflow.ID, secret, "event-1", "account-1", data)
	if err != nil || second.ID != first.ID {
		t.Fatalf("duplicate webhook = %#v, err = %v", second, err)
	}
	results := make(chan WorkflowEventView, 4)
	errors := make(chan error, 4)
	var wait sync.WaitGroup
	for range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := app.PublishWorkflowWebhook(context.Background(), workflow.ID, secret, "event-2", "account-1", data)
			results <- result
			errors <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errors)
	var concurrentID int64
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent duplicate webhook: %v", err)
		}
	}
	for result := range results {
		if concurrentID == 0 {
			concurrentID = result.ID
		} else if result.ID != concurrentID {
			t.Fatalf("concurrent webhook event IDs differ: %d and %d", concurrentID, result.ID)
		}
	}
	var events, deliveries, batches int64
	if err := database.QueryRow(`SELECT COUNT(*) FROM workflow_event_records WHERE source = $1`, fmt.Sprintf("urn:coinsphere:connector:webhook:%d", workflow.ID)).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM workflow_event_deliveries WHERE workflow_id = $1`, workflow.ID).Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM execution_batches WHERE workflow_id = $1 AND trigger_type = 'webhook'`, workflow.ID).Scan(&batches); err != nil {
		t.Fatal(err)
	}
	if events != 2 || deliveries != 2 || batches != 2 {
		t.Fatalf("webhook events=%d deliveries=%d batches=%d", events, deliveries, batches)
	}
}

func TestWorkflowEventsPreserveIdentityPartitionOrderAndOutbox(t *testing.T) {
	app, database, ownerID := openWorkflowTestApp(t)
	principal := &Principal{User: &db.SystemUser{ID: ownerID}}
	workflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Events", TemplateKey: WorkflowTemplateEvent}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE workflow_runtimes SET max_concurrent_batches = 3 WHERE workflow_id = $1`, workflow.ID); err != nil {
		t.Fatal(err)
	}
	newEvent := func(id, partition string, data map[string]any) cloudevents.Event {
		event := cloudevents.NewEvent()
		event.SetID(id)
		event.SetSource("urn:test:events")
		event.SetType("example.event")
		event.SetTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		event.SetExtension("partitionkey", partition)
		if err := event.SetData(cloudevents.ApplicationJSON, data); err != nil {
			t.Fatal(err)
		}
		return event
	}

	first, err := app.PublishWorkflowEvent(context.Background(), newEvent("event-1", "account-a", map[string]any{"value": "one"}))
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := app.PublishWorkflowEvent(context.Background(), newEvent("event-1", "account-a", map[string]any{"value": "one"}))
	if err != nil || duplicate.ID != first.ID {
		t.Fatalf("duplicate event = %#v, err = %v", duplicate, err)
	}
	if _, err := app.PublishWorkflowEvent(context.Background(), newEvent("event-1", "account-a", map[string]any{"value": "changed"})); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting event identity error = %v", err)
	}
	if _, err := app.PublishWorkflowEvent(context.Background(), newEvent("event-2", "account-a", map[string]any{"value": "two"})); err != nil {
		t.Fatal(err)
	}
	if _, err := app.PublishWorkflowEvent(context.Background(), newEvent("event-3", "account-b", map[string]any{"value": "three"})); err != nil {
		t.Fatal(err)
	}

	firstClaim, ok, err := app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok || firstClaim.PartitionKey != "account-a" {
		t.Fatalf("first claim = %#v, ok=%t, err=%v", firstClaim, ok, err)
	}
	crossPartition, ok, err := app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok || crossPartition.PartitionKey != "account-b" {
		t.Fatalf("cross-partition claim = %#v, ok=%t, err=%v", crossPartition, ok, err)
	}
	if _, err := database.Exec(`UPDATE execution_batches SET status = 'waiting', lease_token = NULL, lease_expires_at = NULL WHERE id = $1`, firstClaim.ID); err != nil {
		t.Fatal(err)
	}
	releasedPartition, ok, err := app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok || releasedPartition.PartitionKey != "account-a" || releasedPartition.ID == firstClaim.ID {
		t.Fatalf("released partition claim = %#v, ok=%t, err=%v", releasedPartition, ok, err)
	}

	outboxEvent := newEvent("outbox-1", "outbox", map[string]any{"value": "persisted"})
	outboxEvent.SetType("unmatched.event")
	if err := app.DB.Transaction(func(tx *gorm.DB) error { return app.enqueueWorkflowEvent(tx, outboxEvent) }); err != nil {
		t.Fatal(err)
	}
	if err := app.DB.Transaction(func(tx *gorm.DB) error { return app.enqueueWorkflowEvent(tx, outboxEvent) }); err != nil {
		t.Fatalf("duplicate outbox event: %v", err)
	}
	if err := app.dispatchWorkflowEventOutbox(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := database.QueryRow(`SELECT status FROM workflow_event_outbox WHERE source = $1 AND event_id = $2`, outboxEvent.Source(), outboxEvent.ID()).Scan(&status); err != nil || status != "published" {
		t.Fatalf("outbox status = %q, err = %v", status, err)
	}
	if _, err := database.Exec(`UPDATE execution_batches SET trigger_type = 'failure' WHERE id = $1`, releasedPartition.ID); err != nil {
		t.Fatal(err)
	}
	app.failWorkflowBatch(releasedPartition.ID, "test")
	var outboxCount int64
	if err := database.QueryRow(`SELECT COUNT(*) FROM workflow_event_outbox`).Scan(&outboxCount); err != nil || outboxCount != 1 {
		t.Fatalf("failure handler outbox count = %d, err = %v", outboxCount, err)
	}
}

func TestWorkflowHumanTasksSupersedeDecideAndExpireOnce(t *testing.T) {
	app, database, ownerID := openWorkflowTestApp(t)
	principal := &Principal{User: &db.SystemUser{ID: ownerID}}
	workflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Human tasks"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	graph := `{
  "schemaVersion":1,
  "nodes":[
    {"nodeInstanceId":"manual","nodeType":"core.manual","nodeVersion":"1.0.0","config":{},"position":{"x":80,"y":120}},
    {"nodeInstanceId":"approve","nodeType":"core.human_approval","nodeVersion":"1.0.0","config":{"taskType":"approval","prompt":"Review action","expiresSeconds":3600},"inputBindings":{"businessKey":{"kind":"literal","value":"order-1"}},"position":{"x":360,"y":120}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":640,"y":120}}
  ],
  "edges":[
    {"edgeId":"manual-approve","sourceNodeInstanceId":"manual","sourcePort":"out","targetNodeInstanceId":"approve","targetPort":"in"},
    {"edgeId":"approve-end","sourceNodeInstanceId":"approve","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`
	if _, err := app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: workflow.ActiveRevisionID, Graph: jsonMessage(graph),
	}, principal); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	runUntilWaiting := func() WorkflowBatchView {
		batch, err := app.CreateWorkflowBatch(context.Background(), workflow.ID, principal)
		if err != nil {
			t.Fatal(err)
		}
		claimed, ok, err := app.claimWorkflowBatch(context.Background(), time.Now().UTC())
		if err != nil || !ok || claimed.ID != batch.ID {
			t.Fatalf("human task claim = %#v, ok=%t, err=%v", claimed, ok, err)
		}
		app.executeWorkflowBatch(context.Background(), claimed)
		waiting, err := app.GetWorkflowBatch(context.Background(), batch.ID)
		if err != nil || waiting.Status != BatchStatusWaiting {
			t.Fatalf("human task batch = %#v, err=%v", waiting, err)
		}
		return waiting
	}

	firstBatch := runUntilWaiting()
	secondBatch := runUntilWaiting()
	var firstTaskID, secondTaskID int64
	var firstStatus, secondStatus string
	if err := database.QueryRow(`SELECT id, status FROM workflow_human_tasks WHERE batch_id = $1`, firstBatch.ID).Scan(&firstTaskID, &firstStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT id, status FROM workflow_human_tasks WHERE batch_id = $1`, secondBatch.ID).Scan(&secondTaskID, &secondStatus); err != nil {
		t.Fatal(err)
	}
	if firstStatus != "superseded" || secondStatus != "pending" {
		t.Fatalf("human task statuses = %q, %q", firstStatus, secondStatus)
	}
	claimed, ok, err := app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok || claimed.ID != firstBatch.ID {
		t.Fatalf("superseded batch claim = %#v, ok=%t, err=%v", claimed, ok, err)
	}
	app.executeWorkflowBatch(context.Background(), claimed)
	if finished, err := app.GetWorkflowBatch(context.Background(), firstBatch.ID); err != nil || finished.Status != BatchStatusSucceeded {
		t.Fatalf("superseded batch = %#v, err=%v", finished, err)
	}
	if _, err := app.DecideWorkflowHumanTask(context.Background(), secondTaskID, WorkflowHumanTaskDecision{Action: "approve"}, principal); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DecideWorkflowHumanTask(context.Background(), secondTaskID, WorkflowHumanTaskDecision{Action: "reject"}, principal); !errors.Is(err, ErrConflict) {
		t.Fatalf("second human task decision error = %v", err)
	}
	claimed, ok, err = app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok || claimed.ID != secondBatch.ID {
		t.Fatalf("approved batch claim = %#v, ok=%t, err=%v", claimed, ok, err)
	}
	app.executeWorkflowBatch(context.Background(), claimed)

	thirdBatch := runUntilWaiting()
	var thirdTaskID int64
	if err := database.QueryRow(`UPDATE workflow_human_tasks SET expires_at = CURRENT_TIMESTAMP - INTERVAL '1 second' WHERE batch_id = $1 RETURNING id`, thirdBatch.ID).Scan(&thirdTaskID); err != nil {
		t.Fatal(err)
	}
	if err := app.expireWorkflowHumanTasks(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var thirdStatus string
	if err := database.QueryRow(`SELECT status FROM workflow_human_tasks WHERE id = $1`, thirdTaskID).Scan(&thirdStatus); err != nil || thirdStatus != "expired" {
		t.Fatalf("expired task status = %q, err=%v", thirdStatus, err)
	}
	claimed, ok, err = app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok || claimed.ID != thirdBatch.ID {
		t.Fatalf("expired batch claim = %#v, ok=%t, err=%v", claimed, ok, err)
	}
	app.executeWorkflowBatch(context.Background(), claimed)
	if finished, err := app.GetWorkflowBatch(context.Background(), thirdBatch.ID); err != nil || finished.Status != BatchStatusSucceeded {
		t.Fatalf("expired batch = %#v, err=%v", finished, err)
	}
}

func TestWorkflowDiagnosticReplayReusesSideEffectCheckpoint(t *testing.T) {
	app, database, ownerID := openWorkflowTestApp(t)
	principal := &Principal{User: &db.SystemUser{ID: ownerID}}
	action := &workflowCountingAction{}
	if err := app.Plugins.RegisterPlugin(sdk.PluginDescriptor{
		ID: "official.side-effect-test", Name: "Side effect test", Version: "1.0.0", Contributes: []string{"nodes"},
	}, func(registrar sdk.Registrar) error {
		return registrar.Action(sdk.NodeDescriptor{
			Type: "official.side-effect-test.send", Version: "1.0.0", Kind: sdk.NodeKindAction,
			ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`),
			UISchema:     json.RawMessage(`{}`), InputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
			Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectNotification, State: sdk.StateStateless,
		}, action)
	}); err != nil {
		t.Fatal(err)
	}
	workflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Diagnostic replay"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	graph := `{"schemaVersion":1,"nodes":[
    {"nodeInstanceId":"manual","nodeType":"core.manual","nodeVersion":"1.0.0","config":{},"position":{"x":80,"y":120}},
    {"nodeInstanceId":"send","nodeType":"official.side-effect-test.send","nodeVersion":"1.0.0","config":{},"position":{"x":360,"y":120}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":640,"y":120}}
  ],"edges":[
    {"edgeId":"manual-send","sourceNodeInstanceId":"manual","sourcePort":"out","targetNodeInstanceId":"send","targetPort":"in"},
    {"edgeId":"send-end","sourceNodeInstanceId":"send","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]}`
	if _, err := app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: workflow.ActiveRevisionID, Graph: jsonMessage(graph),
	}, principal); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	original, err := app.CreateWorkflowBatch(context.Background(), workflow.ID, principal)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok || claimed.ID != original.ID {
		t.Fatalf("original claim = %#v, ok=%t, err=%v", claimed, ok, err)
	}
	app.executeWorkflowBatch(context.Background(), claimed)
	replay, err := app.ApplyWorkflowBatchAction(context.Background(), original.ID, WorkflowBatchActionPayload{Action: "replay"})
	if err != nil || !replay.Diagnostic || replay.OriginalBatchID != original.ID {
		t.Fatalf("diagnostic replay = %#v, err=%v", replay, err)
	}
	claimed, ok, err = app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok || claimed.ID != replay.ID {
		t.Fatalf("replay claim = %#v, ok=%t, err=%v", claimed, ok, err)
	}
	app.executeWorkflowBatch(context.Background(), claimed)
	if action.calls.Load() != 1 {
		t.Fatalf("side effect calls = %d", action.calls.Load())
	}
	var copied int64
	if err := database.QueryRow(`SELECT COUNT(*) FROM workflow_checkpoints WHERE batch_id = $1 AND node_instance_id = 'send' AND output_json = '{"value":"sent"}'::jsonb`, replay.ID).Scan(&copied); err != nil || copied != 1 {
		t.Fatalf("replay checkpoint count = %d, err=%v", copied, err)
	}
}

func TestWorkflowTriggerBackpressureAndRestartRecovery(t *testing.T) {
	app, database, ownerID := openWorkflowTestApp(t)
	principal := &Principal{User: &db.SystemUser{ID: ownerID}}
	eventWorkflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Stream events", TemplateKey: WorkflowTemplateEvent}, principal)
	if err != nil {
		t.Fatal(err)
	}
	eventGraph := strings.Replace(eventWorkflowGraph, `"example.event"`, `"test.stream"`, 1)
	if _, err := app.SaveWorkflowRevision(context.Background(), eventWorkflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: eventWorkflow.ActiveRevisionID, Graph: jsonMessage(eventGraph),
	}, principal); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), eventWorkflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	trigger := &workflowBackpressureTrigger{started: make(chan int32, 2), emitted: make(chan string, 4)}
	register := func(target *App) {
		if err := target.Plugins.RegisterPlugin(sdk.PluginDescriptor{
			ID: "official.stream-test", Name: "Stream test", Version: "1.0.0", Contributes: []string{"triggers"},
		}, func(registrar sdk.Registrar) error {
			return registrar.Trigger(sdk.NodeDescriptor{
				Type: "official.stream-test.source", Version: "1.0.0", Kind: sdk.NodeKindTrigger,
				ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`),
				UISchema:     json.RawMessage(`{}`), InputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`),
				OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`),
				Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
			}, trigger)
		}); err != nil {
			t.Fatal(err)
		}
	}
	register(app)
	workflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Stream"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	graph := `{"schemaVersion":1,"nodes":[
    {"nodeInstanceId":"source","nodeType":"official.stream-test.source","nodeVersion":"1.0.0","config":{},"position":{"x":80,"y":120}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":440,"y":120}}
  ],"edges":[
    {"edgeId":"source-end","sourceNodeInstanceId":"source","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]}`
	if _, err := app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: workflow.ActiveRevisionID, Graph: jsonMessage(graph),
	}, principal); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE workflow_runtimes SET backlog_limit = 1 WHERE workflow_id = $1`, workflow.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	if err := app.syncWorkflowTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case run := <-trigger.started:
		if run != 1 {
			t.Fatalf("first trigger run = %d", run)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("trigger did not start")
	}
	select {
	case emitted := <-trigger.emitted:
		if emitted != "1:stream-1" {
			t.Fatalf("first emitted event = %q", emitted)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first stream event was not emitted")
	}
	select {
	case emitted := <-trigger.emitted:
		t.Fatalf("backpressure allowed event %q", emitted)
	case <-time.After(350 * time.Millisecond):
	}
	if _, err := database.Exec(`UPDATE execution_batches SET status = 'succeeded', completed_at = CURRENT_TIMESTAMP WHERE workflow_id = $1 AND status = 'queued'`, workflow.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case emitted := <-trigger.emitted:
		if emitted != "1:stream-2" {
			t.Fatalf("released event = %q", emitted)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("trigger did not resume after backlog release")
	}
	var streamDeliveries, eventDeliveries int64
	if err := database.QueryRow(`SELECT COUNT(*) FROM workflow_event_deliveries WHERE workflow_id = $1`, workflow.ID).Scan(&streamDeliveries); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM workflow_event_deliveries WHERE workflow_id = $1`, eventWorkflow.ID).Scan(&eventDeliveries); err != nil {
		t.Fatal(err)
	}
	if streamDeliveries != 2 || eventDeliveries != 2 {
		t.Fatalf("trigger deliveries stream=%d event=%d", streamDeliveries, eventDeliveries)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	restarted := workflowTestApp(t)
	restarted.DB, restarted.Cipher, restarted.ArtifactRoot = app.DB, app.Cipher, app.ArtifactRoot
	register(restarted)
	if err := restarted.syncWorkflowTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case run := <-trigger.started:
		if run != 2 {
			t.Fatalf("recovered trigger run = %d", run)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("restarted process did not recover trigger")
	}
	if _, err := restarted.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	trigger.fail.Store(true)
	if _, err := restarted.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	if err := restarted.syncWorkflowTriggers(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case run := <-trigger.started:
		if run != 3 {
			t.Fatalf("failing trigger run = %d", run)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("failing trigger did not start")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var status, health string
		err := database.QueryRow(`SELECT w.status, r.health_summary FROM workflows w JOIN workflow_runtimes r ON r.workflow_id = w.id WHERE w.id = $1`, workflow.ID).Scan(&status, &health)
		if err != nil {
			t.Fatal(err)
		}
		if status == WorkflowStatusAttention && health == "degraded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed trigger status=%q health=%q", status, health)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWorkflowEventStreamCapacity(t *testing.T) {
	if os.Getenv("COINSPHERE_P2_CAPACITY") != "1" {
		t.Skip("COINSPHERE_P2_CAPACITY is not enabled")
	}
	app, database, ownerID := openWorkflowTestApp(t)
	principal := &Principal{User: &db.SystemUser{ID: ownerID}}
	const workflowCount = 20
	for index := range workflowCount {
		workflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{
			Name: fmt.Sprintf("Capacity %02d", index), TemplateKey: WorkflowTemplateEvent,
		}, principal)
		if err != nil {
			t.Fatal(err)
		}
		graph := strings.Replace(eventWorkflowGraph, "example.event", fmt.Sprintf("capacity.event.%d", index), 1)
		if _, err := app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
			ExpectedActiveRevisionID: workflow.ActiveRevisionID, Graph: jsonMessage(graph),
		}, principal); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`UPDATE workflow_runtimes SET backlog_limit = 10000 WHERE workflow_id = $1`, workflow.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
			t.Fatal(err)
		}
	}

	engineCtx, cancelEngine := context.WithCancel(context.Background())
	engineDone := make(chan error, 1)
	go func() { engineDone <- app.RunBatchEngine(engineCtx) }()
	engineStopped := false
	t.Cleanup(func() {
		if engineStopped {
			return
		}
		cancelEngine()
		<-engineDone
		_ = app.WaitForWorkflowBatches(context.Background())
	})
	sequence := atomic.Int64{}
	publish := func(count int, interval time.Duration) {
		t.Helper()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		sem := make(chan struct{}, 16)
		errors := make(chan error, count)
		var wait sync.WaitGroup
		for range count {
			<-ticker.C
			id := sequence.Add(1)
			sem <- struct{}{}
			wait.Add(1)
			go func() {
				defer wait.Done()
				defer func() { <-sem }()
				event := cloudevents.NewEvent()
				event.SetID(fmt.Sprintf("capacity-%d", id))
				event.SetSource("urn:coinsphere:capacity")
				event.SetType(fmt.Sprintf("capacity.event.%d", id%workflowCount))
				event.SetTime(time.Now().UTC())
				event.SetExtension("partitionkey", fmt.Sprintf("partition-%03d", id%100))
				publishErr := event.SetData(cloudevents.ApplicationJSON, map[string]any{"sequence": id})
				if publishErr == nil {
					_, publishErr = app.PublishWorkflowEvent(context.Background(), event)
				}
				errors <- publishErr
			}()
		}
		wait.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	publish(100, 100*time.Millisecond)
	publish(3000, 20*time.Millisecond)
	const expected = int64(3100)
	deadline := time.Now().Add(5 * time.Minute)
	for {
		var pending int64
		if err := database.QueryRow(`SELECT COUNT(*) FROM execution_batches WHERE status IN ('queued','running','waiting','retrying')`).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("capacity backlog did not drain: %d batches remain", pending)
		}
		time.Sleep(250 * time.Millisecond)
	}
	cancelEngine()
	if err := <-engineDone; err != nil {
		t.Fatal(err)
	}
	engineStopped = true
	if err := app.WaitForWorkflowBatches(context.Background()); err != nil {
		t.Fatal(err)
	}

	for name, query := range map[string]string{
		"events":     `SELECT COUNT(*) FROM workflow_event_records`,
		"deliveries": `SELECT COUNT(*) FROM workflow_event_deliveries`,
		"batches":    `SELECT COUNT(*) FROM execution_batches WHERE status = 'succeeded'`,
	} {
		var count int64
		if err := database.QueryRow(query).Scan(&count); err != nil || count != expected {
			t.Fatalf("capacity %s = %d, want %d, err=%v", name, count, expected, err)
		}
	}
	var partitions, checkpoints, operationKeys int64
	if err := database.QueryRow(`SELECT COUNT(DISTINCT partition_key) FROM workflow_event_records`).Scan(&partitions); err != nil || partitions != 100 {
		t.Fatalf("capacity partitions = %d, err=%v", partitions, err)
	}
	if err := database.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT operation_key) FROM workflow_checkpoints`).Scan(&checkpoints, &operationKeys); err != nil || checkpoints != expected*2 || operationKeys != checkpoints {
		t.Fatalf("capacity checkpoints=%d operationKeys=%d err=%v", checkpoints, operationKeys, err)
	}
}

func TestWorkflowBatchRetryKeepsOperationKeyAndStateAtomic(t *testing.T) {
	app, database, ownerID := openWorkflowTestApp(t)
	principal := &Principal{User: &db.SystemUser{ID: ownerID}}
	workflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Retry"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	retryGraph := strings.Replace(testPluginWorkflowGraph, `"kind":"cel","expression":"event.type + ':'"`, `"kind":"literal","value":"retry"`, 1)
	secret := "retry-secret"
	revision, err := app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: workflow.ActiveRevisionID, Graph: jsonMessage(retryGraph),
		SecretChanges: []WorkflowSecretChange{{NodeInstanceID: "transform", Field: "token", Value: &secret}},
	}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	batch, err := app.CreateWorkflowBatch(context.Background(), workflow.ID, principal)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("first claim ok=%t err=%v", ok, err)
	}
	app.executeWorkflowBatch(context.Background(), claimed)
	first, _ := app.GetWorkflowBatch(context.Background(), batch.ID)
	if first.Status != BatchStatusRetrying {
		t.Fatalf("first status = %q", first.Status)
	}
	if _, err := database.Exec(`UPDATE execution_batches SET not_before = CURRENT_TIMESTAMP WHERE id = $1`, batch.ID); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err = app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("retry claim ok=%t err=%v", ok, err)
	}
	app.executeWorkflowBatch(context.Background(), claimed)
	finished, _ := app.GetWorkflowBatch(context.Background(), batch.ID)
	if finished.Status != BatchStatusSucceeded {
		t.Fatalf("retry status = %q", finished.Status)
	}
	var triggerRuns, transformRuns, operationKeys int64
	if err := database.QueryRow(`SELECT COUNT(*) FROM workflow_node_runs WHERE batch_id = $1 AND node_instance_id = 'manual'`, batch.ID).Scan(&triggerRuns); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT operation_key) FROM workflow_node_runs WHERE batch_id = $1 AND node_instance_id = 'transform'`, batch.ID).Scan(&transformRuns, &operationKeys); err != nil {
		t.Fatal(err)
	}
	if triggerRuns != 1 || transformRuns != 2 || operationKeys != 1 {
		t.Fatalf("runs trigger=%d transform=%d operationKeys=%d", triggerRuns, transformRuns, operationKeys)
	}
	var checkpointRevision int64
	if err := database.QueryRow(`SELECT revision_id FROM workflow_checkpoints WHERE batch_id = $1 AND node_instance_id = 'transform'`, batch.ID).Scan(&checkpointRevision); err != nil || checkpointRevision != revision.ID {
		t.Fatalf("checkpoint revision = %d, err = %v", checkpointRevision, err)
	}

	invalidGraph := strings.Replace(testPluginWorkflowGraph, `"kind":"cel","expression":"event.type + ':'"`, `"kind":"literal","value":"invalid"`, 1)
	invalidWorkflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Atomic state"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.SaveWorkflowRevision(context.Background(), invalidWorkflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: invalidWorkflow.ActiveRevisionID, Graph: jsonMessage(invalidGraph),
		SecretChanges: []WorkflowSecretChange{{NodeInstanceID: "transform", Field: "token", Value: &secret}},
	}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), invalidWorkflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	invalidBatch, err := app.CreateWorkflowBatch(context.Background(), invalidWorkflow.ID, principal)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err = app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok || claimed.ID != invalidBatch.ID {
		t.Fatalf("invalid claim = %#v, ok=%t err=%v", claimed, ok, err)
	}
	app.executeWorkflowBatch(context.Background(), claimed)
	var states int64
	if err := database.QueryRow(`SELECT COUNT(*) FROM workflow_node_states WHERE workflow_id = $1`, invalidWorkflow.ID).Scan(&states); err != nil || states != 0 {
		t.Fatalf("uncheckpointed states = %d, err = %v", states, err)
	}
}

func TestWorkflowBatchCancellationAndScheduleDeduplication(t *testing.T) {
	app, database, ownerID := openWorkflowTestApp(t)
	principal := &Principal{User: &db.SystemUser{ID: ownerID}}
	waitGraph := strings.Replace(testPluginWorkflowGraph, `"kind":"cel","expression":"event.type + ':'"`, `"kind":"literal","value":"wait"`, 1)
	workflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Cancel"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	secret := "cancel-secret"
	_, err = app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: workflow.ActiveRevisionID, Graph: jsonMessage(waitGraph),
		SecretChanges: []WorkflowSecretChange{{NodeInstanceID: "transform", Field: "token", Value: &secret}},
	}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	batch, err := app.CreateWorkflowBatch(context.Background(), workflow.ID, principal)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("claim ok=%t err=%v", ok, err)
	}
	done := make(chan struct{})
	go func() {
		app.executeWorkflowBatch(context.Background(), claimed)
		close(done)
	}()
	waitForWorkflowNodeRun(t, database, batch.ID, "transform")
	if _, err := app.ApplyWorkflowBatchAction(context.Background(), batch.ID, WorkflowBatchActionPayload{Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("workflow batch did not respond to cancellation")
	}
	cancelled, _ := app.GetWorkflowBatch(context.Background(), batch.ID)
	if cancelled.Status != BatchStatusCancelled {
		t.Fatalf("cancelled status = %q", cancelled.Status)
	}

	scheduled, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Schedule", TemplateKey: WorkflowTemplateSchedule}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), scheduled.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	due := time.Now().UTC().Add(-time.Second)
	if _, err := database.Exec(`UPDATE workflow_runtimes SET next_scheduled_at = $1 WHERE workflow_id = $2`, due, scheduled.ID); err != nil {
		t.Fatal(err)
	}
	if err := app.enqueueScheduledBatches(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := app.enqueueScheduledBatches(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var scheduledCount int64
	if err := database.QueryRow(`SELECT COUNT(*) FROM execution_batches WHERE workflow_id = $1 AND trigger_type = 'schedule'`, scheduled.ID).Scan(&scheduledCount); err != nil || scheduledCount != 1 {
		t.Fatalf("scheduled batches = %d, err = %v", scheduledCount, err)
	}
}

func TestWorkflowBatchProcessInterruptionRequeuesCurrentNode(t *testing.T) {
	app, database, ownerID := openWorkflowTestApp(t)
	principal := &Principal{User: &db.SystemUser{ID: ownerID}}
	graph := strings.Replace(testPluginWorkflowGraph, `"kind":"cel","expression":"event.type + ':'"`, `"kind":"literal","value":"interrupt"`, 1)
	workflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Interrupted"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	secret := "interrupt-secret"
	_, err = app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: workflow.ActiveRevisionID, Graph: jsonMessage(graph),
		SecretChanges: []WorkflowSecretChange{{NodeInstanceID: "transform", Field: "token", Value: &secret}},
	}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	batch, err := app.CreateWorkflowBatch(context.Background(), workflow.ID, principal)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("claim ok=%t err=%v", ok, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		app.executeWorkflowBatch(ctx, claimed)
		close(done)
	}()
	waitForWorkflowNodeRun(t, database, batch.ID, "transform")
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("workflow batch did not stop after process interruption")
	}
	interrupted, _ := app.GetWorkflowBatch(context.Background(), batch.ID)
	if interrupted.Status != BatchStatusQueued || interrupted.CompletedAt != "" {
		t.Fatalf("interrupted batch = %#v", interrupted)
	}
	claimed, ok, err = app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("resume claim ok=%t err=%v", ok, err)
	}
	app.executeWorkflowBatch(context.Background(), claimed)
	resumed, _ := app.GetWorkflowBatch(context.Background(), batch.ID)
	if resumed.Status != BatchStatusSucceeded {
		t.Fatalf("resumed status = %q", resumed.Status)
	}
}

func TestWorkflowHistoryArtifactsAndRetention(t *testing.T) {
	app, database, ownerID := openWorkflowTestApp(t)
	principal := &Principal{User: &db.SystemUser{ID: ownerID}}
	graph := strings.Replace(testPluginWorkflowGraph, `"kind":"cel","expression":"event.type + ':'"`, `"kind":"literal","value":"artifact"`, 1)
	workflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "History"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	secret := "artifact-secret"
	_, err = app.SaveWorkflowRevision(context.Background(), workflow.ID, WorkflowRevisionSavePayload{
		ExpectedActiveRevisionID: workflow.ActiveRevisionID, Graph: jsonMessage(graph),
		SecretChanges: []WorkflowSecretChange{{NodeInstanceID: "transform", Field: "token", Value: &secret}},
	}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	batch, err := app.CreateWorkflowBatch(context.Background(), workflow.ID, principal)
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := app.claimWorkflowBatch(context.Background(), time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("claim ok=%t err=%v", ok, err)
	}
	app.executeWorkflowBatch(context.Background(), claimed)
	detail, err := app.GetWorkflowBatchDetail(context.Background(), batch.ID)
	if err != nil || detail.Status != BatchStatusSucceeded || len(detail.NodeRuns) != 3 || len(detail.Activities) != 9 || len(detail.Artifacts) != 1 {
		t.Fatalf("batch detail = %#v, err = %v", detail, err)
	}
	artifact := detail.Artifacts[0]
	manifest, err := app.GetWorkflowArtifactManifest(context.Background(), artifact.SHA256, true)
	if err != nil || !manifest.Verified || manifest.SizeBytes != int64(len(`{"rows":[1,2,3]}`)) {
		t.Fatalf("artifact manifest = %#v, err = %v", manifest, err)
	}
	reader, _, err := app.OpenWorkflowArtifact(context.Background(), artifact.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || string(content) != `{"rows":[1,2,3]}` {
		t.Fatalf("artifact content = %q, read=%v close=%v", content, readErr, closeErr)
	}
	items, next, err := app.ListWorkflowActivities(context.Background(), workflow.ID, 0, 200)
	if err != nil || len(items) != len(detail.Activities) || next <= 0 {
		t.Fatalf("activity items=%d next=%d err=%v", len(items), next, err)
	}
	old := time.Now().UTC().Add(-48 * time.Hour)
	if _, err := database.Exec(`UPDATE workflows SET retention_days = 1 WHERE id = $1`, workflow.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE execution_batches SET completed_at = $1 WHERE id = $2`, old, batch.ID); err != nil {
		t.Fatal(err)
	}
	if err := app.cleanupWorkflowHistory(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GetWorkflowBatch(context.Background(), batch.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retained batch err = %v", err)
	}
	if _, err := app.GetWorkflowArtifactManifest(context.Background(), artifact.SHA256, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retained artifact err = %v", err)
	}
}

func waitForWorkflowNodeRun(t *testing.T, database *sql.DB, batchID int64, nodeID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var count int64
		if err := database.QueryRow(`SELECT COUNT(*) FROM workflow_node_runs WHERE batch_id = $1 AND node_instance_id = $2 AND status = 'running'`, batchID, nodeID).Scan(&count); err == nil && count == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("workflow node did not start")
}

func jsonMessage(value string) []byte { return []byte(value) }

func openWorkflowTestApp(t *testing.T) (*App, *sql.DB, int64) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("COINSPHERE_TEST_POSTGRES_DSN"))
	if dsn == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("COINSPHERE_TEST_POSTGRES_DSN is required in CI")
		}
		t.Skip("COINSPHERE_TEST_POSTGRES_DSN is not configured")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	lock, err := admin.Conn(context.Background())
	if err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	if _, err := lock.ExecContext(context.Background(), "SELECT pg_advisory_lock(671908427)"); err != nil {
		_ = lock.Close()
		_ = admin.Close()
		t.Fatal(err)
	}
	schema := fmt.Sprintf("workflow_service_test_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), workflowSchemaSequence.Add(1))
	if _, err := admin.Exec("CREATE SCHEMA " + pgx.Identifier{schema}.Sanitize()); err != nil {
		_, _ = lock.ExecContext(context.Background(), "SELECT pg_advisory_unlock(671908427)")
		_ = lock.Close()
		_ = admin.Close()
		t.Fatal(err)
	}
	testConfig := config.Copy()
	if testConfig.RuntimeParams == nil {
		testConfig.RuntimeParams = make(map[string]string)
	}
	testConfig.RuntimeParams["search_path"] = schema
	database := stdlib.OpenDB(*testConfig)
	t.Cleanup(func() {
		_ = database.Close()
		_, _ = admin.Exec("DROP SCHEMA IF EXISTS plugin_quant CASCADE")
		_, _ = admin.Exec("DROP SCHEMA IF EXISTS plugin_notification CASCADE")
		_, _ = admin.Exec("DROP SCHEMA " + pgx.Identifier{schema}.Sanitize() + " CASCADE")
		_, _ = lock.ExecContext(context.Background(), "SELECT pg_advisory_unlock(671908427)")
		_ = lock.Close()
		_ = admin.Close()
	})
	runner, err := migration.New(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	var ownerID int64
	if err := database.QueryRow(`INSERT INTO users (username) VALUES ('workflow-owner') RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	app := workflowTestApp(t)
	cipher, err := security.NewSecretCipher("workflow-test-secret-key")
	if err != nil {
		t.Fatal(err)
	}
	app.DB = gdb
	if err := official.RegisterQuant(app.Plugins, gdb); err != nil {
		t.Fatal(err)
	}
	if err := official.RegisterNotification(app.Plugins, gdb); err != nil {
		t.Fatal(err)
	}
	app.Cipher = cipher
	app.ArtifactRoot = t.TempDir()
	return app, database, ownerID
}
