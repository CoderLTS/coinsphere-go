package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/db"
)

func TestRuntimeCancellationFinalizesInFlightExecution(t *testing.T) {
	ctx := context.Background()
	gdb, err := db.Open(ctx, config.DatabaseConfig{
		Driver: "sqlite",
		Path:   filepath.Join(t.TempDir(), "runtime.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("get sql database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	graph := M{
		"nodes": []any{
			M{"id": "start", "type": "start.manual", "config": M{"entryKey": "manual"}},
			M{"id": "wait", "type": "delay.wait", "config": M{"durationMs": 60_000}},
			M{"id": "end", "type": "end", "config": M{}},
		},
		"edges": []any{
			M{"id": "start-wait", "source": "start", "target": "wait"},
			M{"id": "wait-end", "source": "wait", "target": "end"},
		},
	}
	definition := db.WorkflowDefinition{Code: "test-cancel", Version: 1, DisplayName: "取消测试", GraphJSON: dumpJSON(graph)}
	if err := gdb.Create(&definition).Error; err != nil {
		t.Fatalf("create definition: %v", err)
	}
	workerID := "test-worker"
	startedAt := time.Now()
	execution := db.WorkflowExecution{
		WorkflowDefinitionID: definition.ID,
		StartEntryKey:        "manual",
		StartNodeID:          "start",
		StartNodeType:        "start.manual",
		TriggerType:          "manual",
		Status:               "running",
		QueuedAt:             startedAt,
		ClaimedAt:            &startedAt,
		StartedAt:            &startedAt,
		LastHeartbeatAt:      &startedAt,
		WorkerID:             &workerID,
		AttemptCount:         1,
		MaxAttempts:          2,
	}
	if err := gdb.Create(&execution).Error; err != nil {
		t.Fatalf("create execution: %v", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	app := &App{
		DB:       gdb.WithContext(runCtx),
		database: gdb,
		Cfg:      &config.AppConfig{Workflow: config.WorkflowConfig{HeartbeatIntervalSeconds: 1, MaxOutputSnapshotBytes: 4096, GraphNodeConcurrency: 1, MaxAttempts: 2, RetryBackoffSeconds: []int{1}, DisableNodeInputSnapshot: true}},
		WorkerID: workerID,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.processExecution(runCtx, &execution)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		var count int64
		gdb.Model(&db.WorkflowExecutionNode{}).
			Where("workflow_execution_id = ? AND node_id = ? AND status = ?", execution.ID, "wait", "running").
			Count(&count)
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delay node did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("execution did not stop after context cancellation")
	}

	var finalized db.WorkflowExecution
	if err := gdb.First(&finalized, execution.ID).Error; err != nil {
		t.Fatalf("reload execution: %v", err)
	}
	if finalized.Status != "retry_waiting" || finalized.FailureCategory != failureInfraRetryable {
		t.Fatalf("unexpected cancellation result: status=%s category=%s", finalized.Status, finalized.FailureCategory)
	}
	var runningNodes int64
	gdb.Model(&db.WorkflowExecutionNode{}).
		Where("workflow_execution_id = ? AND status = ?", execution.ID, "running").
		Count(&runningNodes)
	if runningNodes != 0 {
		t.Fatalf("execution left %d running node logs", runningNodes)
	}
}

func TestClaimHandoffSurvivesCancellationAfterUpdate(t *testing.T) {
	gdb, err := db.Open(context.Background(), config.DatabaseConfig{
		Driver: "sqlite",
		Path:   filepath.Join(t.TempDir(), "claim.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("get sql database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	workerID := "claim-worker"
	app := &App{
		DB:          gdb.WithContext(ctx),
		database:    gdb,
		Cfg:         &config.AppConfig{Workflow: config.WorkflowConfig{SemaphoreLimitPerKey: 1, MaxAttempts: 1}},
		WorkerID:    workerID,
		runningKeys: map[string]int{},
	}
	definition := db.WorkflowDefinition{Code: "claim-cancel", Version: 1}
	if err := gdb.Create(&definition).Error; err != nil {
		t.Fatalf("create definition: %v", err)
	}
	execution := db.WorkflowExecution{
		WorkflowDefinitionID: definition.ID,
		Status:               "queued",
		QueuedAt:             time.Now(),
		ConcurrencyKey:       "claim-key",
		MaxAttempts:          1,
	}
	if err := gdb.Create(&execution).Error; err != nil {
		t.Fatalf("create execution: %v", err)
	}
	callbackName := "test:cancel-after-claim"
	if err := gdb.Callback().Update().After("gorm:update").Register(callbackName, func(*gorm.DB) { cancel() }); err != nil {
		t.Fatalf("register update callback: %v", err)
	}
	t.Cleanup(func() { _ = gdb.Callback().Update().Remove(callbackName) })

	claimed := app.claimNextExecution(ctx)
	if claimed == nil {
		t.Fatal("claim was lost after the committed update")
	}
	if _, err := app.getExecutionByID(execution.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("getExecutionByID error = %v, want context canceled", err)
	}
	app.processExecution(ctx, claimed)

	var finalized db.WorkflowExecution
	if err := gdb.First(&finalized, execution.ID).Error; err != nil {
		t.Fatalf("reload execution: %v", err)
	}
	if finalized.Status == "running" {
		t.Fatal("claimed execution was left running after cancellation")
	}
}

func TestStopRuntimeHonorsDeadline(t *testing.T) {
	runtimeCtx, runtimeCancel := context.WithCancel(context.Background())
	app := &App{runtimeCtx: runtimeCtx, runtimeCancel: runtimeCancel}
	release := make(chan struct{})
	app.wg.Add(1)
	go func() {
		defer app.wg.Done()
		<-release
	}()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := app.StopRuntime(shutdownCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StopRuntime error = %v, want context deadline exceeded", err)
	}
	close(release)
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
	defer cleanupCancel()
	if err := app.StopRuntime(cleanupCtx); err != nil {
		t.Fatalf("cleanup runtime: %v", err)
	}
}

func TestHubRejectsConnectionsAfterClose(t *testing.T) {
	hub := NewHub()
	hub.CloseAll()
	if hub.Connect(1, nil) {
		t.Fatal("closed hub accepted a late WebSocket connection")
	}
	if hub.IsOnline(1) {
		t.Fatal("closed hub reported the rejected connection as online")
	}
}
