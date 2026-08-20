package service

import (
	"testing"

	"coinsphere/backend/internal/db"
)

func TestWorkflowNodeTemplateOnlyReferencesBuiltinExecutor(t *testing.T) {
	enabled := true
	valid, err := validateWorkflowNodeTemplate(WorkflowNodeTemplatePayload{
		Name: "价格通知", BaseNodeType: "notify", DefaultConfig: M{"titleTemplate": "价格提醒"}, IsEnabled: &enabled,
	}, nil)
	if err != nil || valid.BaseNodeType != "notify" || valid.DefaultConfigJSON == "" {
		t.Fatalf("valid template = %#v, err=%v", valid, err)
	}
	if _, err := validateWorkflowNodeTemplate(WorkflowNodeTemplatePayload{
		Name: "自定义代码", BaseNodeType: "python.exec", DefaultConfig: M{},
	}, nil); err == nil {
		t.Fatal("unknown executor was accepted")
	}
	disabled := false
	updated, err := validateWorkflowNodeTemplate(WorkflowNodeTemplatePayload{
		Name: "停用模板", BaseNodeType: "end", IsEnabled: &disabled,
	}, &db.WorkflowNodeTemplate{IsEnabled: true})
	if err != nil || updated.IsEnabled {
		t.Fatalf("disabled template = %#v, err=%v", updated, err)
	}
}
