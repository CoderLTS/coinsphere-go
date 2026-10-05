package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"gorm.io/gorm"
)

func (c *registrationCollector) RunPanel(desc RunPanelDescriptor) error {
	if !contributionKeyPattern.MatchString(desc.PanelKey) || desc.Title == "" || len(desc.NodeTypes) == 0 {
		return errors.New("run panel requires key, title and node types")
	}
	if err := validateComponentEntry(desc.ComponentEntry); err != nil {
		return err
	}
	key := c.plugin.ID + "/" + desc.PanelKey
	if _, exists := c.runPanels[key]; exists {
		return fmt.Errorf("duplicate run panel %s", key)
	}
	for _, typ := range desc.NodeTypes {
		found := false
		for _, node := range c.nodes {
			if node.desc.Type == typ {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("run panel references unowned node %s", typ)
		}
	}
	c.runPanels[key] = desc
	c.markUsed("runPanels")
	return nil
}
func (c *registrationCollector) Cleanup(handler CleanupHandler) error {
	if handler == nil || c.cleanup != nil {
		return errors.New("cleanup requires exactly one handler")
	}
	c.cleanup = handler
	c.markUsed("cleanup")
	return nil
}
func (c *registrationCollector) Ingress(nodeType string, handler IngressHandler) error {
	if handler == nil || c.ingresses[nodeType] != nil {
		return errors.New("ingress requires a unique handler")
	}
	found := false
	for _, n := range c.nodes {
		if n.desc.Type == nodeType && n.trigger != nil {
			found = true
		}
	}
	if !found {
		return errors.New("ingress must reference an owned trigger")
	}
	c.ingresses[nodeType] = handler
	c.markUsed("ingress")
	return nil
}
func validateComponentEntry(entry string) error {
	clean := path.Clean(entry)
	if entry == "" || strings.Contains(entry, `\`) || path.IsAbs(clean) || windowsAbsolutePathPattern.MatchString(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return errors.New("component entry must stay inside plugin root")
	}
	return nil
}
func (c *registrationCollector) validatePermissions() error {
	declared := map[string]bool{}
	for _, permission := range c.plugin.Permissions {
		if !strings.HasPrefix(permission.Code, "plugins."+c.plugin.ID+".") || !contributionKeyPattern.MatchString(permission.Code) || permission.Title == "" || declared[permission.Code] {
			return errors.New("invalid or duplicate plugin permission")
		}
		declared[permission.Code] = true
	}
	check := func(code string) error {
		if !declared[code] && !CoreScopePermissions[code] {
			return fmt.Errorf("undeclared permission %q", code)
		}
		return nil
	}
	for _, n := range c.nodes {
		if len(n.desc.ExecutionPermissions) == 0 {
			return fmt.Errorf("node %s must declare execution permissions", n.desc.Type)
		}
		for _, code := range n.desc.ExecutionPermissions {
			if err := check(code); err != nil {
				return err
			}
		}
	}
	for _, p := range c.pages {
		if err := check(p.PermissionCode); err != nil {
			return err
		}
	}
	for _, p := range c.resultPages {
		if err := check(p.PermissionCode); err != nil {
			return err
		}
		for _, action := range p.Actions {
			if err := check(p.ActionPermissions[action]); err != nil {
				return err
			}
		}
		for action := range p.ActionPermissions {
			if !contains(p.Actions, action) {
				return fmt.Errorf("undeclared result action %s", action)
			}
		}
		if p.ValidateScope == nil || p.Resources == nil {
			return errors.New("result page must validate its fixed resource scope")
		}
	}
	for _, r := range c.routes {
		if err := check(r.desc.PermissionCode); err != nil {
			return err
		}
		if r.desc.Scope == ScopeResult && r.desc.Action != "" {
			found := false
			for _, p := range c.resultPages {
				if p.ActionPermissions[r.desc.Action] == r.desc.PermissionCode {
					found = true
				}
			}
			if !found {
				return errors.New("result route action must be declared by a result page")
			}
		}
	}
	return nil
}
func contains(items []string, item string) bool {
	for _, v := range items {
		if v == item {
			return true
		}
	}
	return false
}

var CoreScopePermissions = map[string]bool{"workflows.read": true, "workflows.update": true, "workflows.run": true, "human_tasks.decide": true, "result_views.read": true, "result_views.approve": true, "result_views.reject": true, "result_views.retry": true, "result_views.cancel": true, "result_views.pause": true, "result_views.export": true}

func (r *Registry) RunPanels(pluginID string) []RunPanelDescriptor {
	out := []RunPanelDescriptor{}
	for key, panel := range r.runPanels {
		if strings.HasPrefix(key, pluginID+"/") {
			out = append(out, panel)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PanelKey < out[j].PanelKey })
	return out
}
func (r *Registry) Ingress(nodeType string) (IngressHandler, bool) {
	handler, ok := r.ingresses[nodeType]
	return handler, ok
}
func (r *Registry) Cleanup(ctx context.Context, tx *gorm.DB, request CleanupRequest) error {
	for _, plugin := range r.Plugins() {
		if handler := r.cleanups[plugin.ID]; handler != nil {
			if err := handler(ctx, tx, request); err != nil {
				return err
			}
		}
	}
	return nil
}

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

func ScopeWorkflows(query *gorm.DB, scope RouteScope, column string) *gorm.DB {
	s, ok := scope.(SystemScope)
	if !ok {
		return query.Where("FALSE")
	}
	if s.AllWorkflows {
		return query
	}
	if len(s.WorkflowIDs) == 0 {
		return query.Where("FALSE")
	}
	return query.Where(column+" IN ?", s.WorkflowIDs)
}
