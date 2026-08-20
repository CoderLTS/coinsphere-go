package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrWorkflowNodeTemplateMissing = errors.New("workflow node template was not found")

type WorkflowNodeTemplatePayload struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	Icon          string `json:"icon"`
	BaseNodeType  string `json:"baseNodeType"`
	DefaultConfig M      `json:"defaultConfig"`
	IsEnabled     *bool  `json:"isEnabled"`
}

type WorkflowNodeTemplateView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Icon          string `json:"icon"`
	BaseNodeType  string `json:"baseNodeType"`
	BaseNodeLabel string `json:"baseNodeLabel"`
	DefaultConfig M      `json:"defaultConfig"`
	IsEnabled     bool   `json:"isEnabled"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

type validatedWorkflowNodeTemplate struct {
	Name, Description, Icon, BaseNodeType, DefaultConfigJSON string
	IsEnabled                                                bool
}

func validateWorkflowNodeTemplate(payload WorkflowNodeTemplatePayload, existing *db.WorkflowNodeTemplate) (validatedWorkflowNodeTemplate, error) {
	name := strings.TrimSpace(payload.Name)
	if name == "" || len(name) > 120 {
		return validatedWorkflowNodeTemplate{}, bizErr("节点模板名称长度必须为 1 到 120 个字符")
	}
	description := strings.TrimSpace(payload.Description)
	if len(description) > 500 {
		return validatedWorkflowNodeTemplate{}, bizErr("节点模板说明不能超过 500 个字符")
	}
	icon := strings.TrimSpace(payload.Icon)
	if icon == "" {
		icon = "ri:node-tree"
	}
	if len(icon) > 120 {
		return validatedWorkflowNodeTemplate{}, bizErr("节点模板图标不能超过 120 个字符")
	}
	baseType := strings.TrimSpace(payload.BaseNodeType)
	if _, err := getNodeDefinition(baseType); err != nil {
		return validatedWorkflowNodeTemplate{}, bizErr("请选择可用的内置节点")
	}
	config := payload.DefaultConfig
	if config == nil {
		config = M{}
	}
	raw, err := json.Marshal(config)
	if err != nil || len(raw) > 65536 {
		return validatedWorkflowNodeTemplate{}, bizErr("节点模板默认配置无效")
	}
	enabled := true
	if existing != nil {
		enabled = existing.IsEnabled
	}
	if payload.IsEnabled != nil {
		enabled = *payload.IsEnabled
	}
	return validatedWorkflowNodeTemplate{
		Name: name, Description: description, Icon: icon, BaseNodeType: baseType,
		DefaultConfigJSON: string(raw), IsEnabled: enabled,
	}, nil
}

func (a *App) ListWorkflowNodeTemplates(ctx context.Context, ownerUserID int64) ([]WorkflowNodeTemplateView, error) {
	var rows []db.WorkflowNodeTemplate
	if err := a.dbWithContext(ctx).Where("owner_user_id = ?", ownerUserID).
		Order("updated_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]WorkflowNodeTemplateView, 0, len(rows))
	for _, row := range rows {
		items = append(items, serializeWorkflowNodeTemplate(row))
	}
	return items, nil
}

func (a *App) CreateWorkflowNodeTemplate(ctx context.Context, ownerUserID int64, payload WorkflowNodeTemplatePayload) (WorkflowNodeTemplateView, error) {
	validated, err := validateWorkflowNodeTemplate(payload, nil)
	if err != nil {
		return WorkflowNodeTemplateView{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return WorkflowNodeTemplateView{}, err
	}
	now := time.Now().UTC()
	row := db.WorkflowNodeTemplate{
		ID: id, OwnerUserID: ownerUserID, Name: validated.Name, Description: validated.Description,
		Icon: validated.Icon, BaseNodeType: validated.BaseNodeType, DefaultConfigJSON: validated.DefaultConfigJSON,
		IsEnabled: validated.IsEnabled, CreatedAt: now, UpdatedAt: now,
	}
	if err := a.dbWithContext(ctx).Create(&row).Error; err != nil {
		return WorkflowNodeTemplateView{}, err
	}
	return serializeWorkflowNodeTemplate(row), nil
}

func (a *App) UpdateWorkflowNodeTemplate(ctx context.Context, ownerUserID int64, rawID string, payload WorkflowNodeTemplatePayload) (WorkflowNodeTemplateView, error) {
	id, err := parseRequiredUUIDv7(rawID, "templateId")
	if err != nil {
		return WorkflowNodeTemplateView{}, bizErr("节点模板不存在")
	}
	var row db.WorkflowNodeTemplate
	if err := a.dbWithContext(ctx).Where("id = ? AND owner_user_id = ?", id, ownerUserID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return WorkflowNodeTemplateView{}, ErrWorkflowNodeTemplateMissing
		}
		return WorkflowNodeTemplateView{}, err
	}
	validated, err := validateWorkflowNodeTemplate(payload, &row)
	if err != nil {
		return WorkflowNodeTemplateView{}, err
	}
	updates := map[string]any{
		"name": validated.Name, "description": validated.Description, "icon": validated.Icon,
		"base_node_type": validated.BaseNodeType, "default_config_json": validated.DefaultConfigJSON,
		"is_enabled": validated.IsEnabled, "updated_at": time.Now().UTC(),
	}
	if err := a.dbWithContext(ctx).Model(&row).Updates(updates).Error; err != nil {
		return WorkflowNodeTemplateView{}, err
	}
	if err := a.dbWithContext(ctx).Where("id = ?", id).Take(&row).Error; err != nil {
		return WorkflowNodeTemplateView{}, err
	}
	return serializeWorkflowNodeTemplate(row), nil
}

func (a *App) DeleteWorkflowNodeTemplate(ctx context.Context, ownerUserID int64, rawID string) error {
	id, err := parseRequiredUUIDv7(rawID, "templateId")
	if err != nil {
		return ErrWorkflowNodeTemplateMissing
	}
	result := a.dbWithContext(ctx).Where("id = ? AND owner_user_id = ?", id, ownerUserID).Delete(&db.WorkflowNodeTemplate{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrWorkflowNodeTemplateMissing
	}
	return nil
}

func serializeWorkflowNodeTemplate(row db.WorkflowNodeTemplate) WorkflowNodeTemplateView {
	config := M{}
	_ = json.Unmarshal([]byte(row.DefaultConfigJSON), &config)
	label := row.BaseNodeType
	if definition, err := getNodeDefinition(row.BaseNodeType); err == nil {
		label = definition.Label
	}
	return WorkflowNodeTemplateView{
		ID: row.ID.String(), Name: row.Name, Description: row.Description, Icon: row.Icon,
		BaseNodeType: row.BaseNodeType, BaseNodeLabel: label, DefaultConfig: config,
		IsEnabled: row.IsEnabled, CreatedAt: formatUTC(row.CreatedAt), UpdatedAt: formatUTC(row.UpdatedAt),
	}
}
