package service

import (
	"coinsphere/backend/internal/db"
	"coinsphere/backend/plugin/sdk"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"sort"
	"strings"
	"time"
)

func (a *App) DeliverInbox(ctx context.Context, request sdk.InboxRequest) (json.RawMessage, error) {
	if len(request.Title) > 255 || len(request.Message) > 10000 || len(request.Targets) > 256 || request.OperationKey == "" || len(request.SubjectKey) > 128 {
		return nil, errors.New("invalid inbox request")
	}
	workflowID, revisionID, err := request.Revision.IDs()
	if err != nil {
		return nil, err
	}
	recipients, err := a.inboxRecipients(ctx, workflowID, request.Targets)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	err = a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lease, ok := ctx.Value(executionLeaseKey{}).(db.WorkflowRun)
		if !ok || lease.WorkflowID != workflowID || lease.RevisionID != revisionID {
			return ErrPermission
		}
		if err := a.lockExecutionLease(tx, lease, nil); err != nil {
			return err
		}
		var runNode db.WorkflowRunNode
		if err := tx.Where("run_id=? AND node_instance_id=? AND operation_key=? AND status=?", lease.ID, request.NodeInstanceID, request.OperationKey, RunStatusRunning).First(&runNode).Error; err != nil {
			return ErrPermission
		}
		for _, userID := range recipients {
			delivery := db.NotificationDelivery{OperationKey: request.OperationKey, WorkflowID: workflowID, RevisionID: revisionID, NodeInstanceID: request.NodeInstanceID, Channel: "in_app", RecipientUserID: &userID, SubjectKey: request.SubjectKey, Title: strings.TrimSpace(request.Title), Message: request.Message, Status: "delivered", AttemptCount: 1, DeliveredAt: &now, CreatedAt: now, UpdatedAt: now}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&delivery).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var deliveries []db.NotificationDelivery
	if err := a.DB.WithContext(ctx).Where("operation_key=? AND recipient_user_id IN ?", request.OperationKey, recipients).Order("id").Find(&deliveries).Error; err != nil {
		return nil, err
	}
	if len(deliveries) != len(recipients) {
		return nil, errors.New("inbox recipient mismatch")
	}
	ids := []int64{}
	for _, d := range deliveries {
		ids = append(ids, d.ID)
		a.PublishInAppNotification(ctx, *d.RecipientUserID, d.ID)
	}
	return json.Marshal(M{"deliveryId": ids[0], "deliveryIds": ids, "recipientCount": len(ids), "channel": "in_app", "status": "delivered", "deliveredAt": formatWorkflowTime(*deliveries[0].DeliveredAt)})
}
func (a *App) inboxRecipients(ctx context.Context, workflowID int64, targets []sdk.RecipientTarget) ([]int64, error) {
	if len(targets) == 0 {
		var workflow struct{ OwnerUserID int64 }
		if err := a.DB.WithContext(ctx).Table("workflows").Select("owner_user_id").Where("id = ?", workflowID).Take(&workflow).Error; err != nil {
			return nil, errors.New("load workflow notification owner failed")
		}
		targets = []sdk.RecipientTarget{{TargetType: "user", TargetID: workflow.OwnerUserID}}
	}
	userSet, roleSet := map[int64]struct{}{}, map[int64]struct{}{}
	for _, target := range targets {
		if target.TargetID <= 0 || target.TargetType != "user" && target.TargetType != "role" {
			return nil, errors.New("in-app notification target is invalid")
		}
		if target.TargetType == "user" {
			userSet[target.TargetID] = struct{}{}
		} else {
			roleSet[target.TargetID] = struct{}{}
		}
	}
	directUsers, roleIDs := int64SetValues(userSet), int64SetValues(roleSet)
	if len(directUsers) > 0 {
		var active []int64
		if err := a.DB.WithContext(ctx).Model(&db.SystemUser{}).Where("id IN ? AND is_active = ?", directUsers, true).Pluck("id", &active).Error; err != nil || len(active) != len(directUsers) {
			return nil, errors.New("in-app notification user target is unavailable")
		}
		userSet = make(map[int64]struct{}, len(active))
		for _, userID := range active {
			userSet[userID] = struct{}{}
		}
	}
	if len(roleIDs) > 0 {
		var enabledRoles []int64
		if err := a.DB.WithContext(ctx).Model(&db.SystemRole{}).Where("id IN ? AND is_enabled = ?", roleIDs, true).Pluck("id", &enabledRoles).Error; err != nil || len(enabledRoles) != len(roleIDs) {
			return nil, errors.New("in-app notification role target is unavailable")
		}
		var roleUsers []int64
		if err := a.DB.WithContext(ctx).Table("user_roles").
			Select("DISTINCT user_roles.user_id").Joins("JOIN users ON users.id = user_roles.user_id").
			Where("user_roles.role_id IN ? AND users.is_active = ?", roleIDs, true).Pluck("user_roles.user_id", &roleUsers).Error; err != nil {
			return nil, errors.New("resolve in-app notification role users failed")
		}
		for _, userID := range roleUsers {
			userSet[userID] = struct{}{}
		}
	}
	result := int64SetValues(userSet)
	if len(result) == 0 {
		return nil, errors.New("in-app notification has no active recipients")
	}
	return result, nil
}

func int64SetValues(values map[int64]struct{}) []int64 {
	result := make([]int64, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
