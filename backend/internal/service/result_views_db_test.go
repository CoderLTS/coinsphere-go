package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestResultViewAuthorizationActionsAndRevocation(t *testing.T) {
	app, database, ownerID := openWorkflowTestApp(t)
	var superRoleID, userRoleID, userID, outsiderID int64
	if err := database.QueryRow(`INSERT INTO roles (code) VALUES ('R_SUPER') RETURNING id`).Scan(&superRoleID); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`INSERT INTO roles (code) VALUES ('R_USER') RETURNING id`).Scan(&userRoleID); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`INSERT INTO users (username) VALUES ('result-user') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`INSERT INTO users (username) VALUES ('result-outsider') RETURNING id`).Scan(&outsiderID); err != nil {
		t.Fatal(err)
	}
	for _, binding := range [][2]int64{{ownerID, superRoleID}, {userID, userRoleID}, {outsiderID, userRoleID}} {
		if _, err := database.Exec(`INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)`, binding[0], binding[1]); err != nil {
			t.Fatal(err)
		}
	}
	admin, err := app.buildPrincipal(ownerID)
	if err != nil {
		t.Fatal(err)
	}
	user, err := app.buildPrincipal(userID)
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := app.buildPrincipal(outsiderID)
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Paper workflow"}, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), workflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	batch, err := app.CreateWorkflowBatch(context.Background(), workflow.ID, admin)
	if err != nil {
		t.Fatal(err)
	}

	created, err := app.CreateResultView(context.Background(), ResultViewCreatePayload{
		Name: "Paper review", PluginID: "official.quant", PageKey: "paper",
		Scope:          json.RawMessage(fmt.Sprintf(`{"workflowId":%d,"signalNodeInstanceId":"signal","paperNodeInstanceId":"paper"}`, workflow.ID)),
		Filters:        json.RawMessage(`{"market":"spot","instrument":"BTCUSDT"}`),
		AllowedActions: []string{"approve", "retry", "cancel", "pause"}, UserIDs: []int64{userID},
	}, admin)
	if err != nil {
		t.Fatal(err)
	}
	visible, err := app.GetResultView(context.Background(), created.ID, user)
	if err != nil || len(visible.Scope) != 0 || len(visible.Filters) != 0 {
		t.Fatalf("ordinary result view = %#v, err=%v", visible, err)
	}
	if scope, err := app.ResolveResultScope(context.Background(), created.ID, "approve", user); err != nil || scope.UserID != userID {
		t.Fatalf("approved result scope = %#v, err=%v", scope, err)
	}
	if _, err := app.ResolveResultScope(context.Background(), created.ID, "export", user); !errors.Is(err, ErrPermission) {
		t.Fatalf("operation allowlist error = %v", err)
	}
	scope, err := app.ResolveResultScope(context.Background(), created.ID, "cancel", user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyResultScopeBatchAction(context.Background(), scope, batch.ID, "retry"); !errors.Is(err, ErrConflict) {
		t.Fatalf("queued batch retry error = %v", err)
	}
	if batches, err := app.ListResultScopeBatches(context.Background(), scope); err != nil || len(batches) != 1 || batches[0].ID != batch.ID {
		t.Fatalf("scoped result batches = %#v, err=%v", batches, err)
	}
	otherWorkflow, err := app.CreateWorkflow(context.Background(), WorkflowCreatePayload{Name: "Other workflow"}, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyWorkflowLifecycle(context.Background(), otherWorkflow.ID, WorkflowLifecyclePayload{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	otherBatch, err := app.CreateWorkflowBatch(context.Background(), otherWorkflow.ID, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ApplyResultScopeBatchAction(context.Background(), scope, otherBatch.ID, "cancel"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workflow batch error = %v", err)
	}
	cancelled, err := app.ApplyResultScopeBatchAction(context.Background(), scope, batch.ID, "cancel")
	if err != nil || cancelled.Status != BatchStatusCancelled {
		t.Fatalf("cancelled result batch = %#v, err=%v", cancelled, err)
	}
	paused, err := app.PauseResultScopeWorkflow(context.Background(), scope)
	if err != nil || paused.Status != WorkflowStatusPaused {
		t.Fatalf("paused result workflow = %#v, err=%v", paused, err)
	}
	if _, err := app.GetResultView(context.Background(), created.ID, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unauthorized view error = %v", err)
	}
	if _, err := app.GetResultView(context.Background(), created.ID+9999, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing view error = %v", err)
	}
	if _, err := database.Exec(`UPDATE result_views SET scope_json = '{"workflowId":42}'::jsonb WHERE id = $1`, created.ID); err == nil {
		t.Fatal("immutable result scope was updated")
	}

	if _, err := app.ReplaceResultViewGrants(context.Background(), created.ID, ResultViewGrantPayload{RoleCodes: []string{"R_USER"}}, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GetResultView(context.Background(), created.ID, outsider); err != nil {
		t.Fatalf("role-granted result view: %v", err)
	}
	revoked, err := app.RevokeResultView(context.Background(), created.ID, admin)
	if err != nil || revoked.Status != "revoked" {
		t.Fatalf("revoked result view = %#v, err=%v", revoked, err)
	}
	if _, err := app.GetResultView(context.Background(), created.ID, user); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked ordinary view error = %v", err)
	}
	if _, err := app.ResolveResultScope(context.Background(), created.ID, "", user); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked result scope error = %v", err)
	}
	if _, err := app.CreateResultView(context.Background(), ResultViewCreatePayload{
		Name: "Invalid action", PluginID: "official.quant", PageKey: "paper",
		Scope:          json.RawMessage(fmt.Sprintf(`{"workflowId":%d,"signalNodeInstanceId":"signal","paperNodeInstanceId":"paper"}`, workflow.ID)),
		AllowedActions: []string{"unknown_action"},
	}, admin); err == nil {
		t.Fatal("undeclared result action was accepted")
	}
}
