package sdk

import (
	"encoding/json"

	"gorm.io/gorm"
)

type WorkflowValidationContext struct {
	Graph json.RawMessage
	Nodes map[string]NodeDescriptor
}

type WorkflowValidator interface {
	ValidateWorkflow(WorkflowValidationContext) error
}

type WorkflowValidatorFunc func(WorkflowValidationContext) error

func (f WorkflowValidatorFunc) ValidateWorkflow(input WorkflowValidationContext) error {
	return f(input)
}

type TemplateDescriptor struct {
	Key         string
	Name        string
	Description string
	Mode        string
	Graph       json.RawMessage
}

type GormPluginStores struct{ Database *gorm.DB }

type gormPluginStore struct {
	pluginID string
	database *gorm.DB
}

func (s GormPluginStores) ForPlugin(pluginID string) PluginStore {
	return gormPluginStore{pluginID: pluginID, database: s.Database}
}

func (s gormPluginStore) PluginID() string { return s.pluginID }
func (s gormPluginStore) DB() *gorm.DB     { return s.database }
