package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"net/url"
	"regexp"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
)

var channelTypeLabels = map[string]string{
	"in_app":           "站内通知",
	"dingtalk_webhook": "钉钉机器人",
	"smtp_email":       "邮件通知",
}

var deliveryStatusLabels = map[string]string{
	"success":         "发送成功",
	"failed":          "发送失败",
	"pending":         "待发送",
	"skipped_offline": "离线跳过",
}

var templateVarPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.]+)\s*\}\}`)

// ---------- 渠道管理 ----------

// NotifyChannelUpsertPayload 渠道载荷。
type NotifyChannelUpsertPayload struct {
	ChannelType  string  `json:"channelType"`
	DisplayName  string  `json:"displayName"`
	IsEnabled    *bool   `json:"isEnabled"`
	SettingsJSON string  `json:"settingsJson"`
	SecretJSON   *string `json:"secretJson"`
	Remark       string  `json:"remark"`
}

// GetNotifyOverviewSummary 配置概览的通知摘要。
func (a *App) GetNotifyOverviewSummary() M {
	var latest db.SystemNotifyDelivery
	latestFound := a.DB.Order("created_at DESC, id DESC").First(&latest).Error == nil
	var channels []db.SystemNotifyChannel
	a.DB.Find(&channels)
	enabledCount := 0
	for _, channel := range channels {
		if channel.IsEnabled {
			enabledCount++
		}
	}
	var deliveryCount int64
	a.DB.Model(&db.SystemNotifyDelivery{}).Count(&deliveryCount)
	latestStatus := "unknown"
	latestAt := ""
	if latestFound {
		latestStatus = latest.Status
		latestAt = fmtTimeV(latest.CreatedAt)
	}
	return M{
		"channelCount": len(channels), "enabledChannelCount": enabledCount,
		"latestDeliveryStatus": latestStatus, "latestDeliveryAt": latestAt,
		"deliveryCount": deliveryCount,
	}
}

// ListNotifyChannels 渠道列表(非超管仅可见内置与自有)。
func (a *App) ListNotifyChannels(principal *Principal) []M {
	var channels []db.SystemNotifyChannel
	a.DB.Order("updated_at DESC, id DESC").Find(&channels)
	result := make([]M, 0, len(channels))
	for i := range channels {
		channel := &channels[i]
		if !principal.HasRole("R_SUPER") {
			owned := channel.OwnerID != nil && *channel.OwnerID == principal.User.ID
			if !channel.IsBuiltin && !owned {
				continue
			}
		}
		result = append(result, a.serializeChannel(channel))
	}
	return result
}

// CreateNotifyChannel 创建渠道。
func (a *App) CreateNotifyChannel(payload NotifyChannelUpsertPayload, principal *Principal) (M, error) {
	channelType := strings.TrimSpace(payload.ChannelType)
	if channelType != "dingtalk_webhook" && channelType != "smtp_email" {
		return nil, bizErr("当前仅支持创建钉钉和邮件渠道")
	}
	displayName := strings.TrimSpace(payload.DisplayName)
	if displayName == "" {
		return nil, bizErr("通知渠道名称不能为空")
	}
	settingsJSON, err := normalizeJSONText(payload.SettingsJSON, "渠道配置 JSON")
	if err != nil {
		return nil, err
	}
	secretText := "{}"
	if payload.SecretJSON != nil && strings.TrimSpace(*payload.SecretJSON) != "" {
		secretText = *payload.SecretJSON
	}
	encryptedSecret, err := a.encryptSecretJSON(secretText)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	channel := db.SystemNotifyChannel{
		ChannelType: channelType, OwnerID: &principal.User.ID, DisplayName: displayName,
		IsEnabled:    payload.IsEnabled == nil || *payload.IsEnabled,
		SettingsJSON: settingsJSON, EncryptedSecretsJSON: encryptedSecret,
		Remark: payload.Remark, CreatedAt: now, UpdatedAt: now,
	}
	if err := a.DB.Create(&channel).Error; err != nil {
		return nil, err
	}
	return a.serializeChannel(&channel), nil
}

// UpdateNotifyChannel 更新渠道。
func (a *App) UpdateNotifyChannel(channelID int64, payload NotifyChannelUpsertPayload, principal *Principal) (M, error) {
	channel, err := a.requireChannel(channelID, principal)
	if err != nil {
		return nil, err
	}
	if channel.IsBuiltin {
		return nil, bizErr("内置渠道不允许编辑")
	}
	displayName := strings.TrimSpace(payload.DisplayName)
	if displayName == "" {
		return nil, bizErr("通知渠道名称不能为空")
	}
	settingsText := payload.SettingsJSON
	if strings.TrimSpace(settingsText) == "" {
		settingsText = channel.SettingsJSON
	}
	settingsJSON, err := normalizeJSONText(settingsText, "渠道配置 JSON")
	if err != nil {
		return nil, err
	}
	isEnabled := channel.IsEnabled
	if payload.IsEnabled != nil {
		isEnabled = *payload.IsEnabled
	}
	remark := payload.Remark
	if remark == "" {
		remark = channel.Remark
	}
	fields := map[string]any{
		"display_name": displayName, "is_enabled": isEnabled,
		"settings_json": settingsJSON, "remark": remark, "updated_at": time.Now(),
	}
	if payload.SecretJSON != nil {
		secretText := *payload.SecretJSON
		if strings.TrimSpace(secretText) == "" {
			secretText = "{}"
		}
		encrypted, err := a.encryptSecretJSON(secretText)
		if err != nil {
			return nil, err
		}
		fields["encrypted_secrets_json"] = encrypted
	}
	if err := a.DB.Model(channel).Updates(fields).Error; err != nil {
		return nil, err
	}
	a.DB.First(channel, channelID)
	return a.serializeChannel(channel), nil
}

// DeleteNotifyChannel 删除渠道。
func (a *App) DeleteNotifyChannel(channelID int64, principal *Principal) error {
	channel, err := a.requireChannel(channelID, principal)
	if err != nil {
		return err
	}
	if channel.IsBuiltin {
		return bizErr("内置渠道不允许删除")
	}
	return a.DB.Delete(channel).Error
}

// SetNotifyChannelEnabled 启停渠道。
func (a *App) SetNotifyChannelEnabled(channelID int64, enabled bool, principal *Principal) (M, error) {
	channel, err := a.requireChannel(channelID, principal)
	if err != nil {
		return nil, err
	}
	if channel.IsBuiltin {
		return nil, bizErr("内置渠道不允许修改状态")
	}
	if err := a.DB.Model(channel).Updates(map[string]any{"is_enabled": enabled, "updated_at": time.Now()}).Error; err != nil {
		return nil, err
	}
	a.DB.First(channel, channelID)
	return a.serializeChannel(channel), nil
}

// TestNotifyChannel 渠道连通性测试。
func (a *App) TestNotifyChannel(channelID int64, principal *Principal) (M, error) {
	channel, err := a.requireChannel(channelID, principal)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if channel.ChannelType == "in_app" {
		return M{"success": true, "status": "success", "message": "站内通知渠道可直接使用", "testedAt": fmtTimeV(now)}, nil
	}
	runtimeChannel, err := a.buildRuntimeChannel(channel)
	if err != nil {
		return nil, err
	}
	ok, message, _ := validateNotifyChannel(runtimeChannel)
	status := "failed"
	if ok {
		status = "success"
	}
	a.DB.Model(channel).Updates(map[string]any{
		"last_test_status": status, "last_test_message": message,
		"last_tested_at": now, "updated_at": now,
	})
	return M{"success": ok, "status": status, "message": message, "testedAt": fmtTimeV(now)}, nil
}

// GetNotifyChannelMeta 渠道类型元数据。
func (a *App) GetNotifyChannelMeta() M {
	return M{
		"channelTypes": []M{
			{"value": "in_app", "label": channelTypeLabels["in_app"]},
			{"value": "dingtalk_webhook", "label": channelTypeLabels["dingtalk_webhook"]},
			{"value": "smtp_email", "label": channelTypeLabels["smtp_email"]},
		},
	}
}

// ---------- 投递历史与站内通知 ----------

// DeliveryHistoryQuery 投递历史查询。
type DeliveryHistoryQuery struct {
	Current              int
	Size                 int
	Keyword              string
	WorkflowDefinitionID *int64
	ChannelType          string
	DeliveryStatus       string
}

// ListDeliveryHistory 投递历史分页(非超管仅看自己)。
func (a *App) ListDeliveryHistory(principal *Principal, query DeliveryHistoryQuery) (M, error) {
	q := a.DB.Model(&db.SystemNotifyDelivery{}).
		Joins("LEFT JOIN notification_channels ON notification_deliveries.channel_id = notification_channels.id").
		Joins("LEFT JOIN users ON notification_deliveries.recipient_user_id = users.id").
		Joins("LEFT JOIN workflow_executions ON notification_deliveries.workflow_execution_id = workflow_executions.id").
		Joins("LEFT JOIN workflow_definitions ON workflow_executions.workflow_definition_id = workflow_definitions.id")

	if !principal.HasRole("R_SUPER") {
		q = q.Where("notification_deliveries.recipient_user_id = ?", principal.User.ID)
	}
	if query.WorkflowDefinitionID != nil {
		q = q.Where("workflow_executions.workflow_definition_id = ?", *query.WorkflowDefinitionID)
	}
	if query.ChannelType != "" {
		q = q.Where("notification_deliveries.channel_type = ?", query.ChannelType)
	}
	if query.DeliveryStatus != "" {
		q = q.Where("notification_deliveries.status = ?", query.DeliveryStatus)
	}
	if text := strings.TrimSpace(query.Keyword); text != "" {
		like := "%" + text + "%"
		q = q.Where(
			"COALESCE(notification_deliveries.title,'') LIKE ? OR COALESCE(notification_deliveries.content,'') LIKE ?"+
				" OR COALESCE(notification_channels.display_name,'') LIKE ? OR COALESCE(users.username,'') LIKE ?"+
				" OR COALESCE(workflow_definitions.display_name,'') LIKE ? OR COALESCE(workflow_definitions.code,'') LIKE ?",
			like, like, like, like, like, like,
		)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	var deliveries []db.SystemNotifyDelivery
	if err := q.Preload("Channel").Preload("RecipientUser").
		Preload("WorkflowExecution").Preload("WorkflowExecution.WorkflowDefinition").
		Order("notification_deliveries.created_at DESC, notification_deliveries.id DESC").
		Offset((query.Current - 1) * query.Size).Limit(query.Size).
		Find(&deliveries).Error; err != nil {
		return nil, err
	}
	records := make([]M, 0, len(deliveries))
	for i := range deliveries {
		records = append(records, a.serializeDelivery(&deliveries[i]))
	}
	return pagedResult(records, query.Current, query.Size, total), nil
}

// ListInAppNotifications 站内通知分页。
func (a *App) ListInAppNotifications(userID int64, current, size int) (M, error) {
	q := a.DB.Model(&db.SystemNotifyDelivery{}).
		Where("recipient_user_id = ? AND channel_type = ? AND status = ?", userID, "in_app", "success")
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	var deliveries []db.SystemNotifyDelivery
	if err := a.DB.Preload("WorkflowExecution").Preload("WorkflowExecution.WorkflowDefinition").
		Where("recipient_user_id = ? AND channel_type = ? AND status = ?", userID, "in_app", "success").
		Order("created_at DESC, id DESC").
		Offset((current - 1) * size).Limit(size).
		Find(&deliveries).Error; err != nil {
		return nil, err
	}
	records := make([]M, 0, len(deliveries))
	for i := range deliveries {
		records = append(records, a.serializeDelivery(&deliveries[i]))
	}
	result := pagedResult(records, current, size, total)
	result["unreadCount"] = a.countUnreadInApp(userID)
	return result, nil
}

// MarkInAppRead 标记单条已读。
func (a *App) MarkInAppRead(userID, deliveryID int64) error {
	result := a.DB.Model(&db.SystemNotifyDelivery{}).
		Where("id = ? AND recipient_user_id = ? AND channel_type = ?", deliveryID, userID, "in_app").
		Updates(map[string]any{"is_read": true, "read_at": time.Now()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return bizErr("通知不存在或无权操作")
	}
	return nil
}

// MarkAllInAppRead 全部已读。
func (a *App) MarkAllInAppRead(userID int64) M {
	result := a.DB.Model(&db.SystemNotifyDelivery{}).
		Where("recipient_user_id = ? AND channel_type = ? AND is_read = ?", userID, "in_app", false).
		Updates(map[string]any{"is_read": true, "read_at": time.Now()})
	return M{"updatedCount": result.RowsAffected, "unreadCount": 0}
}

// CountUnreadInApp 未读数。
func (a *App) CountUnreadInApp(userID int64) int64 { return a.countUnreadInApp(userID) }

func (a *App) countUnreadInApp(userID int64) int64 {
	var count int64
	a.DB.Model(&db.SystemNotifyDelivery{}).
		Where("recipient_user_id = ? AND channel_type = ? AND status = ? AND is_read = ?", userID, "in_app", "success", false).
		Count(&count)
	return count
}

// SendTestInAppNotification 发送站内测试通知。
func (a *App) SendTestInAppNotification(userID int64) M {
	now := time.Now()
	var channelID *int64
	var builtin db.SystemNotifyChannel
	if err := a.DB.Where("channel_type = ? AND is_builtin = ?", "in_app", true).First(&builtin).Error; err == nil {
		channelID = &builtin.ID
	}
	delivery := db.SystemNotifyDelivery{
		TargetType: "user", TargetID: &userID, RecipientUserID: &userID,
		ChannelID: channelID, ChannelType: "in_app", Status: "success",
		Title:   "站内通知测试",
		Content: "这是一条用于验证 WebSocket 和通知面板是否正常工作的测试消息。",
		SentAt:  &now, CreatedAt: now,
	}
	a.DB.Create(&delivery)
	record := M{
		"id": delivery.ID, "workflowExecutionId": nil, "workflowExecutionNodeId": nil,
		"workflowDefinitionId": nil, "workflowDefinitionCode": "", "workflowDefinitionName": "",
		"targetType": "user", "targetId": userID, "targetLabel": "",
		"recipientId": userID, "recipientLabel": "",
		"channelType": "in_app", "channelTypeLabel": "站内通知", "channelDisplayName": "站内通知",
		"deliveryStatus": "success", "deliveryStatusLabel": "发送成功",
		"messageTitle": delivery.Title, "messageContent": delivery.Content,
		"providerResponseText": "", "errorMessage": "",
		"isRead": false, "readAt": "", "sentAt": fmtTimeV(now), "createdAt": fmtTimeV(now),
	}
	unreadCount := a.countUnreadInApp(userID)
	a.Hub.SendToUser(userID, M{"type": "notice.created", "record": record, "unreadCount": unreadCount})
	return M{"record": record, "unreadCount": unreadCount}
}

// GetTargetMeta 通知目标元数据(工作流通知节点用)。
func (a *App) GetTargetMeta() M {
	var users []db.SystemUser
	a.DB.Where("is_active = ?", true).Order("username ASC, id ASC").Find(&users)
	var roles []db.SystemRole
	a.DB.Where("is_enabled = ?", true).Order("id ASC").Find(&roles)
	userItems := make([]M, 0, len(users))
	for _, user := range users {
		label := user.Nickname
		if label == "" {
			label = user.Username
		}
		userItems = append(userItems, M{"id": user.ID, "label": label})
	}
	roleItems := make([]M, 0, len(roles))
	for _, role := range roles {
		roleItems = append(roleItems, M{"id": role.ID, "label": role.DisplayName})
	}
	channelTypes := make([]M, 0, len(channelTypeLabels))
	for _, key := range []string{"in_app", "dingtalk_webhook", "smtp_email"} {
		channelTypes = append(channelTypes, M{"value": key, "label": channelTypeLabels[key]})
	}
	return M{
		"users": userItems, "roles": roleItems,
		"targetTypes":  []M{{"value": "user", "label": "用户"}, {"value": "role", "label": "角色"}},
		"channelTypes": channelTypes,
	}
}

// ---------- 通知节点派发 ----------

// dispatchNotifyNode 执行 notify 节点的实际派发。
func (a *App) dispatchNotifyNode(
	execution *db.WorkflowExecution,
	nodeLog *db.WorkflowExecutionNode,
	outboxEventID *int64,
	config M,
	variables M,
) (M, error) {
	targets := normalizeNotifyTargets(config["targets"])
	channelTypes := normalizeChannelTypes(config["channelTypes"])
	if len(targets) == 0 {
		return nil, bizErr("通知节点至少需要一个通知目标")
	}
	if len(channelTypes) == 0 {
		return nil, bizErr("通知节点至少需要一个通知渠道")
	}
	title := renderTemplate(asString(config["titleTemplate"]), variables)
	content := renderTemplate(asString(config["contentTemplate"]), variables)
	messageFormat := strings.TrimSpace(asString(config["messageFormat"]))
	if messageFormat == "" {
		messageFormat = "markdown"
	}

	roleIDs := make([]int64, 0)
	for _, target := range targets {
		if target["targetType"] == "role" {
			roleIDs = append(roleIDs, target["targetId"].(int64))
		}
	}
	roleUserMapping := a.listEnabledUserIDsByRoleIDs(roleIDs)

	dispatched, skipped, failed := 0, 0, 0
	hasInApp := false
	externalTypes := make([]string, 0, len(channelTypes))
	for _, item := range channelTypes {
		if item == "in_app" {
			hasInApp = true
		} else {
			externalTypes = append(externalTypes, item)
		}
	}

	for _, target := range targets {
		targetType := target["targetType"].(string)
		targetID := target["targetId"].(int64)
		var userIDs []int64
		if targetType == "user" {
			userIDs = []int64{targetID}
		} else {
			userIDs = roleUserMapping[targetID]
		}
		for _, userID := range userIDs {
			runtimeChannels := a.listUserRuntimeChannels(userID, externalTypes)
			resolvedTypes := map[string]bool{}
			for _, runtimeChannel := range runtimeChannels {
				resolvedTypes[runtimeChannel.ChannelType] = true
				if a.dispatchExternalChannel(execution, nodeLog, outboxEventID, targetType, targetID, userID, runtimeChannel, title, content, messageFormat) {
					dispatched++
				} else {
					failed++
				}
			}
			for _, missingType := range externalTypes {
				if resolvedTypes[missingType] {
					continue
				}
				a.DB.Create(&db.SystemNotifyDelivery{
					WorkflowExecutionID: &execution.ID, WorkflowExecutionNodeID: &nodeLog.ID,
					OutboxEventID: outboxEventID, TargetType: targetType, TargetID: &targetID,
					RecipientUserID: &userID, ChannelType: missingType, Status: "failed",
					Title: title, Content: content,
					ErrorMessage: "user channel not configured", ProviderResponseText: "user channel not configured",
					CreatedAt: time.Now(),
				})
				failed++
			}
			if hasInApp {
				switch a.dispatchInAppChannel(execution, nodeLog, outboxEventID, targetType, targetID, userID, title, content) {
				case "success":
					dispatched++
				case "skipped_offline":
					skipped++
				default:
					failed++
				}
			}
		}
	}
	return M{
		"title": title, "content": content, "channelTypes": channelTypes,
		"targetCount": len(targets), "dispatchedCount": dispatched,
		"skippedCount": skipped, "failedCount": failed,
	}, nil
}

func (a *App) dispatchExternalChannel(
	execution *db.WorkflowExecution, nodeLog *db.WorkflowExecutionNode, outboxEventID *int64,
	targetType string, targetID, userID int64, runtimeChannel *notifyRuntimeChannel,
	title, content, messageFormat string,
) bool {
	delivery := db.SystemNotifyDelivery{
		WorkflowExecutionID: &execution.ID, WorkflowExecutionNodeID: &nodeLog.ID,
		OutboxEventID: outboxEventID, TargetType: targetType, TargetID: &targetID,
		RecipientUserID: &userID, ChannelID: &runtimeChannel.ChannelID,
		ChannelType: runtimeChannel.ChannelType, Status: "pending",
		Title: title, Content: content, CreatedAt: time.Now(),
	}
	a.DB.Create(&delivery)
	ok, message, providerResponse := sendNotifyChannel(runtimeChannel, title, content, messageFormat)
	updates := map[string]any{
		"status":                 statusText(ok),
		"provider_response_text": providerResponse,
		"error_message":          "",
	}
	if !ok {
		updates["error_message"] = message
	} else {
		updates["sent_at"] = time.Now()
	}
	a.DB.Model(&delivery).Updates(updates)
	return ok
}

func (a *App) dispatchInAppChannel(
	execution *db.WorkflowExecution, nodeLog *db.WorkflowExecutionNode, outboxEventID *int64,
	targetType string, targetID, userID int64, title, content string,
) string {
	var channelID *int64
	var builtin db.SystemNotifyChannel
	if err := a.DB.Where("channel_type = ? AND is_builtin = ?", "in_app", true).First(&builtin).Error; err == nil {
		channelID = &builtin.ID
	}
	if !a.Hub.IsOnline(userID) {
		a.DB.Create(&db.SystemNotifyDelivery{
			WorkflowExecutionID: &execution.ID, WorkflowExecutionNodeID: &nodeLog.ID,
			OutboxEventID: outboxEventID, TargetType: targetType, TargetID: &targetID,
			RecipientUserID: &userID, ChannelID: channelID, ChannelType: "in_app",
			Status: "skipped_offline", Title: title, Content: content, CreatedAt: time.Now(),
		})
		return "skipped_offline"
	}
	delivery := db.SystemNotifyDelivery{
		WorkflowExecutionID: &execution.ID, WorkflowExecutionNodeID: &nodeLog.ID,
		OutboxEventID: outboxEventID, TargetType: targetType, TargetID: &targetID,
		RecipientUserID: &userID, ChannelID: channelID, ChannelType: "in_app",
		Status: "pending", Title: title, Content: content, CreatedAt: time.Now(),
	}
	a.DB.Create(&delivery)

	definitionID, definitionCode, definitionName := int64(0), "", ""
	if execution.WorkflowDefinition != nil {
		definitionID = execution.WorkflowDefinition.ID
		definitionCode = execution.WorkflowDefinition.Code
		definitionName = execution.WorkflowDefinition.DisplayName
	}
	unreadCount := a.countUnreadInApp(userID) + 1
	payload := M{
		"type": "notice.created",
		"record": M{
			"id": delivery.ID, "workflowExecutionId": execution.ID, "workflowExecutionNodeId": nodeLog.ID,
			"workflowDefinitionId": definitionID, "workflowDefinitionCode": definitionCode,
			"workflowDefinitionName": definitionName,
			"targetType":             targetType, "targetId": targetID, "targetLabel": "",
			"recipientId": userID, "recipientLabel": "",
			"channelType": "in_app", "channelTypeLabel": "站内通知", "channelDisplayName": "站内通知",
			"deliveryStatus": "success", "deliveryStatusLabel": "发送成功",
			"messageTitle": title, "messageContent": content,
			"providerResponseText": "", "errorMessage": "",
			"isRead": false, "readAt": "", "sentAt": "", "createdAt": fmtTimeV(delivery.CreatedAt),
		},
		"unreadCount": unreadCount,
	}
	if a.Hub.SendToUser(userID, payload) {
		a.DB.Model(&delivery).Updates(map[string]any{"status": "success", "sent_at": time.Now()})
		return "success"
	}
	a.DB.Model(&delivery).Updates(map[string]any{
		"status": "failed", "error_message": "websocket push failed", "provider_response_text": "websocket push failed",
	})
	return "failed"
}

// ---------- 渠道适配(钉钉 / SMTP) ----------

type notifyRuntimeChannel struct {
	ChannelID   int64
	ChannelType string
	DisplayName string
	Enabled     bool
	Config      M
	Secrets     M
}

func (a *App) buildRuntimeChannel(channel *db.SystemNotifyChannel) (*notifyRuntimeChannel, error) {
	secretsText, err := a.Cipher.Decrypt(channel.EncryptedSecretsJSON)
	if err != nil {
		return nil, err
	}
	if secretsText == "" {
		secretsText = "{}"
	}
	return &notifyRuntimeChannel{
		ChannelID: channel.ID, ChannelType: channel.ChannelType, DisplayName: channel.DisplayName,
		Enabled: channel.IsEnabled, Config: loadJSONObject(channel.SettingsJSON), Secrets: loadJSONObject(secretsText),
	}, nil
}

func (a *App) listUserRuntimeChannels(userID int64, channelTypes []string) []*notifyRuntimeChannel {
	if len(channelTypes) == 0 {
		return nil
	}
	var channels []db.SystemNotifyChannel
	a.DB.Where(
		"owner_id = ? AND channel_type IN ? AND is_enabled = ? AND is_builtin = ?",
		userID, channelTypes, true, false,
	).Order("updated_at DESC, id DESC").Find(&channels)
	result := make([]*notifyRuntimeChannel, 0, len(channels))
	for i := range channels {
		if runtimeChannel, err := a.buildRuntimeChannel(&channels[i]); err == nil {
			result = append(result, runtimeChannel)
		}
	}
	return result
}

func (a *App) listEnabledUserIDsByRoleIDs(roleIDs []int64) map[int64][]int64 {
	result := map[int64][]int64{}
	if len(roleIDs) == 0 {
		return result
	}
	var rows []struct {
		RoleID int64
		UserID int64
	}
	a.DB.Table("user_roles").
		Select("user_roles.role_id AS role_id, user_roles.user_id AS user_id").
		Joins("JOIN users ON user_roles.user_id = users.id").
		Where("user_roles.role_id IN ? AND users.is_active = ?", roleIDs, true).
		Order("user_roles.id ASC").Scan(&rows)
	for _, row := range rows {
		result[row.RoleID] = append(result[row.RoleID], row.UserID)
	}
	return result
}

func validateNotifyChannel(channel *notifyRuntimeChannel) (bool, string, string) {
	switch channel.ChannelType {
	case "dingtalk_webhook":
		ok, response := sendDingTalk(channel, "text", "", "【coinsphere】通知渠道连通性测试")
		message := "连接失败"
		if ok {
			message = "连接成功"
		}
		return ok, message, response
	case "smtp_email":
		if err := smtpValidate(channel); err != nil {
			return false, "连接失败: " + err.Error(), err.Error()
		}
		return true, "连接成功", "SMTP OK"
	default:
		return false, "不支持的通知渠道类型: " + channel.ChannelType, ""
	}
}

func sendNotifyChannel(channel *notifyRuntimeChannel, title, content, format string) (bool, string, string) {
	switch channel.ChannelType {
	case "dingtalk_webhook":
		msgType := "text"
		body := content
		if format == "markdown" {
			msgType = "markdown"
		} else if format == "html" {
			body = stripSimpleHTML(content)
		}
		ok, response := sendDingTalk(channel, msgType, title, body)
		message := "发送失败"
		if ok {
			message = "发送成功"
		}
		return ok, message, response
	case "smtp_email":
		if err := smtpSend(channel, title, content, format); err != nil {
			return false, "发送失败: " + err.Error(), err.Error()
		}
		return true, "发送成功", "{}"
	default:
		return false, "不支持的通知渠道类型: " + channel.ChannelType, ""
	}
}

func sendDingTalk(channel *notifyRuntimeChannel, msgType, title, content string) (bool, string) {
	accessToken := strings.TrimSpace(asString(channel.Secrets["accessToken"]))
	if accessToken == "" {
		return false, `{"errcode":400,"errmsg":"钉钉渠道缺少访问令牌"}`
	}
	secret := strings.TrimSpace(asString(channel.Secrets["secret"]))
	baseURL := asString(channel.Config["webhookBaseUrl"])
	if baseURL == "" {
		baseURL = "https://oapi.dingtalk.com/robot/send"
	}
	webhookURL := baseURL + "?access_token=" + url.QueryEscape(accessToken)
	if secret != "" {
		timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(timestamp + "\n" + secret))
		sign := url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
		webhookURL += "&timestamp=" + timestamp + "&sign=" + sign
	}
	var body M
	if msgType == "markdown" {
		body = M{"msgtype": "markdown", "markdown": M{"title": title, "text": content}}
	} else {
		body = M{"msgtype": "text", "text": M{"content": content}}
	}
	body["at"] = M{"atMobiles": []string{}, "isAtAll": false}
	raw, _ := json.Marshal(body)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(raw))
	if err != nil {
		return false, dumpJSON(M{"errcode": 500, "errmsg": err.Error()})
	}
	defer resp.Body.Close()
	var payload M
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false, dumpJSON(M{"errcode": 500, "errmsg": err.Error()})
	}
	errcode, _ := payload["errcode"].(float64)
	ok := resp.StatusCode == 200 && int(errcode) == 0
	return ok, dumpJSON(payload)
}

func smtpConfig(channel *notifyRuntimeChannel) (host string, port int, username, password string, useTLS bool, err error) {
	host = strings.TrimSpace(asString(channel.Config["host"]))
	username = strings.TrimSpace(asString(channel.Config["username"]))
	password = strings.TrimSpace(asString(channel.Secrets["password"]))
	if host == "" {
		return "", 0, "", "", false, bizErr("SMTP 服务器 不能为空")
	}
	if username == "" {
		return "", 0, "", "", false, bizErr("SMTP 用户名 不能为空")
	}
	if password == "" {
		return "", 0, "", "", false, bizErr("SMTP 密码 不能为空")
	}
	if raw, ok := channel.Config["port"].(float64); ok {
		port = int(raw)
	}
	useTLS = true
	if raw, ok := channel.Config["useTls"].(bool); ok {
		useTLS = raw
	}
	return host, port, username, password, useTLS, nil
}

func smtpDial(host string, port int, useTLS bool) (*smtp.Client, error) {
	if useTLS {
		if port == 0 {
			port = 465
		}
		conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", host, port), &tls.Config{ServerName: host})
		if err != nil {
			return nil, err
		}
		return smtp.NewClient(conn, host)
	}
	if port == 0 {
		port = 25
	}
	client, err := smtp.Dial(fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, err
	}
	// 尽量升级 STARTTLS,失败则沿用明文。
	if ok, _ := client.Extension("STARTTLS"); ok {
		_ = client.StartTLS(&tls.Config{ServerName: host})
	}
	return client, nil
}

func smtpValidate(channel *notifyRuntimeChannel) error {
	host, port, username, password, useTLS, err := smtpConfig(channel)
	if err != nil {
		return err
	}
	client, err := smtpDial(host, port, useTLS)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
		return err
	}
	return client.Noop()
}

func smtpSend(channel *notifyRuntimeChannel, title, content, format string) error {
	host, port, username, password, useTLS, err := smtpConfig(channel)
	if err != nil {
		return err
	}
	recipients := normalizeRecipients(channel.Config["recipients"])
	if len(recipients) == 0 {
		return bizErr("收件人不能为空")
	}
	fromEmail := strings.TrimSpace(asString(channel.Config["fromEmail"]))
	if fromEmail == "" {
		return bizErr("发件邮箱 不能为空")
	}
	fromName := strings.TrimSpace(asString(channel.Config["fromName"]))
	fromHeader := fromEmail
	if fromName != "" {
		fromHeader = fromName + " <" + fromEmail + ">"
	}

	contentType := "text/plain; charset=UTF-8"
	body := content
	if format == "html" {
		contentType = "text/html; charset=UTF-8"
	}
	message := strings.Join([]string{
		"From: " + fromHeader,
		"To: " + strings.Join(recipients, ", "),
		"Subject: " + title,
		"MIME-Version: 1.0",
		"Content-Type: " + contentType,
		"",
		body,
	}, "\r\n")

	client, err := smtpDial(host, port, useTLS)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
		return err
	}
	if err := client.Mail(fromEmail); err != nil {
		return err
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write([]byte(message)); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// ---------- 序列化与工具 ----------

func (a *App) requireChannel(channelID int64, principal *Principal) (*db.SystemNotifyChannel, error) {
	var channel db.SystemNotifyChannel
	if err := a.DB.First(&channel, channelID).Error; err != nil {
		return nil, bizErr("通知渠道不存在")
	}
	if !principal.HasRole("R_SUPER") && !channel.IsBuiltin {
		if channel.OwnerID == nil || *channel.OwnerID != principal.User.ID {
			return nil, bizErr("无权访问当前通知渠道")
		}
	}
	return &channel, nil
}

func (a *App) encryptSecretJSON(secretJSONText string) (string, error) {
	normalized, err := normalizeJSONText(secretJSONText, "密钥 JSON")
	if err != nil {
		return "", err
	}
	return a.Cipher.Encrypt(normalized), nil
}

func (a *App) serializeChannel(channel *db.SystemNotifyChannel) M {
	secretText, _ := a.Cipher.Decrypt(channel.EncryptedSecretsJSON)
	if secretText == "" {
		secretText = "{}"
	}
	secretValues := loadJSONObject(secretText)
	masked := M{}
	for key, value := range secretValues {
		text := asString(value)
		switch {
		case text == "":
			masked[key] = ""
		case len([]rune(text)) <= 8:
			masked[key] = strings.Repeat("*", len([]rune(text)))
		default:
			runes := []rune(text)
			stars := len(runes) - 8
			if stars < 4 {
				stars = 4
			}
			masked[key] = string(runes[:4]) + strings.Repeat("*", stars) + string(runes[len(runes)-4:])
		}
	}
	var ownerID any
	ownerLabel := ""
	if channel.OwnerID != nil {
		ownerID = *channel.OwnerID
		var owner db.SystemUser
		if err := a.DB.First(&owner, *channel.OwnerID).Error; err == nil {
			ownerLabel = owner.Nickname
			if ownerLabel == "" {
				ownerLabel = owner.Username
			}
		}
	}
	targetSummary := "当前用户"
	if channel.IsBuiltin {
		targetSummary = "系统内置"
	}
	maskedJSON, _ := json.MarshalIndent(masked, "", "  ")
	return M{
		"id": channel.ID, "channelType": channel.ChannelType,
		"channelTypeLabel": labelOr(channelTypeLabels, channel.ChannelType),
		"displayName":      channel.DisplayName, "ownerId": ownerID, "ownerLabel": ownerLabel,
		"isEnabled": channel.IsEnabled, "isBuiltin": channel.IsBuiltin,
		"settingsJson":     prettyJSONText(channel.SettingsJSON),
		"secretJsonMasked": string(maskedJSON),
		"lastTestStatus":   orDefault(channel.LastTestStatus, "unknown"),
		"lastTestMessage":  channel.LastTestMessage,
		"lastTestedAt":     fmtTime(channel.LastTestedAt),
		"remark":           channel.Remark, "updatedAt": fmtTimeV(channel.UpdatedAt),
		"targetSummary": targetSummary,
	}
}

func (a *App) serializeDelivery(delivery *db.SystemNotifyDelivery) M {
	var definition *db.WorkflowDefinition
	if delivery.WorkflowExecution != nil {
		definition = delivery.WorkflowExecution.WorkflowDefinition
	}
	var workflowExecutionID, workflowExecutionNodeID, workflowDefinitionID, targetID, recipientID any
	if delivery.WorkflowExecutionID != nil {
		workflowExecutionID = *delivery.WorkflowExecutionID
	}
	if delivery.WorkflowExecutionNodeID != nil {
		workflowExecutionNodeID = *delivery.WorkflowExecutionNodeID
	}
	definitionCode, definitionName := "", ""
	if definition != nil {
		workflowDefinitionID = definition.ID
		definitionCode = definition.Code
		definitionName = definition.DisplayName
	}
	targetLabel := ""
	if delivery.TargetID != nil {
		targetID = *delivery.TargetID
		targetLabel = a.resolveTargetLabel(delivery.TargetType, *delivery.TargetID)
	}
	recipientLabel := ""
	if delivery.RecipientUserID != nil {
		recipientID = *delivery.RecipientUserID
		if delivery.RecipientUser != nil {
			recipientLabel = delivery.RecipientUser.Username
		}
	}
	channelDisplayName := ""
	if delivery.Channel != nil {
		channelDisplayName = delivery.Channel.DisplayName
	}
	return M{
		"id":                      delivery.ID,
		"workflowExecutionId":     workflowExecutionID,
		"workflowExecutionNodeId": workflowExecutionNodeID,
		"workflowDefinitionId":    workflowDefinitionID,
		"workflowDefinitionCode":  definitionCode,
		"workflowDefinitionName":  definitionName,
		"targetType":              delivery.TargetType, "targetId": targetID, "targetLabel": targetLabel,
		"recipientId": recipientID, "recipientLabel": recipientLabel,
		"channelType":         delivery.ChannelType,
		"channelTypeLabel":    labelOr(channelTypeLabels, delivery.ChannelType),
		"channelDisplayName":  channelDisplayName,
		"deliveryStatus":      delivery.Status,
		"deliveryStatusLabel": labelOr(deliveryStatusLabels, delivery.Status),
		"messageTitle":        delivery.Title, "messageContent": delivery.Content,
		"providerResponseText": delivery.ProviderResponseText, "errorMessage": delivery.ErrorMessage,
		"isRead": delivery.IsRead, "readAt": fmtTime(delivery.ReadAt),
		"sentAt": fmtTime(delivery.SentAt), "createdAt": fmtTimeV(delivery.CreatedAt),
	}
}

func (a *App) resolveTargetLabel(targetType string, targetID int64) string {
	if targetType == "" || targetID == 0 {
		return ""
	}
	if targetType == "user" {
		var user db.SystemUser
		if err := a.DB.First(&user, targetID).Error; err != nil {
			return fmt.Sprintf("用户#%d", targetID)
		}
		if user.Nickname != "" {
			return user.Nickname
		}
		return user.Username
	}
	var role db.SystemRole
	if err := a.DB.First(&role, targetID).Error; err != nil {
		return fmt.Sprintf("角色#%d", targetID)
	}
	return role.DisplayName
}

func normalizeNotifyTargets(rawValue any) []M {
	items, ok := rawValue.([]any)
	if !ok {
		if text := strings.TrimSpace(asString(rawValue)); text != "" {
			var parsed []any
			if err := json.Unmarshal([]byte(text), &parsed); err == nil {
				items = parsed
			}
		}
	}
	normalized := []M{}
	seen := map[string]bool{}
	for _, itemAny := range items {
		item, ok := itemAny.(map[string]any)
		if !ok {
			continue
		}
		targetType := strings.TrimSpace(asString(item["targetType"]))
		targetID := asInt64(item["targetId"])
		if (targetType != "user" && targetType != "role") || targetID <= 0 {
			continue
		}
		key := targetType + ":" + fmt.Sprint(targetID)
		if seen[key] {
			continue
		}
		seen[key] = true
		normalized = append(normalized, M{"targetType": targetType, "targetId": targetID})
	}
	return normalized
}

func normalizeChannelTypes(rawValue any) []string {
	items, ok := rawValue.([]any)
	if !ok {
		if text := strings.TrimSpace(asString(rawValue)); text != "" {
			var parsed []any
			if err := json.Unmarshal([]byte(text), &parsed); err == nil {
				items = parsed
			}
		}
	}
	allowed := map[string]bool{"in_app": true, "dingtalk_webhook": true, "smtp_email": true}
	result := []string{}
	seen := map[string]bool{}
	for _, item := range items {
		value := strings.TrimSpace(asString(item))
		if !allowed[value] || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func normalizeRecipients(value any) []string {
	var raw []string
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			raw = append(raw, asString(item))
		}
	default:
		text := strings.ReplaceAll(asString(value), ";", ",")
		raw = strings.Split(text, ",")
	}
	result := []string{}
	for _, item := range raw {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func renderTemplate(template string, variables M) string {
	return templateVarPattern.ReplaceAllStringFunc(template, func(match string) string {
		groups := templateVarPattern.FindStringSubmatch(match)
		if len(groups) < 2 {
			return ""
		}
		value := readPath(variables, groups[1])
		if value == nil {
			return ""
		}
		if text, ok := value.(string); ok {
			return text
		}
		return dumpJSON(value)
	})
}

func stripSimpleHTML(content string) string {
	replacer := strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n", "</p>", "\n", "<p>", "")
	return replacer.Replace(content)
}

func asString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func asInt64(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		var parsed int64
		fmt.Sscanf(typed, "%d", &parsed)
		return parsed
	default:
		return 0
	}
}

func statusText(ok bool) string {
	if ok {
		return "success"
	}
	return "failed"
}

func labelOr(labels map[string]string, key string) string {
	if label, ok := labels[key]; ok {
		return label
	}
	return key
}
