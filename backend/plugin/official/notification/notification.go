package notification

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	notificationPluginID = "official.notification"
	notificationTimeout  = 8 * time.Second
)

type notificationRuntime struct {
	db    *gorm.DB
	inbox sdk.InboxService
	http  sdk.NetworkClient
}

type notificationAction struct {
	runtime *notificationRuntime
	channel string
}

type ExternalDeliveryInput struct {
	SubjectKey string `json:"subjectKey"`
	Message    string `json:"message"`
}

type notificationTarget = sdk.RecipientTarget

func Register(registrar sdk.Registrar, host sdk.Host) error {
	if err := registrar.WorkflowValidator(sdk.WorkflowValidatorFunc(validateComposeGraph)); err != nil {
		return err
	}
	client, err := host.Network.New([]string{"oapi.dingtalk.com"})
	if err != nil {
		return err
	}
	client.SetTimeout(notificationTimeout)
	client.DisableRedirects()
	runtime := &notificationRuntime{db: host.Store.DB(), inbox: host.Inbox, http: client}
	return runtime.register(registrar)
}

func (n *notificationRuntime) register(registrar sdk.Registrar) error {
	if err := registerCompose(registrar); err != nil {
		return err
	}
	if err := registrar.Cleanup(cleanupWorkflow); err != nil {
		return err
	}
	descriptors := []sdk.NodeDescriptor{
		{
			ExecutionPermissions: []string{"plugins.official.notification.execute"}, Type: "official.notification.in_app", Version: "1.0.0", Kind: sdk.NodeKindAction,
			Title: "站内通知", Description: "向用户或角色发送站内通知", Category: "notification", Aliases: []string{"站内消息", "通知提醒"}, Tags: []string{"通知", "用户", "角色"}, SortOrder: 10, Color: "#7c3aed", Icon: "bell", Width: 220, Height: 72,
			ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"title":{"type":"string","title":"通知标题","minLength":1,"maxLength":160},"targets":{"type":"array","title":"通知目标","maxItems":100,"items":{"type":"object","properties":{"targetType":{"type":"string","title":"目标类型","enum":["user","role"],"enumLabels":["用户","角色"]},"targetId":{"type":"integer","title":"目标 ID","minimum":1}},"required":["targetType","targetId"],"additionalProperties":false}}},"required":["title"],"additionalProperties":false}`),
			UISchema:     json.RawMessage(`{"ui:order":["title","targets"]}`),
		},
		{
			ExecutionPermissions: []string{"plugins.official.notification.execute"}, Type: "official.notification.dingtalk", Version: "1.0.0", Kind: sdk.NodeKindAction,
			Title: "钉钉通知", Description: "通过钉钉机器人发送通知", Category: "notification", Aliases: []string{"钉钉消息", "DingTalk"}, Tags: []string{"通知", "钉钉", "机器人"}, SortOrder: 20, Color: "#2563eb", Icon: "message-circle", Width: 220, Height: 72,
			ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"title":{"type":"string","title":"通知标题","minLength":1,"maxLength":160},"format":{"type":"string","title":"消息格式","enum":["text","markdown"],"enumLabels":["纯文本","Markdown 文本"],"default":"markdown"},"signed":{"type":"boolean","title":"启用加签","default":false},"accessToken":{"type":"string","title":"访问令牌","x-coinsphere-secret":true},"signingSecret":{"type":"string","title":"签名密钥","x-coinsphere-secret":true}},"required":["title","format","signed","accessToken"],"additionalProperties":false}`),
			UISchema:     json.RawMessage(`{"ui:order":["title","format","signed","accessToken","signingSecret"]}`),
		},
		{
			ExecutionPermissions: []string{"plugins.official.notification.execute"}, Type: "official.notification.smtp", Version: "1.0.0", Kind: sdk.NodeKindAction,
			Title: "邮件通知", Description: "通过 TLS SMTP 发送邮件通知", Category: "notification", Aliases: []string{"邮件", "SMTP"}, Tags: []string{"通知", "邮件", "SMTP"}, SortOrder: 30, Color: "#15803d", Icon: "mail", Width: 220, Height: 72,
			ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"title":{"type":"string","title":"邮件主题","minLength":1,"maxLength":160},"host":{"type":"string","title":"SMTP 公网域名","minLength":1,"maxLength":253},"port":{"type":"integer","title":"端口","minimum":1,"maximum":65535,"default":465},"security":{"type":"string","title":"连接安全","enum":["implicit_tls","starttls"],"enumLabels":["TLS","STARTTLS"],"default":"implicit_tls"},"username":{"type":"string","title":"用户名","minLength":1,"maxLength":320},"fromEmail":{"type":"string","title":"发件邮箱","format":"email","maxLength":320},"fromName":{"type":"string","title":"发件名称","maxLength":160},"recipients":{"type":"array","title":"收件人","minItems":1,"maxItems":100,"uniqueItems":true,"items":{"type":"string","format":"email","maxLength":320}},"password":{"type":"string","title":"密码","x-coinsphere-secret":true}},"required":["title","host","port","security","username","fromEmail","recipients","password"],"additionalProperties":false}`),
			UISchema:     json.RawMessage(`{"ui:order":["title","host","port","security","username","fromEmail","fromName","recipients","password"]}`),
		},
	}
	for index := range descriptors {
		descriptors[index].InputSchema = notificationInputSchema()
		descriptors[index].OutputSchema = notificationOutputSchema(strings.TrimPrefix(descriptors[index].Type, "official.notification."))
		descriptors[index].Pool = sdk.PoolStream
		descriptors[index].SideEffect = sdk.SideEffectNotification
		descriptors[index].State = sdk.StateStateless
		descriptors[index].RetrySafe = descriptors[index].Type == "official.notification.in_app"
		if err := registrar.Action(descriptors[index], notificationAction{
			runtime: n, channel: strings.TrimPrefix(descriptors[index].Type, "official.notification."),
		}); err != nil {
			return err
		}
	}
	return nil
}

func notificationInputSchema() json.RawMessage {
	return json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"subjectKey":{"type":"string","minLength":1,"maxLength":256},"message":{"type":"string","minLength":1,"maxLength":2000}},"required":["subjectKey","message"],"additionalProperties":false}`)
}

func notificationOutputSchema(channel string) json.RawMessage {
	required := []string{"deliveryId", "channel", "status", "deliveredAt"}
	if channel != "in_app" {
		required = append(required, "deliveryIds", "recipientCount")
	}
	raw, _ := json.Marshal(map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object",
		"properties": map[string]any{
			"deliveryId":     map[string]any{"type": "integer"},
			"deliveryIds":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
			"recipientCount": map[string]any{"type": "integer", "minimum": 1},
			"channel":        map[string]any{"type": "string", "const": channel},
			"status":         map[string]any{"type": "string", "const": "delivered"},
			"deliveredAt":    map[string]any{"type": "string", "format": "date-time"},
		},
		// Keep in_app@1.0.0 checkpoints valid while returning the expanded fields on new runs.
		"required":             required,
		"additionalProperties": false,
	})
	return raw
}

func (a notificationAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	var input ExternalDeliveryInput
	if json.Unmarshal(request.Input, &input) != nil {
		return sdk.ActionResult{}, errors.New("notification input is invalid")
	}
	input.SubjectKey, input.Message = strings.TrimSpace(input.SubjectKey), strings.TrimSpace(input.Message)
	workflowID, workflowErr := strconv.ParseInt(request.Revision.WorkflowID, 10, 64)
	revisionID, revisionErr := strconv.ParseInt(request.Revision.RevisionID, 10, 64)
	if workflowErr != nil || revisionErr != nil || input.SubjectKey == "" || input.Message == "" ||
		utf8.RuneCountInString(input.SubjectKey) > 256 || utf8.RuneCountInString(input.Message) > 2000 {
		return sdk.ActionResult{}, errors.New("notification identity or text is invalid")
	}
	if a.channel == "in_app" {
		return a.executeInApp(ctx, request, workflowID, revisionID, input)
	}
	return a.executeExternal(ctx, request, workflowID, revisionID, input)
}

func (a notificationAction) executeInApp(ctx context.Context, request sdk.ActionRequest, workflowID, revisionID int64, input ExternalDeliveryInput) (sdk.ActionResult, error) {
	var config struct {
		Title   string               `json:"title"`
		Targets []notificationTarget `json:"targets"`
	}
	if json.Unmarshal(request.Config, &config) != nil || !validNotificationTitle(config.Title) {
		return sdk.ActionResult{}, errors.New("in-app notification configuration is invalid")
	}
	if a.runtime.inbox == nil {
		return sdk.ActionResult{}, errors.New("inbox service unavailable")
	}
	raw, err := a.runtime.inbox.DeliverInbox(ctx, sdk.InboxRequest{Revision: request.Revision, OperationKey: request.OperationKey, NodeInstanceID: request.NodeInstanceID, Title: config.Title, SubjectKey: input.SubjectKey, Message: input.Message, Targets: config.Targets})
	return sdk.ActionResult{Output: raw}, err
}

func (a notificationAction) executeExternal(ctx context.Context, request sdk.ActionRequest, workflowID, revisionID int64, input ExternalDeliveryInput) (sdk.ActionResult, error) {
	title, err := notificationTitle(request.Config)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	delivery, err := BeginExternalDelivery(ctx, a.runtime.db, request, workflowID, revisionID, a.channel, title, input)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	if delivery.Status == "delivered" {
		return notificationResult([]Delivery{delivery}, externalRecipientCount(a.channel, request.Config)), nil
	}
	category, sendErr := a.runtime.sendExternal(ctx, a.channel, request, title, input.Message)
	if sendErr != nil {
		if updateErr := FinishExternalDelivery(ctx, a.runtime.db, delivery.ID, "failed", category); updateErr != nil {
			return sdk.ActionResult{}, updateErr
		}
		return sdk.ActionResult{}, &sdk.ExecutionError{Class: sdk.ErrorUnknownResult, Err: errors.New("notification provider result requires reconciliation")}
	}
	if err := FinishExternalDelivery(ctx, a.runtime.db, delivery.ID, "delivered", ""); err != nil {
		return sdk.ActionResult{}, err
	}
	if err := a.runtime.db.WithContext(ctx).First(&delivery, delivery.ID).Error; err != nil {
		return sdk.ActionResult{}, errors.New("load notification delivery failed")
	}
	return notificationResult([]Delivery{delivery}, externalRecipientCount(a.channel, request.Config)), nil
}

func BeginExternalDelivery(ctx context.Context, database *gorm.DB, request sdk.ActionRequest, workflowID, revisionID int64, channel, title string, input ExternalDeliveryInput) (Delivery, error) {
	now := time.Now().UTC()
	delivery := Delivery{
		OperationKey: request.OperationKey, WorkflowID: workflowID, RevisionID: revisionID,
		NodeInstanceID: request.NodeInstanceID, Channel: channel, SubjectKey: input.SubjectKey,
		Title: title, Message: input.Message, Status: "pending", AttemptCount: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&delivery)
		if result.Error != nil {
			return errors.New("persist notification delivery failed")
		}
		if result.RowsAffected == 0 {
			if err := tx.Where("operation_key = ?", request.OperationKey).Take(&delivery).Error; err != nil {
				return errors.New("load notification delivery failed")
			}
			if delivery.Status != "delivered" {
				return &sdk.ExecutionError{Class: sdk.ErrorUnknownResult, Err: errors.New("delivery result requires reconciliation")}
			}
		}
		return nil
	}); err != nil {
		return Delivery{}, err
	}
	return delivery, nil
}

func FinishExternalDelivery(ctx context.Context, database *gorm.DB, deliveryID int64, status, category string) error {
	now := time.Now().UTC()
	updates := map[string]any{"status": status, "updated_at": now}
	if status == "delivered" {
		updates["delivered_at"] = now
		updates["last_error_category"] = nil
	} else {
		updates["delivered_at"] = nil
		updates["last_error_category"] = category
	}
	if err := database.WithContext(ctx).Model(&Delivery{}).Where("id = ?", deliveryID).Updates(updates).Error; err != nil {
		return errors.New("finish notification delivery failed")
	}
	return nil
}

func (n *notificationRuntime) sendExternal(ctx context.Context, channel string, request sdk.ActionRequest, title, message string) (string, error) {
	sendCtx, cancel := context.WithTimeout(ctx, notificationTimeout)
	defer cancel()
	switch channel {
	case "dingtalk":
		return n.sendDingTalk(sendCtx, request, title, message)
	case "smtp":
		return n.sendSMTP(sendCtx, request, title, message)
	default:
		return "configuration", errors.New("unsupported notification channel")
	}
}

func notificationTitle(raw json.RawMessage) (string, error) {
	var config struct {
		Title string `json:"title"`
	}
	if json.Unmarshal(raw, &config) != nil || !validNotificationTitle(config.Title) {
		return "", errors.New("notification configuration is invalid")
	}
	return strings.TrimSpace(config.Title), nil
}

func validNotificationTitle(title string) bool {
	title = strings.TrimSpace(title)
	return title != "" && !strings.ContainsAny(title, "\r\n") && utf8.RuneCountInString(title) <= 160
}

func externalRecipientCount(channel string, raw json.RawMessage) int {
	if channel != "smtp" {
		return 1
	}
	var config struct {
		Recipients []string `json:"recipients"`
	}
	if json.Unmarshal(raw, &config) != nil {
		return 1
	}
	recipients := map[string]struct{}{}
	for _, recipient := range config.Recipients {
		if recipient = strings.TrimSpace(recipient); recipient != "" {
			recipients[recipient] = struct{}{}
		}
	}
	if len(recipients) == 0 {
		return 1
	}
	return len(recipients)
}

func notificationResult(deliveries []Delivery, recipientCount int) sdk.ActionResult {
	ids := make([]int64, len(deliveries))
	deliveredAt := deliveries[0].CreatedAt
	for index := range deliveries {
		ids[index] = deliveries[index].ID
		if deliveries[index].DeliveredAt != nil && deliveries[index].DeliveredAt.After(deliveredAt) {
			deliveredAt = *deliveries[index].DeliveredAt
		}
	}
	return sdk.ActionResult{Output: mustMarshal(map[string]any{
		"deliveryId": ids[0], "deliveryIds": ids, "recipientCount": recipientCount,
		"channel": deliveries[0].Channel, "status": "delivered",
		"deliveredAt": deliveredAt.UTC().Format(time.RFC3339Nano),
	})}
}

var _ sdk.ActionHandler = notificationAction{}

func mustMarshal(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}
