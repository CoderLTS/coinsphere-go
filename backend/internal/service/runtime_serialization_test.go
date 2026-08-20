package service

import (
	"testing"
	"time"

	"coinsphere/backend/internal/db"
)

func TestExecutionSummaryUsesDisplayFieldsOnly(t *testing.T) {
	workerID := "internal-worker"
	idempotencyKey := "internal-key"
	execution := &db.WorkflowExecution{
		ID: 7, WorkflowDefinitionID: 3, StartNodeID: "start",
		TriggerType: "event", Status: "failed", QueuedAt: time.Now().UTC(),
		WorkerID: &workerID, IdempotencyKey: &idempotencyKey,
		FailureCategory: failureBusiness, ErrorMessage: "参数缺失",
		WorkflowDefinition: &db.WorkflowDefinition{
			Code: "internal.code", Version: 2, DisplayName: "行情策略",
			GraphJSON: dumpJSON(M{"nodes": []any{M{
				"id": "start", "type": "start.event", "label": "事件开始",
				"config": M{"entryKey": "workflow.failed.default", "displayName": "K 线收盘"},
			}}}),
		},
	}

	got := (&App{}).serializeExecutionSummary(execution)
	for _, key := range []string{"workflowDefinitionCode", "startEntryKey", "workerId", "idempotencyKey", "inputSnapshotJson"} {
		if _, exists := got[key]; exists {
			t.Fatalf("internal field %q leaked in execution summary", key)
		}
	}
	if got["entryName"] != "K 线收盘" || got["statusLabel"] != "失败" || got["triggerLabel"] != "事件触发" {
		t.Fatalf("unexpected display fields: %#v", got)
	}
	errorInfo, ok := got["error"].(M)
	if !ok || errorInfo["category"] != "配置或业务校验失败" || errorInfo["summary"] != "参数缺失" {
		t.Fatalf("unexpected structured error: %#v", got["error"])
	}
}

func TestTerminalWorkflowExecutionStatusesIncludeCanceled(t *testing.T) {
	want := []string{"success", "failed", "canceled"}
	for index := range want {
		if terminalWorkflowExecutionStatuses[index] != want[index] {
			t.Fatalf("terminal status[%d] = %q, want %q", index, terminalWorkflowExecutionStatuses[index], want[index])
		}
	}
}

func TestRuntimeEntryUsesDisplayName(t *testing.T) {
	got := serializeRuntimeEntry(&db.WorkflowRuntimeEntry{EntryKey: "manual.internal"}, "人工复核")
	if got["entryName"] != "人工复核" {
		t.Fatalf("entryName = %#v, want display name", got["entryName"])
	}
}
