package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"coinsphere/backend/internal/db"
	"gorm.io/gorm"
)

const (
	assistantHistoryLimit = 20
	assistantInputLimit   = 8000
)

type AssistantSessionCreatePayload struct {
	ModelID int64  `json:"modelId"`
	Title   string `json:"title"`
}

type AssistantStreamPayload struct {
	Text string `json:"text"`
}

type AssistantSessionView struct {
	ID            int64  `json:"id"`
	ModelID       int64  `json:"modelId"`
	ModelName     string `json:"modelName"`
	Title         string `json:"title"`
	MessageCount  int64  `json:"messageCount"`
	LatestPreview string `json:"latestPreview"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
	LastMessageAt string `json:"lastMessageAt"`
}

type AssistantMessageView struct {
	ID        int64  `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"createdAt"`
}

type assistantWorkflowCreateSummary struct {
	WorkflowID     int64    `json:"workflowId"`
	Status         string   `json:"status"`
	EditURL        string   `json:"editUrl"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	NodeCount      int      `json:"nodeCount"`
	EdgeCount      int      `json:"edgeCount"`
	NodeTypes      []string `json:"nodeTypes"`
	MissingSecrets []string `json:"missingSecrets"`
	graph          validatedWorkflowGraph
}

type AssistantStreamEvent struct {
	Name string
	Data any
}

func (a *App) CreateAssistantSession(ctx context.Context, payload AssistantSessionCreatePayload, principal *Principal) (AssistantSessionView, error) {
	if principal == nil || principal.User == nil {
		return AssistantSessionView{}, ErrPermission
	}
	modelID := payload.ModelID
	if modelID <= 0 {
		var model db.AIModelConfig
		if err := a.DB.WithContext(ctx).Where("is_enabled = ?", true).Order("priority, id").First(&model).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return AssistantSessionView{}, fmt.Errorf("%w: no enabled AI model", ErrConflict)
			}
			return AssistantSessionView{}, errors.New("load default AI model failed")
		}
		modelID = model.ID
	}
	if _, err := a.loadAssistantModel(ctx, modelID, true); err != nil {
		return AssistantSessionView{}, err
	}
	title := strings.TrimSpace(payload.Title)
	if title == "" {
		title = "新会话"
	}
	if utf8.RuneCountInString(title) > 160 {
		return AssistantSessionView{}, errors.New("title must not exceed 160 characters")
	}
	now := time.Now().UTC()
	session := db.AssistantSession{
		UserID: principal.User.ID, ModelConfigID: modelID, Title: title,
		CreatedAt: now, UpdatedAt: now, LastMessageAt: now,
	}
	if err := a.DB.WithContext(ctx).Create(&session).Error; err != nil {
		return AssistantSessionView{}, errors.New("create assistant session failed")
	}
	return a.assistantSessionView(ctx, session)
}

func (a *App) ListAssistantSessions(ctx context.Context, principal *Principal) ([]AssistantSessionView, error) {
	if principal == nil || principal.User == nil {
		return nil, ErrPermission
	}
	var sessions []db.AssistantSession
	if err := a.DB.WithContext(ctx).Where("user_id = ?", principal.User.ID).
		Order("last_message_at DESC, id DESC").Limit(100).Find(&sessions).Error; err != nil {
		return nil, errors.New("list assistant sessions failed")
	}
	result := make([]AssistantSessionView, 0, len(sessions))
	for _, session := range sessions {
		view, err := a.assistantSessionView(ctx, session)
		if err != nil {
			return nil, err
		}
		result = append(result, view)
	}
	return result, nil
}

func (a *App) GetAssistantSession(ctx context.Context, sessionID int64, principal *Principal) (AssistantSessionView, error) {
	session, err := a.requireAssistantSession(ctx, sessionID, principal)
	if err != nil {
		return AssistantSessionView{}, err
	}
	return a.assistantSessionView(ctx, session)
}

func (a *App) DeleteAssistantSession(ctx context.Context, sessionID int64, principal *Principal) error {
	session, err := a.requireAssistantSession(ctx, sessionID, principal)
	if err != nil {
		return err
	}
	if err := a.DB.WithContext(ctx).Delete(&session).Error; err != nil {
		return errors.New("delete assistant session failed")
	}
	return nil
}

func (a *App) ListAssistantMessages(ctx context.Context, sessionID int64, principal *Principal) ([]AssistantMessageView, error) {
	if _, err := a.requireAssistantSession(ctx, sessionID, principal); err != nil {
		return nil, err
	}
	var messages []db.AssistantMessage
	if err := a.DB.WithContext(ctx).Where("session_id = ?", sessionID).Order("id").Limit(500).Find(&messages).Error; err != nil {
		return nil, errors.New("list assistant messages failed")
	}
	result := make([]AssistantMessageView, len(messages))
	for index := range messages {
		result[index] = assistantMessageView(messages[index])
	}
	return result, nil
}

func (a *App) StreamAssistantSession(ctx context.Context, sessionID int64, payload AssistantStreamPayload, principal *Principal, emit func(AssistantStreamEvent) error) error {
	session, err := a.requireAssistantSession(ctx, sessionID, principal)
	if err != nil {
		return err
	}
	text := strings.TrimSpace(payload.Text)
	if text == "" || utf8.RuneCountInString(text) > assistantInputLimit {
		return fmt.Errorf("message must contain 1 to %d characters", assistantInputLimit)
	}
	runtime, err := a.loadAssistantModel(ctx, session.ModelConfigID, true)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	userMessage := db.AssistantMessage{
		SessionID: session.ID, Role: "user", Content: text, MetadataJSON: `{}`, CreatedAt: now,
	}
	if err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&userMessage).Error; err != nil {
			return errors.New("create assistant user message failed")
		}
		updates := map[string]any{"updated_at": now, "last_message_at": now}
		if session.Title == "新会话" {
			updates["title"] = truncateAssistantText(text, 40)
			session.Title = updates["title"].(string)
		}
		return tx.Model(&db.AssistantSession{}).Where("id = ?", session.ID).Updates(updates).Error
	}); err != nil {
		return err
	}
	if err := emit(AssistantStreamEvent{Name: "user", Data: map[string]any{"message": assistantMessageView(userMessage)}}); err != nil {
		return err
	}

	history, err := a.loadAssistantHistory(ctx, session.ID)
	if err != nil {
		return err
	}
	run, err := a.runAssistantModel(ctx, runtime, history, principal, emit)
	if err != nil {
		return err
	}
	if strings.TrimSpace(run.Content) == "" {
		if run.Workflow == nil {
			return errors.New("AI model returned an empty response")
		}
		run.Content = fmt.Sprintf("工作流已创建：%s（ID %d），当前状态为 inactive。编辑地址：%s", run.Workflow.Name, run.Workflow.WorkflowID, run.Workflow.EditURL)
	}
	assistantMessage := db.AssistantMessage{
		SessionID: session.ID, Role: "assistant", Content: run.Content,
		MetadataJSON: `{}`, CreatedAt: time.Now().UTC(),
	}
	if err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&assistantMessage).Error; err != nil {
			return errors.New("create assistant response failed")
		}
		return tx.Model(&db.AssistantSession{}).Where("id = ?", session.ID).Updates(map[string]any{
			"updated_at": assistantMessage.CreatedAt, "last_message_at": assistantMessage.CreatedAt,
		}).Error
	}); err != nil {
		return err
	}
	session.UpdatedAt = assistantMessage.CreatedAt
	session.LastMessageAt = assistantMessage.CreatedAt
	view := assistantMessageView(assistantMessage)
	sessionView, err := a.assistantSessionView(ctx, session)
	if err != nil {
		return err
	}
	return emit(AssistantStreamEvent{Name: "done", Data: map[string]any{"message": view, "session": sessionView}})
}
func (a *App) requireAssistantSession(ctx context.Context, sessionID int64, principal *Principal) (db.AssistantSession, error) {
	if principal == nil || principal.User == nil {
		return db.AssistantSession{}, ErrPermission
	}
	var session db.AssistantSession
	if err := a.DB.WithContext(ctx).Where("id = ? AND user_id = ?", sessionID, principal.User.ID).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return db.AssistantSession{}, fmt.Errorf("%w: assistant session", ErrNotFound)
		}
		return db.AssistantSession{}, errors.New("load assistant session failed")
	}
	return session, nil
}

func (a *App) assistantSessionView(ctx context.Context, session db.AssistantSession) (AssistantSessionView, error) {
	var model db.AIModelConfig
	if err := a.DB.WithContext(ctx).Select("id, display_name").First(&model, session.ModelConfigID).Error; err != nil {
		return AssistantSessionView{}, errors.New("load assistant session model failed")
	}
	var count int64
	if err := a.DB.WithContext(ctx).Model(&db.AssistantMessage{}).Where("session_id = ?", session.ID).Count(&count).Error; err != nil {
		return AssistantSessionView{}, errors.New("count assistant messages failed")
	}
	var latest db.AssistantMessage
	preview := ""
	if err := a.DB.WithContext(ctx).Where("session_id = ?", session.ID).Order("id DESC").First(&latest).Error; err == nil {
		preview = truncateAssistantText(latest.Content, 120)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return AssistantSessionView{}, errors.New("load assistant message preview failed")
	}
	return AssistantSessionView{
		ID: session.ID, ModelID: model.ID, ModelName: model.DisplayName, Title: session.Title,
		MessageCount: count, LatestPreview: preview, CreatedAt: formatWorkflowTime(session.CreatedAt),
		UpdatedAt: formatWorkflowTime(session.UpdatedAt), LastMessageAt: formatWorkflowTime(session.LastMessageAt),
	}, nil
}

func (a *App) loadAssistantHistory(ctx context.Context, sessionID int64) ([]assistantChatMessage, error) {
	var messages []db.AssistantMessage
	if err := a.DB.WithContext(ctx).Where("session_id = ?", sessionID).
		Order("id DESC").Limit(assistantHistoryLimit).Find(&messages).Error; err != nil {
		return nil, errors.New("load assistant history failed")
	}
	result := make([]assistantChatMessage, 0, len(messages))
	for index := len(messages) - 1; index >= 0; index-- {
		content := truncateAssistantText(messages[index].Content, 16000)
		if content != "" {
			result = append(result, assistantChatMessage{Role: messages[index].Role, Content: content})
		}
	}
	return result, nil
}

func assistantMessageView(message db.AssistantMessage) AssistantMessageView {
	return AssistantMessageView{ID: message.ID, Role: message.Role, Content: message.Content, CreatedAt: formatWorkflowTime(message.CreatedAt)}
}

func (a *App) workflowCreateSummary(name, description string, raw json.RawMessage) (*assistantWorkflowCreateSummary, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || utf8.RuneCountInString(name) > 120 || utf8.RuneCountInString(description) > 500 {
		return nil, errors.New("workflow name or description is invalid")
	}
	validated, err := a.validateWorkflowGraph(raw)
	if err != nil {
		return nil, err
	}
	var graph workflowGraph
	if json.Unmarshal([]byte(validated.graphJSON), &graph) != nil {
		return nil, errors.New("decode normalized workflow graph failed")
	}
	nodeTypes := make([]string, 0, len(validated.nodeTypes))
	seenTypes := map[string]bool{}
	for _, nodeType := range validated.nodeTypes {
		if !seenTypes[nodeType] {
			seenTypes[nodeType] = true
			nodeTypes = append(nodeTypes, nodeType)
		}
	}
	sort.Strings(nodeTypes)
	missingSecrets := make([]string, 0, len(validated.requiredSecrets))
	for key := range validated.requiredSecrets {
		missingSecrets = append(missingSecrets, key.nodeInstanceID+"."+key.field)
	}
	sort.Strings(missingSecrets)
	return &assistantWorkflowCreateSummary{
		Name: name, Description: description, NodeCount: len(graph.Nodes), EdgeCount: len(graph.Edges),
		NodeTypes: nodeTypes, MissingSecrets: missingSecrets, graph: validated,
	}, nil
}

func (a *App) createAssistantWorkflow(ctx context.Context, name, description string, raw json.RawMessage, principal *Principal) (*assistantWorkflowCreateSummary, error) {
	if principal == nil || principal.User == nil || principal.User.ID <= 0 {
		return nil, ErrPermission
	}
	summary, err := a.workflowCreateSummary(name, description, raw)
	if err != nil {
		return nil, err
	}
	var workflow db.Workflow
	err = a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		workflow, err = createWorkflowRecord(tx, summary.Name, summary.Description, nil, summary.graph, principal.User.ID, time.Now().UTC())
		return err
	})
	if err != nil {
		return nil, err
	}
	summary.WorkflowID = workflow.ID
	summary.Status = WorkflowStatusInactive
	summary.EditURL = fmt.Sprintf("/scheduler/workflow/%d/edit", workflow.ID)
	return summary, nil
}

func truncateAssistantText(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}
