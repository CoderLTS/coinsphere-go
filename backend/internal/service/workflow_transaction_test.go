package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/migration"
	"coinsphere/backend/internal/perm"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const workflowContractPostgresDSNEnv = "COINSPHERE_TEST_POSTGRES_DSN"

var workflowContractSchemaSequence atomic.Uint64

type workflowContractDatabase struct {
	primary  *gorm.DB
	openPeer func(t *testing.T) *gorm.DB
}

func TestWorkflowTransactionContractPostgres(t *testing.T) {
	runWorkflowTransactionContract(t)
}

// runWorkflowTransactionContract 通过真实 PostgreSQL 并发与失败注入固定跨实例锁、事务回滚和一致性快照语义。
func runWorkflowTransactionContract(t *testing.T) {
	t.Helper()

	t.Run("owner scope hides every workflow resource", func(t *testing.T) {
		database := openPostgresWorkflowContractDatabase(t)
		app := newWorkflowContractApp(database.primary)
		ownerOne, err := app.CreateWorkflowDefinition(workflowContractPayload("同名租户", "owner1"), 1)
		if err != nil {
			t.Fatalf("create owner one workflow: %v", err)
		}
		ownerTwo, err := app.CreateWorkflowDefinition(workflowContractPayload("同名租户", "owner2"), 2)
		if err != nil {
			t.Fatalf("create owner two workflow: %v", err)
		}
		ownerOneID := workflowContractResultID(t, ownerOne)
		ownerTwoID := workflowContractResultID(t, ownerTwo)
		first := readWorkflowContractDefinition(t, database.primary, ownerOneID)
		second := readWorkflowContractDefinition(t, database.primary, ownerTwoID)
		if first.Code != second.Code {
			t.Fatalf("owner-scoped workflow codes = %q and %q, want equal", first.Code, second.Code)
		}
		if _, err := app.GetWorkflowDefinition(ownerTwoID, 1); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-owner definition read error = %v", err)
		}
		if _, err := app.UpdateWorkflowDefinition(ownerTwoID, workflowContractPayload("越权", "owner2"), 1); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-owner definition update error = %v", err)
		}
		if err := app.DeleteWorkflowDefinition(ownerTwoID, 1); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-owner definition delete error = %v", err)
		}
		if _, err := app.ActivateDefinition(ownerTwoID, 2); err != nil {
			t.Fatalf("activate owner two workflow: %v", err)
		}
		if _, err := app.GetRuntimeByDefinition(ownerTwoID, 1); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-owner runtime read error = %v", err)
		}
		execution := db.WorkflowExecution{
			OwnerUserID: 2, WorkflowDefinitionID: ownerTwoID, StartEntryKey: "manual.owner2",
			StartNodeID: "manual-owner2", StartNodeType: "start.manual", TriggerType: "manual",
			ConcurrencyKey: "2:" + second.Code + ":manual.owner2", Status: "failed", QueuedAt: time.Now(), MaxAttempts: 1,
		}
		if err := database.primary.Create(&execution).Error; err != nil {
			t.Fatalf("create owner two execution: %v", err)
		}
		if _, err := app.GetExecutionDetail(execution.ID, 1); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-owner execution read error = %v", err)
		}
		listed, err := app.ListExecutions(WorkflowExecutionQuery{OwnerUserID: 1, Page: CursorPage{Limit: 20}})
		if err != nil {
			t.Fatalf("list owner one executions: %v", err)
		}
		if listed["total"] != int64(0) {
			t.Fatalf("owner one execution total = %#v, want 0", listed["total"])
		}

		ownerTwoOnly, err := app.CreateWorkflowDefinition(workflowContractPayload("租户二专属", "owner2-only"), 2)
		if err != nil {
			t.Fatalf("create owner two resource workflow: %v", err)
		}
		ownerTwoOnlyDefinition := readWorkflowContractDefinition(t, database.primary, workflowContractResultID(t, ownerTwoOnly))
		resourceGraph := M{"nodes": []any{M{
			"id": "call-owner2", "type": "workflow.call",
			"config": M{"workflowCode": ownerTwoOnlyDefinition.Code, "entryKey": "manual.owner2-only"},
		}}}
		if err := app.assertWorkflowResourcesOwned(resourceGraph, 1); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-owner workflow node resource error = %v, want not found", err)
		}
		if err := app.assertWorkflowResourcesOwned(resourceGraph, 2); err != nil {
			t.Fatalf("owner workflow node resource check: %v", err)
		}
	})

	t.Run("global workflow resources are recognized", func(t *testing.T) {
		database := openPostgresWorkflowContractDatabase(t)
		app := newWorkflowContractApp(database.primary)
		news := db.BlockbeatsNews{Title: "workflow resource"}
		agent := db.AssistantAgent{Code: "workflow-resource", DisplayName: "Workflow Resource"}
		role := db.SystemRole{Code: "workflow-resource", DisplayName: "Workflow Resource", IsEnabled: true}
		for _, record := range []any{&news, &agent, &role} {
			if err := database.primary.Create(record).Error; err != nil {
				t.Fatalf("create global workflow resource: %v", err)
			}
		}

		graph := M{"nodes": []any{
			M{"id": "news", "type": "news.manage", "config": M{"action": "update", "newsId": news.ID}},
			M{"id": "assistant", "type": "config.assistant", "config": M{"action": "update", "agentId": agent.ID}},
			M{"id": "user", "type": "admin.user", "config": M{"action": "update", "userId": int64(1)}},
			M{"id": "role", "type": "admin.role", "config": M{"action": "update", "roleId": role.ID}},
			M{"id": "permissions", "type": "admin.permissions", "config": M{"roleId": role.ID}},
		}}
		if err := app.assertWorkflowResourcesOwned(graph, 1); err != nil {
			t.Fatalf("global workflow resource check: %v", err)
		}
		graph["nodes"].([]any)[4].(M)["config"].(M)["roleId"] = int64(999999)
		if err := app.assertWorkflowResourcesOwned(graph, 1); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing global workflow resource error = %v, want not found", err)
		}
	})

	t.Run("concurrent action decisions have one winner", func(t *testing.T) {
		database := openPostgresWorkflowContractDatabase(t)
		peer := database.openPeer(t)
		apps := []*App{newWorkflowContractApp(database.primary), newWorkflowContractApp(peer)}
		definitionID := createWorkflowContractDefinition(t, apps[0], workflowContractPayload("人工决策", "action"))
		now := time.Now().UTC()
		execution := db.WorkflowExecution{
			OwnerUserID: 1, WorkflowDefinitionID: definitionID, StartEntryKey: "manual.action",
			StartNodeID: "manual-action", StartNodeType: "start.manual", TriggerType: "manual",
			ConcurrencyKey: "1:action", Status: "waiting_action", QueuedAt: now, StartedAt: &now,
			MaxAttempts: 1, InputSnapshotJSON: "{}", ContextSnapshotJSON: "{}", ResultSnapshotJSON: "{}",
		}
		if err := database.primary.Create(&execution).Error; err != nil {
			t.Fatalf("create waiting execution: %v", err)
		}
		waitID, err := uuid.NewV7()
		if err != nil {
			t.Fatalf("create workflow action id: %v", err)
		}
		wait := db.WorkflowExecutionWait{
			ID: waitID, OwnerUserID: 1, WorkflowExecutionID: execution.ID,
			Kind: "human_action", ActionType: "strategy.archive", TargetType: "strategy", TargetID: "unused",
			Status: "pending", RequestJSON: "{}", ResultJSON: "{}", CreatedAt: now, UpdatedAt: now,
		}
		if err := database.primary.Create(&wait).Error; err != nil {
			t.Fatalf("create workflow action: %v", err)
		}
		principal := &Principal{User: &db.SystemUser{ID: 1, IsActive: true}, PermissionCodes: map[string]bool{perm.TradingOverviewView: true}}
		start := make(chan struct{})
		results := make(chan error, len(apps))
		for index := range apps {
			go func(index int) {
				<-start
				_, err := apps[index].DecideWorkflowAction(
					context.Background(), waitID.String(), principal,
					WorkflowActionDecision{Decision: "rejected"}, fmt.Sprintf("workflow-reject-%d", index), "",
				)
				results <- err
			}(index)
		}
		close(start)
		succeeded, conflicted := 0, 0
		for range apps {
			switch err := <-results; {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrWorkflowActionConflict):
				conflicted++
			default:
				t.Fatalf("concurrent workflow action decision: %v", err)
			}
		}
		if succeeded != 1 || conflicted != 1 {
			t.Fatalf("decision outcomes succeeded=%d conflicted=%d, want 1/1", succeeded, conflicted)
		}
		if err := database.primary.First(&wait, "id = ?", waitID).Error; err != nil {
			t.Fatalf("reload workflow action: %v", err)
		}
		if wait.Status != "rejected" {
			t.Fatalf("workflow action status = %q, want rejected", wait.Status)
		}
	})

	t.Run("concurrent same-name creation allocates distinct families", func(t *testing.T) {
		database := openPostgresWorkflowContractDatabase(t)
		peer := database.openPeer(t)
		apps := []*App{
			newWorkflowContractApp(database.primary),
			newWorkflowContractApp(peer),
		}

		start := make(chan struct{})
		type createResult struct {
			id  int64
			err error
		}
		results := make(chan createResult, len(apps))
		for index := range apps {
			go func(index int) {
				<-start
				result, err := apps[index].CreateWorkflowDefinition(workflowContractPayload("同名并发", fmt.Sprintf("create%d", index)), 1)
				if err != nil {
					results <- createResult{err: err}
					return
				}
				id, _ := result["id"].(int64)
				results <- createResult{id: id}
			}(index)
		}
		close(start)

		codes := map[string]struct{}{}
		for range apps {
			result := <-results
			if result.err != nil {
				t.Fatalf("concurrent same-name workflow creation: %v", result.err)
			}
			definition := readWorkflowContractDefinition(t, database.primary, result.id)
			if definition.Version != 1 {
				t.Fatalf("created workflow version = %d, want 1", definition.Version)
			}
			codes[definition.Code] = struct{}{}
		}
		if len(codes) != len(apps) {
			t.Fatalf("concurrent same-name creation produced %d codes, want %d", len(codes), len(apps))
		}
	})

	t.Run("concurrent versions are continuous", func(t *testing.T) {
		database := openPostgresWorkflowContractDatabase(t)
		peer := database.openPeer(t)
		primaryApp := newWorkflowContractApp(database.primary)
		peerApp := newWorkflowContractApp(peer)
		baseID := createWorkflowContractDefinition(t, primaryApp, workflowContractPayload("并发版本", "version"))
		base := readWorkflowContractDefinition(t, database.primary, baseID)

		const updateCount = 6
		start := make(chan struct{})
		results := make(chan error, updateCount)
		apps := []*App{primaryApp, peerApp}
		for index := range updateCount {
			go func(index int) {
				<-start
				_, err := apps[index%len(apps)].UpdateWorkflowDefinition(
					baseID,
					workflowContractPayload(fmt.Sprintf("并发版本-%d", index), "version"),
					1,
				)
				results <- err
			}(index)
		}
		close(start)
		for range updateCount {
			if err := <-results; err != nil {
				t.Fatalf("concurrent workflow version creation: %v", err)
			}
		}

		var definitions []db.WorkflowDefinition
		if err := database.primary.Where("code = ?", base.Code).Order("version ASC").Find(&definitions).Error; err != nil {
			t.Fatalf("read concurrent workflow versions: %v", err)
		}
		if len(definitions) != updateCount+1 {
			t.Fatalf("workflow version count = %d, want %d", len(definitions), updateCount+1)
		}
		for index, definition := range definitions {
			if definition.Version != index+1 {
				t.Fatalf("workflow version[%d] = %d, want %d", index, definition.Version, index+1)
			}
		}
	})

	t.Run("version allocation uses maximum after a gap", func(t *testing.T) {
		database := openPostgresWorkflowContractDatabase(t)
		app := newWorkflowContractApp(database.primary)
		baseID := createWorkflowContractDefinition(t, app, workflowContractPayload("版本缺口", "gap1"))
		secondID := createWorkflowContractVersion(t, app, baseID, workflowContractPayload("版本缺口-2", "gap2"))
		thirdID := createWorkflowContractVersion(t, app, secondID, workflowContractPayload("版本缺口-3", "gap3"))
		if err := app.DeleteWorkflowDefinition(secondID, 1); err != nil {
			t.Fatalf("delete middle workflow version: %v", err)
		}
		createdID := createWorkflowContractVersion(t, app, thirdID, workflowContractPayload("版本缺口-4", "gap4"))
		created := readWorkflowContractDefinition(t, database.primary, createdID)
		if created.Version != 4 {
			t.Fatalf("workflow version after gap = %d, want 4", created.Version)
		}
	})

	t.Run("concurrent activation leaves one complete runtime", func(t *testing.T) {
		database := openPostgresWorkflowContractDatabase(t)
		peer := database.openPeer(t)
		primaryApp := newWorkflowContractApp(database.primary)
		peerApp := newWorkflowContractApp(peer)
		firstID := createWorkflowContractDefinition(t, primaryApp, workflowContractPayload("并发激活", "first"))
		secondID := createWorkflowContractVersion(t, primaryApp, firstID, workflowContractPayload("并发激活-2", "second"))

		start := make(chan struct{})
		results := make(chan error, 2)
		activate := func(app *App, definitionID, operatorID int64) {
			<-start
			_, err := app.ActivateDefinition(definitionID, operatorID)
			results <- err
		}
		go activate(primaryApp, firstID, 1)
		go activate(peerApp, secondID, 1)
		close(start)
		for range 2 {
			if err := <-results; err != nil {
				t.Fatalf("concurrent workflow activation: %v", err)
			}
		}

		snapshot := readWorkflowRuntimeSnapshot(t, database.primary, "并发激活")
		assertWorkflowRuntimeMatchesActiveDefinition(t, snapshot, map[int64][]string{
			firstID:  workflowContractEntryKeys("first"),
			secondID: workflowContractEntryKeys("second"),
		})
	})

	t.Run("activation failure rolls back all runtime state", func(t *testing.T) {
		database := openPostgresWorkflowContractDatabase(t)
		app := newWorkflowContractApp(database.primary)
		firstID := createWorkflowContractDefinition(t, app, workflowContractPayload("激活回滚", "stable"))
		secondID := createWorkflowContractVersion(t, app, firstID, workflowContractPayload("激活回滚-2", "stable"))
		if _, err := app.ActivateDefinition(firstID, 1); err != nil {
			t.Fatalf("activate rollback fixture: %v", err)
		}
		if _, err := app.SetEntryEnabled(firstID, 1, "manual.stable", false); err != nil {
			t.Fatalf("disable rollback fixture entry: %v", err)
		}
		if _, err := app.RotateWebhookSecret(firstID, 1, "webhook.stable"); err != nil {
			t.Fatalf("rotate rollback fixture secret: %v", err)
		}
		before := readWorkflowRuntimeSnapshot(t, database.primary, "激活回滚")
		installWorkflowEntryInsertFailureTrigger(t, database.primary, secondID, "webhook.stable")

		if _, err := app.ActivateDefinition(secondID, 1); err == nil {
			t.Fatal("activation unexpectedly succeeded after entry failure injection")
		}
		after := readWorkflowRuntimeSnapshot(t, database.primary, "激活回滚")
		assertWorkflowRuntimeSnapshotEqual(t, after, before)
		var secondEntries int64
		if err := database.primary.Model(&db.WorkflowRuntimeEntry{}).
			Where("workflow_definition_id = ?", secondID).Count(&secondEntries).Error; err != nil {
			t.Fatalf("count failed activation entries: %v", err)
		}
		if secondEntries != 0 {
			t.Fatalf("failed activation left %d entries for definition %d", secondEntries, secondID)
		}
	})

	t.Run("uncommitted activation is never partially visible", func(t *testing.T) {
		database := openPostgresWorkflowContractDatabase(t)
		peer := database.openPeer(t)
		writer := newWorkflowContractApp(database.primary)
		firstID := createWorkflowContractDefinition(t, writer, workflowContractPayload("中间状态", "old"))
		secondID := createWorkflowContractVersion(t, writer, firstID, workflowContractPayload("中间状态-2", "new"))
		if _, err := writer.ActivateDefinition(firstID, 1); err != nil {
			t.Fatalf("activate intermediate-state fixture: %v", err)
		}
		before := readWorkflowRuntimeSnapshot(t, peer, "中间状态")

		paused := make(chan struct{})
		resume := make(chan struct{})
		var pauseOnce sync.Once
		var resumeOnce sync.Once
		callbackName := "workflow-contract:pause-first-entry"
		if err := database.primary.Callback().Create().After("gorm:create").Register(callbackName, func(tx *gorm.DB) {
			entry, ok := tx.Statement.Dest.(*db.WorkflowRuntimeEntry)
			if !ok || entry.WorkflowDefinitionID != secondID {
				return
			}
			pauseOnce.Do(func() {
				close(paused)
				<-resume
			})
		}); err != nil {
			t.Fatalf("register activation pause callback: %v", err)
		}
		t.Cleanup(func() { _ = database.primary.Callback().Create().Remove(callbackName) })
		release := func() { resumeOnce.Do(func() { close(resume) }) }
		t.Cleanup(release)

		activationDone := make(chan error, 1)
		go func() {
			_, err := writer.ActivateDefinition(secondID, 1)
			activationDone <- err
		}()
		waitWorkflowContractSignal(t, paused, "activation did not pause after the first uncommitted entry")

		// 写事务此时已经删除旧入口并插入一个新入口；另一个独立句柄仍只能看到完整旧快照。
		during := readWorkflowRuntimeSnapshot(t, peer, "中间状态")
		assertWorkflowRuntimeSnapshotEqual(t, during, before)
		release()
		if err := waitWorkflowContractResult(t, activationDone, "activation did not finish after resume"); err != nil {
			t.Fatalf("finish paused activation: %v", err)
		}
		after := readWorkflowRuntimeSnapshot(t, peer, "中间状态")
		assertWorkflowRuntimeMatchesActiveDefinition(t, after, map[int64][]string{
			secondID: workflowContractEntryKeys("new"),
		})
	})

	t.Run("runtime read never mixes activation snapshots", func(t *testing.T) {
		database := openPostgresWorkflowContractDatabase(t)
		peer := database.openPeer(t)
		reader := newWorkflowContractApp(database.primary)
		writer := newWorkflowContractApp(peer)
		firstID := createWorkflowContractDefinition(t, reader, workflowContractPayload("一致读取", "old"))
		secondID := createWorkflowContractVersion(t, reader, firstID, workflowContractPayload("一致读取-2", "new"))
		if _, err := writer.ActivateDefinition(firstID, 1); err != nil {
			t.Fatalf("activate runtime-read fixture: %v", err)
		}

		paused := make(chan struct{})
		resume := make(chan struct{})
		var pauseOnce sync.Once
		var resumeOnce sync.Once
		callbackName := "workflow-contract:pause-runtime-state-read"
		if err := database.primary.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
			if workflowContractStatementTable(tx) != "workflow_runtime_states" {
				return
			}
			pauseOnce.Do(func() {
				close(paused)
				<-resume
			})
		}); err != nil {
			t.Fatalf("register runtime read pause callback: %v", err)
		}
		t.Cleanup(func() { _ = database.primary.Callback().Query().Remove(callbackName) })
		release := func() { resumeOnce.Do(func() { close(resume) }) }
		t.Cleanup(release)

		type runtimeResult struct {
			data M
			err  error
		}
		readDone := make(chan runtimeResult, 1)
		go func() {
			data, err := reader.GetRuntimeByDefinition(firstID, 1)
			readDone <- runtimeResult{data: data, err: err}
		}()
		waitWorkflowContractSignal(t, paused, "runtime read did not pause after loading state")
		if _, err := writer.ActivateDefinition(secondID, 1); err != nil {
			t.Fatalf("activate new definition during runtime read: %v", err)
		}
		release()

		var result runtimeResult
		select {
		case result = <-readDone:
		case <-time.After(10 * time.Second):
			t.Fatal("runtime read did not finish after resume")
		}
		if result.err != nil {
			t.Fatalf("read runtime snapshot: %v", result.err)
		}
		assertSerializedRuntimeMatchesDefinition(t, result.data, firstID, workflowContractEntryKeys("old"))
	})

	t.Run("deactivation failure restores entries and active state", func(t *testing.T) {
		database := openPostgresWorkflowContractDatabase(t)
		app := newWorkflowContractApp(database.primary)
		definitionID := createWorkflowContractDefinition(t, app, workflowContractPayload("停用回滚", "deactivate"))
		if _, err := app.ActivateDefinition(definitionID, 1); err != nil {
			t.Fatalf("activate deactivation fixture: %v", err)
		}
		before := readWorkflowRuntimeSnapshot(t, database.primary, "停用回滚")
		installWorkflowStateDeactivateFailureTrigger(t, database.primary, before.State.ID)

		if _, err := app.DeactivateDefinition(definitionID, 1); err == nil {
			t.Fatal("deactivation unexpectedly succeeded after state failure injection")
		}
		after := readWorkflowRuntimeSnapshot(t, database.primary, "停用回滚")
		assertWorkflowRuntimeSnapshotEqual(t, after, before)
	})
}

func newWorkflowContractApp(database *gorm.DB) *App {
	return &App{
		DB:       database,
		database: database,
		Cfg: &config.AppConfig{
			Auth: config.AuthConfig{
				WebhookPepper: "workflow-transaction-contract-pepper",
			},
		},
	}
}

func workflowContractPayload(displayName, suffix string) WorkflowDefinitionUpsertPayload {
	return WorkflowDefinitionUpsertPayload{
		DisplayName: displayName,
		Graph: M{
			"schemaVersion": 2,
			"nodes": []any{
				M{"id": "manual-" + suffix, "type": "start.manual", "config": M{"entryKey": "manual." + suffix}},
				M{"id": "webhook-" + suffix, "type": "start.webhook", "config": M{"entryKey": "webhook." + suffix}},
				M{"id": "end-" + suffix, "type": "end", "config": M{}},
			},
			"edges": []any{
				M{"id": "manual-end-" + suffix, "kind": "flow", "source": "manual-" + suffix, "target": "end-" + suffix},
				M{"id": "webhook-end-" + suffix, "kind": "flow", "source": "webhook-" + suffix, "target": "end-" + suffix},
			},
		},
	}
}

func workflowContractEntryKeys(suffix string) []string {
	return []string{"manual." + suffix, "webhook." + suffix}
}

func createWorkflowContractDefinition(t *testing.T, app *App, payload WorkflowDefinitionUpsertPayload) int64 {
	t.Helper()
	result, err := app.CreateWorkflowDefinition(payload, 1)
	if err != nil {
		t.Fatalf("create workflow definition: %v", err)
	}
	return workflowContractResultID(t, result)
}

func createWorkflowContractVersion(t *testing.T, app *App, definitionID int64, payload WorkflowDefinitionUpsertPayload) int64 {
	t.Helper()
	result, err := app.UpdateWorkflowDefinition(definitionID, payload, 1)
	if err != nil {
		t.Fatalf("create workflow version: %v", err)
	}
	return workflowContractResultID(t, result)
}

func workflowContractResultID(t *testing.T, result M) int64 {
	t.Helper()
	id, ok := result["id"].(int64)
	if !ok || id == 0 {
		t.Fatalf("workflow result has invalid id: %#v", result["id"])
	}
	return id
}

func readWorkflowContractDefinition(t *testing.T, database *gorm.DB, definitionID int64) db.WorkflowDefinition {
	t.Helper()
	var definition db.WorkflowDefinition
	if err := database.First(&definition, definitionID).Error; err != nil {
		t.Fatalf("read workflow definition %d: %v", definitionID, err)
	}
	return definition
}

type workflowRuntimeStateSnapshot struct {
	ID                 int64
	WorkflowCode       string
	HasActive          bool
	ActiveDefinitionID int64
	ActivatedAt        string
	HasActivatedBy     bool
	ActivatedBy        int64
	UpdatedAt          string
}

type workflowRuntimeEntrySnapshot struct {
	ID                     int64
	WorkflowRuntimeStateID int64
	WorkflowDefinitionID   int64
	StartNodeID            string
	EntryKey               string
	StartType              string
	IsEnabled              bool
	RegistrationStatus     string
	ScheduleJobID          string
	NextRunAt              string
	LastTriggeredAt        string
	LastErrorMessage       string
	SecretHash             string
	SecretHint             string
	SecretRotatedAt        string
	CreatedAt              string
	UpdatedAt              string
}

type workflowRuntimeSnapshot struct {
	State   workflowRuntimeStateSnapshot
	Entries []workflowRuntimeEntrySnapshot
}

func readWorkflowRuntimeSnapshot(t *testing.T, database *gorm.DB, workflowCode string) workflowRuntimeSnapshot {
	t.Helper()
	var state db.WorkflowRuntimeState
	if err := database.Where("workflow_code = ?", workflowCode).First(&state).Error; err != nil {
		t.Fatalf("read workflow runtime state %q: %v", workflowCode, err)
	}
	stateSnapshot := workflowRuntimeStateSnapshot{
		ID:           state.ID,
		WorkflowCode: state.WorkflowCode,
		ActivatedAt:  workflowContractTimePointer(state.ActivatedAt),
		UpdatedAt:    workflowContractTime(state.UpdatedAt),
	}
	if state.ActiveWorkflowDefinitionID != nil {
		stateSnapshot.HasActive = true
		stateSnapshot.ActiveDefinitionID = *state.ActiveWorkflowDefinitionID
	}
	if state.ActivatedBy != nil {
		stateSnapshot.HasActivatedBy = true
		stateSnapshot.ActivatedBy = *state.ActivatedBy
	}

	var rows []db.WorkflowRuntimeEntry
	if err := database.Where("workflow_runtime_state_id = ?", state.ID).
		Order("entry_key ASC, id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("read workflow runtime entries for state %d: %v", state.ID, err)
	}
	entries := make([]workflowRuntimeEntrySnapshot, 0, len(rows))
	for _, entry := range rows {
		entries = append(entries, workflowRuntimeEntrySnapshot{
			ID:                     entry.ID,
			WorkflowRuntimeStateID: entry.WorkflowRuntimeStateID,
			WorkflowDefinitionID:   entry.WorkflowDefinitionID,
			StartNodeID:            entry.StartNodeID,
			EntryKey:               entry.EntryKey,
			StartType:              entry.StartType,
			IsEnabled:              entry.IsEnabled,
			RegistrationStatus:     entry.RegistrationStatus,
			ScheduleJobID:          entry.ScheduleJobID,
			NextRunAt:              workflowContractTimePointer(entry.NextRunAt),
			LastTriggeredAt:        workflowContractTimePointer(entry.LastTriggeredAt),
			LastErrorMessage:       entry.LastErrorMessage,
			SecretHash:             entry.SecretHash,
			SecretHint:             entry.SecretHint,
			SecretRotatedAt:        workflowContractTimePointer(entry.SecretRotatedAt),
			CreatedAt:              workflowContractTime(entry.CreatedAt),
			UpdatedAt:              workflowContractTime(entry.UpdatedAt),
		})
	}
	return workflowRuntimeSnapshot{State: stateSnapshot, Entries: entries}
}

func workflowContractTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func workflowContractTimePointer(value *time.Time) string {
	if value == nil {
		return ""
	}
	return workflowContractTime(*value)
}

func assertWorkflowRuntimeSnapshotEqual(t *testing.T, got, want workflowRuntimeSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workflow runtime snapshot changed after rollback\n got: %#v\nwant: %#v", got, want)
	}
}

func assertWorkflowRuntimeMatchesActiveDefinition(
	t *testing.T,
	snapshot workflowRuntimeSnapshot,
	wantEntryKeys map[int64][]string,
) {
	t.Helper()
	if !snapshot.State.HasActive {
		t.Fatal("workflow runtime has no active definition")
	}
	wantKeys, ok := wantEntryKeys[snapshot.State.ActiveDefinitionID]
	if !ok {
		t.Fatalf("unexpected active workflow definition %d", snapshot.State.ActiveDefinitionID)
	}
	if len(snapshot.Entries) != len(wantKeys) {
		t.Fatalf("runtime entry count = %d, want %d", len(snapshot.Entries), len(wantKeys))
	}
	for index, entry := range snapshot.Entries {
		if entry.WorkflowDefinitionID != snapshot.State.ActiveDefinitionID {
			t.Fatalf("entry %q belongs to definition %d, active definition is %d", entry.EntryKey, entry.WorkflowDefinitionID, snapshot.State.ActiveDefinitionID)
		}
		if entry.EntryKey != wantKeys[index] {
			t.Fatalf("runtime entry[%d] = %q, want %q", index, entry.EntryKey, wantKeys[index])
		}
	}
}

func assertSerializedRuntimeMatchesDefinition(t *testing.T, runtime M, definitionID int64, wantEntryKeys []string) {
	t.Helper()
	activeID, ok := runtime["activeDefinitionId"].(int64)
	if !ok || activeID != definitionID {
		t.Fatalf("runtime active definition = %#v, want %d", runtime["activeDefinitionId"], definitionID)
	}
	entries, ok := runtime["entries"].([]M)
	if !ok {
		t.Fatalf("runtime entries have unexpected type %T", runtime["entries"])
	}
	if len(entries) != len(wantEntryKeys) {
		t.Fatalf("serialized runtime entry count = %d, want %d", len(entries), len(wantEntryKeys))
	}
	for index, entry := range entries {
		entryDefinitionID, ok := entry["definitionId"].(int64)
		if !ok || entryDefinitionID != definitionID {
			t.Fatalf("serialized entry %q belongs to %#v, want definition %d", entry["entryKey"], entry["definitionId"], definitionID)
		}
		if entry["entryKey"] != wantEntryKeys[index] {
			t.Fatalf("serialized entry[%d] = %#v, want %q", index, entry["entryKey"], wantEntryKeys[index])
		}
	}
}

func installWorkflowEntryInsertFailureTrigger(t *testing.T, database *gorm.DB, definitionID int64, entryKey string) {
	t.Helper()
	statements := []string{
		fmt.Sprintf(`CREATE FUNCTION workflow_contract_reject_entry() RETURNS trigger AS $$
BEGIN
    IF NEW.workflow_definition_id = %d AND NEW.entry_key = %s THEN
        RAISE EXCEPTION 'workflow contract entry failure';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql`, definitionID, workflowContractSQLLiteral(entryKey)),
		`CREATE TRIGGER workflow_contract_reject_entry
BEFORE INSERT ON workflow_runtime_entries
FOR EACH ROW EXECUTE FUNCTION workflow_contract_reject_entry()`,
	}
	for _, statement := range statements {
		if err := database.Exec(statement).Error; err != nil {
			t.Fatalf("install workflow entry failure trigger: %v", err)
		}
	}
}

func installWorkflowStateDeactivateFailureTrigger(t *testing.T, database *gorm.DB, stateID int64) {
	t.Helper()
	statements := []string{
		fmt.Sprintf(`CREATE FUNCTION workflow_contract_reject_deactivation() RETURNS trigger AS $$
BEGIN
    IF OLD.id = %d AND NEW.active_workflow_definition_id IS NULL THEN
        RAISE EXCEPTION 'workflow contract deactivation failure';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql`, stateID),
		`CREATE TRIGGER workflow_contract_reject_deactivation
BEFORE UPDATE OF active_workflow_definition_id ON workflow_runtime_states
FOR EACH ROW EXECUTE FUNCTION workflow_contract_reject_deactivation()`,
	}
	for _, statement := range statements {
		if err := database.Exec(statement).Error; err != nil {
			t.Fatalf("install workflow deactivation failure trigger: %v", err)
		}
	}
}

func workflowContractSQLLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func workflowContractStatementTable(tx *gorm.DB) string {
	if tx.Statement.Table != "" {
		return tx.Statement.Table
	}
	if tx.Statement.Schema != nil {
		return tx.Statement.Schema.Table
	}
	return ""
}

func waitWorkflowContractSignal(t *testing.T, signal <-chan struct{}, timeoutMessage string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatal(timeoutMessage)
	}
}

func waitWorkflowContractResult(t *testing.T, result <-chan error, timeoutMessage string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal(timeoutMessage)
		return nil
	}
}

func openPostgresWorkflowContractDatabase(t *testing.T) *workflowContractDatabase {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(workflowContractPostgresDSNEnv))
	if dsn == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is required in CI", workflowContractPostgresDSNEnv)
		}
		t.Skipf("%s is not configured", workflowContractPostgresDSNEnv)
	}
	adminConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse PostgreSQL workflow contract DSN: %v", err)
	}
	adminDB := stdlib.OpenDB(*adminConfig)
	if err := adminDB.Ping(); err != nil {
		_ = adminDB.Close()
		t.Fatalf("ping PostgreSQL workflow contract database: %v", err)
	}
	schemaName := fmt.Sprintf(
		"workflow_transaction_contract_%d_%d_%d",
		os.Getpid(),
		time.Now().UnixNano(),
		workflowContractSchemaSequence.Add(1),
	)
	quotedSchema := `"` + strings.ReplaceAll(schemaName, `"`, `""`) + `"`
	if _, err := adminDB.Exec("CREATE SCHEMA " + quotedSchema); err != nil {
		_ = adminDB.Close()
		t.Fatalf("create PostgreSQL workflow contract schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminDB.Exec("DROP SCHEMA " + quotedSchema + " CASCADE"); err != nil {
			t.Errorf("drop PostgreSQL workflow contract schema: %v", err)
		}
		if err := adminDB.Close(); err != nil {
			t.Errorf("close PostgreSQL workflow contract admin database: %v", err)
		}
	})

	testConfig := adminConfig.Copy()
	if testConfig.RuntimeParams == nil {
		testConfig.RuntimeParams = make(map[string]string)
	}
	testConfig.RuntimeParams["search_path"] = schemaName
	primary := openPostgresWorkflowContractHandle(t, testConfig)
	prepareWorkflowContractRelations(t, primary)
	return &workflowContractDatabase{
		primary: primary,
		openPeer: func(t *testing.T) *gorm.DB {
			return openPostgresWorkflowContractHandle(t, testConfig)
		},
	}
}

func openPostgresWorkflowContractHandle(t *testing.T, cfg *pgx.ConnConfig) *gorm.DB {
	t.Helper()
	sqlDB := stdlib.OpenDB(*cfg)
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("ping PostgreSQL workflow contract schema: %v", err)
	}
	database, err := gorm.Open(gormpostgres.New(gormpostgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		_ = sqlDB.Close()
		t.Fatalf("open PostgreSQL workflow contract GORM handle: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close PostgreSQL workflow contract handle: %v", err)
		}
	})
	return database
}

func prepareWorkflowContractRelations(t *testing.T, database *gorm.DB) {
	t.Helper()
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("get workflow contract database: %v", err)
	}
	runner, err := migration.New(sqlDB)
	if err != nil {
		t.Fatalf("create workflow contract migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatalf("apply workflow contract baseline: %v", err)
	}
	if err := database.Exec(`INSERT INTO users (id, username, is_active) VALUES (1, 'workflow-owner-1', TRUE), (2, 'workflow-owner-2', TRUE)`).Error; err != nil {
		t.Fatalf("seed workflow contract owners: %v", err)
	}
	if err := database.Exec(`SELECT setval(pg_get_serial_sequence('users', 'id'), 2, TRUE)`).Error; err != nil {
		t.Fatalf("advance workflow contract owner sequence: %v", err)
	}
}
