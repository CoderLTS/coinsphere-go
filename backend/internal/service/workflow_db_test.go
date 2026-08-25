package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/migration"
	"coinsphere/backend/internal/security"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var workflowSchemaSequence atomic.Uint64

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
	schema := fmt.Sprintf("workflow_service_test_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), workflowSchemaSequence.Add(1))
	if _, err := admin.Exec("CREATE SCHEMA " + pgx.Identifier{schema}.Sanitize()); err != nil {
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
		_, _ = admin.Exec("DROP SCHEMA " + pgx.Identifier{schema}.Sanitize() + " CASCADE")
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
	app.Cipher = cipher
	return app, database, ownerID
}
