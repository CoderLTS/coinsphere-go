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
	return &App{DB: gdb}, database, ownerID
}
