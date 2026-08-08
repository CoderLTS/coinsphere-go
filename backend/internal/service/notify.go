package service

// 本文件用到的库:标准库负责加密签名(crypto/*)、发邮件(net/smtp)、发 HTTP 请求(net/http)、
// 正则(regexp)、JSON(encoding/json)等;最后一组 coinsphere/backend/internal/db 是本项目的数据库模型包。
// 标准库与本项目包之间空一行分组,是 Go 的惯例。见 GO入门笔记『import』
import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"regexp"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// channelTypeLabels 渠道类型代号 → 中文名(包级变量,程序启动即存在)。
// map[string]string 表示"键是 string、值是 string"的字典;这里把内部代号翻译成给人看的标签。见 GO入门笔记『复合类型』
var channelTypeLabels = map[string]string{
	"in_app":           "站内通知",
	"dingtalk_webhook": "钉钉机器人",
	"qq_bot":           "QQ Bot",
	"smtp_email":       "邮件通知",
}

var deliveryStatusLabels = map[string]string{
	"success":         "发送成功",
	"failed":          "发送失败",
	"pending":         "待发送",
	"skipped_offline": "离线跳过",
}

// templateVarPattern 预编译的正则,匹配模板里的 {{ 变量名 }} 占位符(渲染通知标题/内容时替换成真实值)。
// regexp.MustCompile 在程序启动时把正则编译一次;若正则写错会直接 panic —— 适合这种写死、必然正确的模式。
var templateVarPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.]+)\s*\}\}`)

// ---------- 渠道管理 ----------

// NotifyChannelUpsertPayload 渠道载荷。
// 这是"请求体结构":前端提交的 JSON 会被反序列化(解析)进这个 struct。反引号里的 `json:"channelType"` 是 struct tag(标签),
// 告诉 JSON 库该字段对应 JSON 里的哪个键。见 GO入门笔记『复合类型』
// *bool / *string 这类"指针字段"用于可选值:普通 bool 分不清"传了 false"和"没传",用指针就能靠 nil 表示"前端没提供",
// 从而在更新时"只改前端确实传了的字段"。
type NotifyChannelUpsertPayload struct {
	ChannelType  string  `json:"channelType"`
	DisplayName  string  `json:"displayName"`
	IsEnabled    *bool   `json:"isEnabled"`
	SettingsJSON string  `json:"settingsJson"`
	SecretJSON   *string `json:"secretJson"`
	Remark       string  `json:"remark"`
}

// GetNotifyOverviewSummary 当前用户配置概览的通知摘要。
// 返回类型 M 是本项目别名:type M = map[string]any(见 app.go),即"键为字符串、值任意"的字典,用来拼 JSON 响应。
// (a *App) 是方法接收者,a 即当前 App 实例;a.DB 是 GORM 的数据库句柄(*gorm.DB)。见 GO入门笔记『框架:GORM』
func (a *App) GetNotifyOverviewSummary(principal *Principal) M {
	// var 声明变量并给"零值";先建一个空记录,等 GORM 把查询结果写回它。
	var latest db.SystemNotifyDelivery
	// 链式查询:Order 排序 → First(&latest) 取第一条并写回;末尾 .Error 是本次错误,==nil 表示查到了。传 &latest(指针)让 GORM 把结果写回。
	deliveryQuery := a.DB.Where("recipient_user_id = ?", principal.User.ID)
	latestFound := deliveryQuery.Order("created_at DESC, id DESC").First(&latest).Error == nil
	// []db.SystemNotifyChannel 是"渠道切片"(一堆渠道);Find(&channels) 查出全部并写回。
	var channels []db.SystemNotifyChannel
	a.DB.Where("is_builtin = ? OR owner_id = ?", true, principal.User.ID).Find(&channels)
	enabledCount := 0
	// for _, v := range slice 遍历切片:_ 丢弃下标,channel 是每个元素。range 是本文件第一次出现。见 GO入门笔记『复合类型』
	for _, channel := range channels {
		if channel.IsEnabled {
			enabledCount++
		}
	}
	var deliveryCount int64
	deliveryQuery.Model(&db.SystemNotifyDelivery{}).Count(&deliveryCount)
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

// ListNotifyChannels 渠道列表(所有角色仅可见内置与自有)。
func (a *App) ListNotifyChannels(principal *Principal) []M {
	var channels []db.SystemNotifyChannel
	a.DB.Where("is_builtin = ? OR owner_id = ?", true, principal.User.ID).
		Order("updated_at DESC, id DESC").Find(&channels)
	result := make([]M, 0, len(channels))
	// 用 for i := range + channel := &channels[i](取第 i 个元素的地址),而不是 for _, channel := range channels。
	// 因为 range 的第二返回值是元素"副本",改它不影响原切片;要拿到指向真实元素的指针,就得用 &channels[i]。见 GO入门笔记『复合类型』
	for i := range channels {
		channel := &channels[i]
		result = append(result, a.serializeChannel(channel))
	}
	return result
}

// CreateNotifyChannel 创建渠道。
// 返回 (M, error):Go 的多返回值,约定最后一个是 error。成功返回 (对象, nil),失败返回 (nil, 错误)。见 GO入门笔记『错误处理』
func (a *App) CreateNotifyChannel(payload NotifyChannelUpsertPayload, principal *Principal) (M, error) {
	// strings.TrimSpace 去掉首尾空白;bizErr 造一个业务错误返回给前端。
	channelType := strings.TrimSpace(payload.ChannelType)
	if channelType != "dingtalk_webhook" && channelType != "qq_bot" && channelType != "smtp_email" {
		return nil, bizErr("当前仅支持创建钉钉、QQ Bot 和邮件渠道")
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
	// SecretJSON 是 *string(可选):先判非 nil,再用 *payload.SecretJSON 解引用取出内容。
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
	// Create(&channel) = INSERT 一行;传指针,GORM 会把自增主键 ID 等写回 channel。见 GO入门笔记『框架:GORM』
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
		secretText, err := normalizeJSONText(*payload.SecretJSON, "密钥 JSON")
		if err != nil {
			return nil, err
		}
		secretPatch := loadJSONObject(secretText)
		if len(secretPatch) > 0 {
			existingText, err := a.Cipher.Decrypt(channel.EncryptedSecretsJSON)
			if err != nil {
				return nil, err
			}
			existing := loadJSONObject(existingText)
			for key, value := range secretPatch {
				existing[key] = value
			}
			encrypted, err := a.encryptSecretJSON(dumpJSON(existing))
			if err != nil {
				return nil, err
			}
			fields["encrypted_secrets_json"] = encrypted
		}
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
func (a *App) TestNotifyChannel(ctx context.Context, channelID int64, principal *Principal) (M, error) {
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
	ok, message, _ := a.validateNotifyChannel(ctx, runtimeChannel)
	status := "failed"
	if ok {
		status = "success"
	}
	a.DB.WithContext(ctx).Model(channel).Updates(map[string]any{
		"last_test_status": status, "last_test_message": message,
		"last_tested_at": now, "updated_at": now,
	})
	return M{"success": ok, "status": status, "message": message, "testedAt": fmtTimeV(now)}, nil
}

// GetNotifyChannelMeta 渠道类型元数据。
func (a *App) GetNotifyChannelMeta() M {
	return M{
		"channelTypes": []M{
			{"value": "in_app", "label": channelTypeLabels["in_app"], "description": "应用内持久通知", "builtinReadonly": true},
			{"value": "dingtalk_webhook", "label": channelTypeLabels["dingtalk_webhook"], "description": "钉钉自定义机器人 Webhook"},
			{"value": "qq_bot", "label": channelTypeLabels["qq_bot"], "description": "腾讯官方 QQ Bot 群或频道消息"},
			{"value": "smtp_email", "label": channelTypeLabels["smtp_email"], "description": "通过 SMTP 投递邮件"},
		},
	}
}

// ---------- 投递历史与站内通知 ----------

// DeliveryHistoryQuery 投递历史查询。
type DeliveryHistoryQuery struct {
	Page                 CursorPage
	Keyword              string
	WorkflowDefinitionID *int64
	ChannelType          string
	DeliveryStatus       string
}

// ListDeliveryHistory 投递历史分页(非超管仅看自己)。
func (a *App) ListDeliveryHistory(principal *Principal, query DeliveryHistoryQuery) (M, error) {
	// GORM 查询可"先攒条件、最后执行":先建 q(此时还没查库),后面按需 q = q.Where(...) 叠加条件。
	// Joins 手写多表 LEFT JOIN,把渠道 / 用户 / 执行 / 工作流定义等关联表接进来,供筛选和展示。
	// 语句里的 ? 是占位符,真实值单独传、由 GORM 转义 —— 防 SQL 注入。见 GO入门笔记『框架:GORM』
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
	// 分页第一步:先用同样的条件 Count 出总条数 total(前端据此算总页数)。
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	afterID, err := query.Page.AfterID()
	if err != nil {
		return nil, err
	}
	if afterID > 0 {
		q = q.Where("notification_deliveries.id < ?", afterID)
	}
	var deliveries []db.SystemNotifyDelivery
	if err := q.Preload("Channel").Preload("RecipientUser").Preload("StrategySignal").
		Preload("WorkflowExecution").Preload("WorkflowExecution.WorkflowDefinition").
		Order("notification_deliveries.id DESC").Limit(query.Page.Limit + 1).
		Find(&deliveries).Error; err != nil {
		return nil, err
	}
	hasMore := len(deliveries) > query.Page.Limit
	if hasMore {
		deliveries = deliveries[:query.Page.Limit]
	}
	records := make([]M, 0, len(deliveries))
	for i := range deliveries {
		records = append(records, a.serializeDelivery(&deliveries[i]))
	}
	lastKey := ""
	if len(deliveries) > 0 {
		lastKey = int64CursorKey(deliveries[len(deliveries)-1].ID)
	}
	return cursorResult(records, query.Page, lastKey, hasMore, total), nil
}

// ListInAppNotifications 站内通知分页。
// 前端"通知面板"的数据源:只查该用户、渠道 in_app、状态 success 的记录,分页返回并附未读数(分页写法见上面 ListDeliveryHistory)。
func (a *App) ListInAppNotifications(userID int64, page CursorPage) (M, error) {
	q := a.DB.Model(&db.SystemNotifyDelivery{}).
		Where("recipient_user_id = ? AND channel_type = ? AND status = ?", userID, "in_app", "success")
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	afterID, err := page.AfterID()
	if err != nil {
		return nil, err
	}
	if afterID > 0 {
		q = q.Where("id < ?", afterID)
	}
	var deliveries []db.SystemNotifyDelivery
	if err := q.Preload("StrategySignal").Preload("WorkflowExecution").Preload("WorkflowExecution.WorkflowDefinition").
		Order("id DESC").Limit(page.Limit + 1).
		Find(&deliveries).Error; err != nil {
		return nil, err
	}
	hasMore := len(deliveries) > page.Limit
	if hasMore {
		deliveries = deliveries[:page.Limit]
	}
	records := make([]M, 0, len(deliveries))
	for i := range deliveries {
		records = append(records, a.serializeDelivery(&deliveries[i]))
	}
	lastKey := ""
	if len(deliveries) > 0 {
		lastKey = int64CursorKey(deliveries[len(deliveries)-1].ID)
	}
	result := cursorResult(records, page, lastKey, hasMore, total)
	result["unreadCount"] = a.countUnreadInApp(userID)
	return result, nil
}

// MarkInAppRead 标记单条已读。
func (a *App) MarkInAppRead(userID, deliveryID int64) error {
	// Updates(map) 只更新 map 里列出的这几列(is_read、read_at),其余列不动 = 一条 UPDATE。见 GO入门笔记『框架:GORM』
	// Where 里带 recipient_user_id 是越权保护:只能改自己的通知。
	result := a.DB.Model(&db.SystemNotifyDelivery{}).
		Where("id = ? AND recipient_user_id = ? AND channel_type = ?", deliveryID, userID, "in_app").
		Updates(map[string]any{"is_read": true, "read_at": time.Now()})
	if result.Error != nil {
		return result.Error
	}
	// RowsAffected 是本次实际影响的行数;为 0 说明没匹配到 —— 要么通知不存在,要么不是你的。
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
// 演示站内通知的完整两步:① 先把通知写进数据库(留痕、供通知面板查询);② 再通过 Hub 实时推给前端。
func (a *App) SendTestInAppNotification(userID int64) M {
	now := time.Now()
	// channelID 声明为 *int64(指针),这样"查不到内置渠道"时保持 nil = 不关联渠道。
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
	// Hub 在每条连接的唯一 writer 中补齐版本、序列与 UTC 时间；业务层只提供事件类型和 data。
	a.Hub.SendToUser(userID, RealtimeEvent{
		Type: "notice.created",
		Data: M{"record": record, "unreadCount": unreadCount},
	})
	return M{"record": record, "unreadCount": unreadCount}
}

var errCriticalNotificationDelivery = errors.New("critical notification delivery failed")

// deliverStrategySignalNotification 将持久信号事件幂等投递到站内和用户启用的固定外部渠道。
func (a *App) deliverStrategySignalNotification(ctx context.Context, event *domainEvent) error {
	if event.EventType != "strategy.signal.created" {
		return nil
	}
	if event.AggregateType != "strategy_signal" {
		return fmt.Errorf("strategy signal event has invalid aggregate type")
	}
	signalID, err := requiredStrategyUUID(event.AggregateID, "aggregateId")
	if err != nil {
		return err
	}

	database := a.dbWithContext(ctx)
	var signal db.StrategySignal
	if err := database.Where("id = ?", signalID).Take(&signal).Error; err != nil {
		return fmt.Errorf("load strategy signal notification: %w", err)
	}
	var version db.StrategyVersion
	if err := database.Where("id = ?", signal.StrategyVersionID).Take(&version).Error; err != nil {
		return fmt.Errorf("load strategy version notification: %w", err)
	}
	now := time.Now().UTC()
	title := "策略信号已生成"
	if signal.Mode == "manual" {
		switch {
		case signal.Status == "approved":
			title = "策略信号已批准"
		case signal.Status == "rejected":
			title = "策略信号已拒绝"
		case signal.Status == "expired" || signal.ExpiresAt == nil || !signal.ExpiresAt.After(now):
			title = "策略信号已过期"
		default:
			title = "策略信号待批准"
		}
	}
	content := fmt.Sprintf(
		"%s %s 目标仓位 %s（%s/%s）",
		version.Symbol, signal.Interval, signal.Target.String(), signal.Environment, signal.Mode,
	)
	if signal.ExpiresAt != nil {
		content += "，有效期至 " + formatUTC(*signal.ExpiresAt)
	}

	var deliveryErrors []error
	if err := a.deliverStrategySignalInApp(ctx, event.OutboxID, &signal, title, content, now); err != nil {
		deliveryErrors = append(deliveryErrors, err)
	}

	var channels []db.SystemNotifyChannel
	if err := database.Where(
		"owner_id = ? AND is_enabled = ? AND is_builtin = ? AND channel_type IN ?",
		signal.OwnerUserID, true, false, []string{"dingtalk_webhook", "qq_bot", "smtp_email"},
	).Order("id ASC").Find(&channels).Error; err != nil {
		return err
	}
	externalContent := content
	if a.Cfg != nil && strings.TrimSpace(a.Cfg.Server.PublicBaseURL) != "" {
		externalContent += "\n\n打开 CoinSphere: " + strings.TrimRight(a.Cfg.Server.PublicBaseURL, "/")
	}
	for i := range channels {
		if err := a.deliverStrategySignalExternal(ctx, event.OutboxID, &signal, &channels[i], title, externalContent); err != nil {
			deliveryErrors = append(deliveryErrors, err)
		}
	}
	return errors.Join(deliveryErrors...)
}

func (a *App) deliverStrategySignalInApp(
	ctx context.Context,
	outboxID int64,
	signal *db.StrategySignal,
	title, content string,
	now time.Time,
) error {
	database := a.dbWithContext(ctx)
	var channelID *int64
	var builtin db.SystemNotifyChannel
	if err := database.Where("channel_type = ? AND is_builtin = ?", "in_app", true).First(&builtin).Error; err == nil {
		channelID = &builtin.ID
	}
	delivery := db.SystemNotifyDelivery{
		OutboxEventID: &outboxID, StrategySignalID: &signal.ID, StrategySignal: signal,
		TargetType: "strategy_signal", RecipientUserID: &signal.OwnerUserID,
		ChannelID: channelID, ChannelType: "in_app", Status: "success",
		Title: title, Content: content, SentAt: &now, CreatedAt: now,
	}
	result := database.Clauses(clause.OnConflict{DoNothing: true}).Create(&delivery)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	if channelID != nil {
		delivery.Channel = &builtin
	}
	record := a.serializeDelivery(&delivery)
	unreadCount := a.countUnreadInApp(signal.OwnerUserID)
	if a.Hub != nil {
		a.Hub.SendToUser(signal.OwnerUserID, RealtimeEvent{
			Type: "notice.created",
			Data: M{"record": record, "unreadCount": unreadCount},
		})
	}
	return nil
}

func (a *App) deliverStrategySignalExternal(
	ctx context.Context,
	outboxID int64,
	signal *db.StrategySignal,
	channel *db.SystemNotifyChannel,
	title, content string,
) error {
	database := a.dbWithContext(ctx)
	var delivery db.SystemNotifyDelivery
	err := database.Where("strategy_signal_id = ? AND channel_id = ?", signal.ID, channel.ID).Take(&delivery).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		now := time.Now().UTC()
		delivery = db.SystemNotifyDelivery{
			OutboxEventID: &outboxID, StrategySignalID: &signal.ID,
			TargetType: "strategy_signal", RecipientUserID: &signal.OwnerUserID,
			ChannelID: &channel.ID, ChannelType: channel.ChannelType, Status: "pending",
			Title: title, Content: content, CreatedAt: now,
		}
		result := database.Clauses(clause.OnConflict{DoNothing: true}).Create(&delivery)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			err = database.Where("strategy_signal_id = ? AND channel_id = ?", signal.ID, channel.ID).Take(&delivery).Error
			if err != nil {
				return err
			}
		}
	} else if err != nil {
		return err
	}
	if delivery.Status == "success" {
		return nil
	}

	runtimeChannel, err := a.buildRuntimeChannel(channel)
	if err != nil {
		if updateErr := database.Model(&delivery).Updates(map[string]any{
			"status": "failed", "error_message": "channel configuration unavailable",
			"provider_response_text": "channel configuration unavailable",
		}).Error; updateErr != nil {
			return updateErr
		}
		return errCriticalNotificationDelivery
	}
	ok, message, providerResponse := a.sendNotifyChannel(ctx, runtimeChannel, title, content, "text")
	updates := map[string]any{
		"title": title, "content": content,
		"status": statusText(ok), "provider_response_text": providerResponse, "error_message": "",
	}
	if ok {
		updates["sent_at"] = time.Now().UTC()
	} else {
		updates["error_message"] = message
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cleanupCancel()
	if err := a.dbWithContext(cleanupCtx).Model(&delivery).Updates(updates).Error; err != nil {
		return err
	}
	if !ok {
		return errCriticalNotificationDelivery
	}
	return nil
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
	for _, key := range []string{"in_app", "dingtalk_webhook", "qq_bot", "smtp_email"} {
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
	ctx context.Context,
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			runtimeChannels := a.listUserRuntimeChannels(userID, externalTypes)
			resolvedTypes := map[string]bool{}
			for _, runtimeChannel := range runtimeChannels {
				resolvedTypes[runtimeChannel.ChannelType] = true
				sent := a.dispatchExternalChannel(ctx, execution, nodeLog, outboxEventID, targetType, targetID, userID, runtimeChannel, title, content, messageFormat)
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if sent {
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
				status := a.dispatchInAppChannel(execution, nodeLog, outboxEventID, targetType, targetID, userID, title, content)
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				switch status {
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
	ctx context.Context,
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
	if err := a.DB.WithContext(ctx).Create(&delivery).Error; err != nil {
		return false
	}
	ok, message, providerResponse := a.sendNotifyChannel(ctx, runtimeChannel, title, content, messageFormat)
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
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cleanupCancel()
	if err := a.dbWithContext(cleanupCtx).Model(&delivery).Updates(updates).Error; err != nil {
		slog.ErrorContext(cleanupCtx, "notification delivery finalization failed", "delivery_id", delivery.ID, "error_category", "database")
	}
	return ok
}

// dispatchInAppChannel 派发一条站内通知,返回投递结果("success"/"skipped_offline"/"failed")。
// 这是工作流"通知节点"推站内消息的核心:同样是"先写库留痕,再用 Hub 实时推送"。
func (a *App) dispatchInAppChannel(
	execution *db.WorkflowExecution, nodeLog *db.WorkflowExecutionNode, outboxEventID *int64,
	targetType string, targetID, userID int64, title, content string,
) string {
	var channelID *int64
	var builtin db.SystemNotifyChannel
	if err := a.DB.Where("channel_type = ? AND is_builtin = ?", "in_app", true).First(&builtin).Error; err == nil {
		channelID = &builtin.ID
	}
	// 用户当前不在线(没有任何 WebSocket 连接)就没法实时推:落一条 skipped_offline 记录,等他下次进面板看历史。
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
	// SendToUser=true 表示至少一条在线连接已接受入队；后续网络失败由持久记录和重连快照兜底，不引入额外 ACK 协议。
	if a.Hub.SendToUser(userID, RealtimeEvent{Type: "notice.created", Data: payload}) {
		a.DB.Model(&delivery).Updates(map[string]any{"status": "success", "sent_at": time.Now()})
		return "success"
	}
	a.DB.Model(&delivery).Updates(map[string]any{
		"status": "failed", "error_message": "websocket push failed", "provider_response_text": "websocket push failed",
	})
	return "failed"
}

// ---------- 渠道适配(钉钉 / QQ Bot / SMTP) ----------

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

func (a *App) validateNotifyChannel(ctx context.Context, channel *notifyRuntimeChannel) (bool, string, string) {
	switch channel.ChannelType {
	case "dingtalk_webhook":
		ok, response := sendDingTalk(ctx, channel, "text", "", "【coinsphere】通知渠道连通性测试")
		message := "连接失败"
		if ok {
			message = "连接成功"
		}
		return ok, message, response
	case "qq_bot":
		ok, response := a.sendQQBot(ctx, channel, "CoinSphere", "通知渠道连通性测试")
		message := "连接失败"
		if ok {
			message = "连接成功"
		}
		return ok, message, response
	case "smtp_email":
		if err := smtpValidate(ctx, channel); err != nil {
			return false, "连接失败: " + err.Error(), err.Error()
		}
		return true, "连接成功", "SMTP OK"
	default:
		return false, "不支持的通知渠道类型: " + channel.ChannelType, ""
	}
}

func (a *App) sendNotifyChannel(ctx context.Context, channel *notifyRuntimeChannel, title, content, format string) (bool, string, string) {
	switch channel.ChannelType {
	case "dingtalk_webhook":
		msgType := "text"
		body := content
		if format == "markdown" {
			msgType = "markdown"
		} else if format == "html" {
			body = stripSimpleHTML(content)
		}
		ok, response := sendDingTalk(ctx, channel, msgType, title, body)
		message := "发送失败"
		if ok {
			message = "发送成功"
		}
		return ok, message, response
	case "qq_bot":
		ok, response := a.sendQQBot(ctx, channel, title, content)
		message := "发送失败"
		if ok {
			message = "发送成功"
		}
		return ok, message, response
	case "smtp_email":
		if err := smtpSend(ctx, channel, title, content, format); err != nil {
			return false, "发送失败: " + err.Error(), err.Error()
		}
		return true, "发送成功", "{}"
	default:
		return false, "不支持的通知渠道类型: " + channel.ChannelType, ""
	}
}

func sendDingTalk(ctx context.Context, channel *notifyRuntimeChannel, msgType, title, content string) (bool, string) {
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
	// 钉钉"加签"安全模式:用密钥对"时间戳\n密钥"做 HMAC-SHA256,再 base64 + URL 编码成 sign 拼到 URL 上;
	// 钉钉服务端用同样算法校验,确认请求来自持有密钥者(防 webhook 被盗用)。这是标准库 crypto/hmac 的典型用法。
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
	// json.Marshal 把 body(map)序列化成 JSON 字节;raw, _ := 里的 _ 丢弃错误(内容可控,基本不会失败)。
	raw, _ := json.Marshal(body)
	// http.Client 是发 HTTP 请求的客户端,设 8 秒超时防卡死;Post 发一个 POST 请求。
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(raw))
	if err != nil {
		return false, dumpJSON(M{"errcode": 500, "errmsg": err.Error()})
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(request)
	if err != nil {
		return false, dumpJSON(M{"errcode": 500, "errmsg": err.Error()})
	}
	// defer resp.Body.Close():函数返回前一定关闭响应体、释放连接;忘了关会泄漏连接。见 GO入门笔记『defer』
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

func smtpDial(ctx context.Context, host string, port int, useTLS bool) (*smtp.Client, func() bool, error) {
	if port == 0 {
		if useTLS {
			port = 465
		} else {
			port = 25
		}
	}
	conn, err := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, nil, err
	}
	deadline := time.Now().Add(8 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	fail := func(err error) (*smtp.Client, func() bool, error) {
		stopCancel()
		_ = conn.Close()
		return nil, nil, err
	}
	if useTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: host})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fail(err)
		}
		conn = tlsConn
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return fail(err)
	}
	// 尽量升级 STARTTLS,失败则沿用明文。
	if !useTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			_ = client.StartTLS(&tls.Config{ServerName: host})
		}
	}
	if err := ctx.Err(); err != nil {
		_ = client.Close()
		stopCancel()
		return nil, nil, err
	}
	return client, stopCancel, nil
}

func smtpValidate(ctx context.Context, channel *notifyRuntimeChannel) error {
	host, port, username, password, useTLS, err := smtpConfig(channel)
	if err != nil {
		return err
	}
	client, stopCancel, err := smtpDial(ctx, host, port, useTLS)
	if err != nil {
		return err
	}
	defer client.Close()
	defer stopCancel()
	if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
		return err
	}
	return client.Noop()
}

func smtpSend(ctx context.Context, channel *notifyRuntimeChannel, title, content, format string) error {
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

	client, stopCancel, err := smtpDial(ctx, host, port, useTLS)
	if err != nil {
		return err
	}
	defer client.Close()
	defer stopCancel()
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
	if !channel.IsBuiltin && (channel.OwnerID == nil || *channel.OwnerID != principal.User.ID) {
		return nil, bizErr("无权访问当前通知渠道")
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
	var workflowExecutionID, workflowExecutionNodeID, workflowDefinitionID, strategySignalID, targetID, recipientID any
	if delivery.WorkflowExecutionID != nil {
		workflowExecutionID = *delivery.WorkflowExecutionID
	}
	if delivery.WorkflowExecutionNodeID != nil {
		workflowExecutionNodeID = *delivery.WorkflowExecutionNodeID
	}
	if delivery.StrategySignalID != nil {
		strategySignalID = delivery.StrategySignalID.String()
	}
	strategySignalMode, strategySignalStatus, strategySignalExpiresAt := "", "", ""
	if delivery.StrategySignal != nil {
		strategySignalMode = delivery.StrategySignal.Mode
		strategySignalStatus = delivery.StrategySignal.Status
		if delivery.StrategySignal.ExpiresAt != nil {
			strategySignalExpiresAt = formatUTC(*delivery.StrategySignal.ExpiresAt)
		}
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
		"strategySignalId":        strategySignalID,
		"strategySignalMode":      strategySignalMode,
		"strategySignalStatus":    strategySignalStatus,
		"strategySignalExpiresAt": strategySignalExpiresAt,
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
	allowed := map[string]bool{"in_app": true, "dingtalk_webhook": true, "qq_bot": true, "smtp_email": true}
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

// asInt64 把"任意类型 any"尽量转成 int64。
// switch v := x.(type) 是"类型 switch":JSON 解析出来的数字可能是 float64 / json.Number / string 等,这里按实际类型分别转换。
// x.(T) 这种写法叫"类型断言"(判断并取出接口底层的具体类型)。见 GO入门笔记『interface』
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
