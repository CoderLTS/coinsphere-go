package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"coinsphere/backend/internal/db"
)

type NotificationEvent struct {
	Type string `json:"type"`
	Data M      `json:"data"`
}

func (a *App) ListInAppNotifications(ctx context.Context, userID int64, page CursorPage) (M, error) {
	query := a.DB.WithContext(ctx).Model(&db.NotificationDelivery{}).
		Where("recipient_user_id = ? AND channel = ? AND status = ?", userID, "in_app", "delivered")
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, errors.New("count in-app notifications failed")
	}
	afterID, err := page.AfterID()
	if err != nil {
		return nil, err
	}
	if afterID > 0 {
		query = query.Where("id < ?", afterID)
	}
	var deliveries []db.NotificationDelivery
	if err := query.Order("id DESC").Limit(page.Limit + 1).Find(&deliveries).Error; err != nil {
		return nil, errors.New("list in-app notifications failed")
	}
	hasMore := len(deliveries) > page.Limit
	if hasMore {
		deliveries = deliveries[:page.Limit]
	}
	records := make([]M, len(deliveries))
	for index := range deliveries {
		records[index] = notificationRecord(deliveries[index])
	}
	lastKey := ""
	if len(deliveries) > 0 {
		lastKey = int64CursorKey(deliveries[len(deliveries)-1].ID)
	}
	result := cursorResult(records, page, lastKey, hasMore, total)
	result["unreadCount"] = a.CountUnreadInApp(ctx, userID)
	return result, nil
}

func (a *App) MarkInAppRead(ctx context.Context, userID, deliveryID int64) (int64, error) {
	now := time.Now().UTC()
	result := a.DB.WithContext(ctx).Model(&db.NotificationDelivery{}).
		Where("id = ? AND recipient_user_id = ? AND channel = ? AND status = ?", deliveryID, userID, "in_app", "delivered").
		Updates(map[string]any{"is_read": true, "read_at": now, "updated_at": now})
	if result.Error != nil {
		return 0, errors.New("mark in-app notification read failed")
	}
	if result.RowsAffected == 0 {
		return 0, ErrNotFound
	}
	return a.CountUnreadInApp(ctx, userID), nil
}

func (a *App) MarkAllInAppRead(ctx context.Context, userID int64) (M, error) {
	now := time.Now().UTC()
	result := a.DB.WithContext(ctx).Model(&db.NotificationDelivery{}).
		Where("recipient_user_id = ? AND channel = ? AND status = ? AND is_read = ?", userID, "in_app", "delivered", false).
		Updates(map[string]any{"is_read": true, "read_at": now, "updated_at": now})
	if result.Error != nil {
		return nil, errors.New("mark all in-app notifications read failed")
	}
	return M{"updatedCount": result.RowsAffected, "unreadCount": 0}, nil
}

func (a *App) CountUnreadInApp(ctx context.Context, userID int64) int64 {
	var count int64
	_ = a.DB.WithContext(ctx).Model(&db.NotificationDelivery{}).
		Where("recipient_user_id = ? AND channel = ? AND status = ? AND is_read = ?", userID, "in_app", "delivered", false).
		Count(&count).Error
	return count
}

func (a *App) PublishInAppNotification(ctx context.Context, userID, deliveryID int64) {
	var delivery db.NotificationDelivery
	if err := a.DB.WithContext(ctx).Where("id = ? AND recipient_user_id = ?", deliveryID, userID).Take(&delivery).Error; err != nil {
		return
	}
	event := NotificationEvent{Type: "notice.created", Data: M{
		"record": notificationRecord(delivery), "unreadCount": a.CountUnreadInApp(ctx, userID),
	}}
	a.notificationWatchMu.Lock()
	defer a.notificationWatchMu.Unlock()
	for watcher := range a.notificationWatches[userID] {
		select {
		case watcher <- event:
		default:
			delete(a.notificationWatches[userID], watcher)
			close(watcher)
		}
	}
	if len(a.notificationWatches[userID]) == 0 {
		delete(a.notificationWatches, userID)
	}
}

func (a *App) SubscribeNotificationEvents(ctx context.Context, userID int64) (<-chan NotificationEvent, func()) {
	watcher := make(chan NotificationEvent, 64)
	a.notificationWatchMu.Lock()
	watcher <- NotificationEvent{Type: "notice.unread", Data: M{"unreadCount": a.CountUnreadInApp(ctx, userID)}}
	if a.notificationWatches[userID] == nil {
		a.notificationWatches[userID] = map[chan NotificationEvent]struct{}{}
	}
	a.notificationWatches[userID][watcher] = struct{}{}
	a.notificationWatchMu.Unlock()
	var once sync.Once
	return watcher, func() {
		once.Do(func() {
			a.notificationWatchMu.Lock()
			if _, exists := a.notificationWatches[userID][watcher]; exists {
				delete(a.notificationWatches[userID], watcher)
				close(watcher)
			}
			if len(a.notificationWatches[userID]) == 0 {
				delete(a.notificationWatches, userID)
			}
			a.notificationWatchMu.Unlock()
		})
	}
}

func (a *App) CloseNotificationEvents() {
	a.notificationWatchMu.Lock()
	defer a.notificationWatchMu.Unlock()
	for _, watchers := range a.notificationWatches {
		for watcher := range watchers {
			close(watcher)
		}
	}
	a.notificationWatches = map[int64]map[chan NotificationEvent]struct{}{}
}

func notificationRecord(delivery db.NotificationDelivery) M {
	return M{
		"id": delivery.ID, "workflowExecutionId": nil, "workflowExecutionNodeId": nil,
		"workflowDefinitionId": delivery.WorkflowID, "workflowDefinitionCode": "", "workflowDefinitionName": "",
		"strategySignalId": nil, "strategySignalMode": "", "strategySignalStatus": "", "strategySignalExpiresAt": "",
		"targetType": "user", "targetId": delivery.RecipientUserID, "targetLabel": "",
		"recipientId": delivery.RecipientUserID, "recipientLabel": "",
		"channelType": delivery.Channel, "channelTypeLabel": "站内通知", "channelDisplayName": "站内通知",
		"deliveryStatus": delivery.Status, "deliveryStatusLabel": "已送达",
		"messageTitle": delivery.Title, "messageContent": delivery.Message,
		"providerResponseText": "", "errorMessage": "", "isRead": delivery.IsRead,
		"readAt": fmtTime(delivery.ReadAt), "sentAt": fmtTime(delivery.DeliveredAt),
		"createdAt": delivery.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func fmtTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
