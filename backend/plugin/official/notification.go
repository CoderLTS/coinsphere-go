package official

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const notificationPluginID = "official.notification"

type notificationDelivery struct {
	ID             int64
	OperationKey   string
	WorkflowID     int64
	RevisionID     int64
	NodeInstanceID string
	Channel        string
	SubjectKey     string
	Title          string
	Message        string
	Status         string
	AttemptCount   int
	DeliveredAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (notificationDelivery) TableName() string { return "plugin_notification.deliveries" }

type notificationRuntime struct{ db *gorm.DB }
type notificationInAppAction struct{ runtime *notificationRuntime }

func RegisterNotification(registry *sdk.Registry, database *gorm.DB) error {
	runtime := &notificationRuntime{db: database}
	return registry.RegisterPlugin(sdk.PluginDescriptor{
		ID: notificationPluginID, Name: "CoinSphere Notification", Version: "1.0.0",
		Contributes: []string{"nodes", "apiRoutes", "pages"},
	}, runtime.register)
}

func (n *notificationRuntime) register(registrar sdk.Registrar) error {
	if err := registrar.Page(sdk.PageDescriptor{
		PageKey: "deliveries", Title: "通知投递", Icon: "ri:notification-3-line", KeepAlive: true,
	}); err != nil {
		return err
	}
	if err := registrar.Action(sdk.NodeDescriptor{
		Type: "official.notification.in_app", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"title":{"type":"string","title":"Title","minLength":1,"maxLength":160}},"required":["title"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["title"]}`),
		InputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"subjectKey":{"type":"string","minLength":1,"maxLength":256},"message":{"type":"string","minLength":1,"maxLength":2000}},"required":["subjectKey","message"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"deliveryId":{"type":"integer"},"channel":{"type":"string","const":"in_app"},"status":{"type":"string","enum":["delivered","failed"]},"deliveredAt":{"type":"string","format":"date-time"}},"required":["deliveryId","channel","status","deliveredAt"],"additionalProperties":false}`),
		Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectNotification, State: sdk.StateStateless,
	}, notificationInAppAction{runtime: n}); err != nil {
		return err
	}
	return registrar.Route(
		sdk.RouteDescriptor{Method: "GET", Pattern: "/deliveries", Scope: sdk.ScopeSystem},
		n.handleDeliveries,
	)
}

func (a notificationInAppAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	var config struct {
		Title string `json:"title"`
	}
	var input struct{ SubjectKey, Message string }
	if json.Unmarshal(request.Config, &config) != nil || json.Unmarshal(request.Input, &input) != nil {
		return sdk.ActionResult{}, errors.New("notification input is invalid")
	}
	config.Title, input.SubjectKey, input.Message = strings.TrimSpace(config.Title), strings.TrimSpace(input.SubjectKey), strings.TrimSpace(input.Message)
	workflowID, workflowErr := quantInt64(request.Revision.WorkflowID)
	revisionID, revisionErr := quantInt64(request.Revision.RevisionID)
	if workflowErr != nil || revisionErr != nil || config.Title == "" || input.SubjectKey == "" || input.Message == "" ||
		utf8.RuneCountInString(config.Title) > 160 || utf8.RuneCountInString(input.SubjectKey) > 256 || utf8.RuneCountInString(input.Message) > 2000 {
		return sdk.ActionResult{}, errors.New("notification identity or text is invalid")
	}
	if existing, ok, err := a.runtime.loadDelivery(ctx, request.OperationKey); err != nil {
		return sdk.ActionResult{}, err
	} else if ok {
		return notificationResult(existing), nil
	}
	now := time.Now().UTC()
	delivery := notificationDelivery{
		OperationKey: request.OperationKey, WorkflowID: workflowID, RevisionID: revisionID,
		NodeInstanceID: request.NodeInstanceID, Channel: "in_app", SubjectKey: input.SubjectKey,
		Title: config.Title, Message: input.Message, Status: "delivered", AttemptCount: 1,
		DeliveredAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err := a.runtime.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&delivery).Error; err != nil {
		return sdk.ActionResult{}, errors.New("persist notification delivery failed")
	}
	stored, ok, err := a.runtime.loadDelivery(ctx, request.OperationKey)
	if err != nil || !ok {
		return sdk.ActionResult{}, errors.New("load notification delivery failed")
	}
	return notificationResult(stored), nil
}

func (n *notificationRuntime) loadDelivery(ctx context.Context, operationKey string) (notificationDelivery, bool, error) {
	var delivery notificationDelivery
	if err := n.db.WithContext(ctx).Where("operation_key = ?", operationKey).First(&delivery).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return notificationDelivery{}, false, nil
		}
		return notificationDelivery{}, false, errors.New("load notification delivery failed")
	}
	return delivery, true, nil
}

func notificationResult(delivery notificationDelivery) sdk.ActionResult {
	deliveredAt := delivery.CreatedAt
	if delivery.DeliveredAt != nil {
		deliveredAt = *delivery.DeliveredAt
	}
	return sdk.ActionResult{Output: mustMarshal(map[string]any{
		"deliveryId": delivery.ID, "channel": delivery.Channel, "status": delivery.Status,
		"deliveredAt": deliveredAt.UTC().Format(time.RFC3339Nano),
	})}
}

func (n *notificationRuntime) handleDeliveries(w http.ResponseWriter, r *http.Request, scope sdk.RouteScope) {
	value, ok := scope.(sdk.SystemScope)
	if !ok || value.PluginID != notificationPluginID || !quantQueryKeys(r, "status", "limit") {
		writeQuantProblem(w, http.StatusBadRequest, "invalid notification delivery query")
		return
	}
	n.listDeliveries(w, r, strings.TrimSpace(r.URL.Query().Get("status")))
}

func (n *notificationRuntime) listDeliveries(w http.ResponseWriter, r *http.Request, status string) {
	limit, err := quantQueryLimit(r, 100, 200)
	if err != nil {
		writeQuantProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	query := n.db.WithContext(r.Context()).Order("created_at DESC, id DESC").Limit(limit)
	if status != "" {
		if status != "delivered" && status != "failed" {
			writeQuantProblem(w, http.StatusBadRequest, "notification status is invalid")
			return
		}
		query = query.Where("status = ?", status)
	}
	var deliveries []notificationDelivery
	if err := query.Find(&deliveries).Error; err != nil {
		writeQuantProblem(w, http.StatusInternalServerError, "list notification deliveries failed")
		return
	}
	items := make([]map[string]any, len(deliveries))
	for index := range deliveries {
		items[index] = map[string]any{
			"id": deliveries[index].ID, "channel": deliveries[index].Channel,
			"subjectKey": deliveries[index].SubjectKey, "title": deliveries[index].Title,
			"message": deliveries[index].Message, "status": deliveries[index].Status,
			"attemptCount": deliveries[index].AttemptCount,
			"createdAt":    deliveries[index].CreatedAt.UTC().Format(time.RFC3339Nano),
		}
	}
	writeQuantOK(w, map[string]any{"items": items})
}

var _ sdk.ActionHandler = notificationInAppAction{}
