package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const jsonSchema202012 = "https://json-schema.org/draft/2020-12/schema"

var contributionKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
var windowsAbsolutePathPattern = regexp.MustCompile(`^[A-Za-z]:/`)

type PluginDescriptor struct {
	ID          string
	Name        string
	Version     string
	Contributes []string
}

type RegisterFunc func(Registrar) error

type Registrar interface {
	Action(NodeDescriptor, ActionHandler) error
	Trigger(NodeDescriptor, TriggerHandler) error
	ResultPage(ResultPageDescriptor) error
	Route(RouteDescriptor, ScopedRouteHandler) error
}

type Registry struct {
	plugins     map[string]PluginDescriptor
	nodes       map[string]registeredNode
	resultPages map[string]ResultPageDescriptor
	routes      map[string]ScopedRouteHandler
}

type registeredNode struct {
	pluginID string
	desc     NodeDescriptor
	action   ActionHandler
	trigger  TriggerHandler
}

func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]PluginDescriptor), nodes: make(map[string]registeredNode),
		resultPages: make(map[string]ResultPageDescriptor), routes: make(map[string]ScopedRouteHandler),
	}
}

func (r *Registry) RegisterPlugin(plugin PluginDescriptor, register RegisterFunc) error {
	if err := validatePluginDescriptor(plugin); err != nil {
		return fmt.Errorf("plugin %q: %w", plugin.ID, err)
	}
	if register == nil {
		return fmt.Errorf("plugin %q: register function is required", plugin.ID)
	}
	if _, exists := r.plugins[plugin.ID]; exists {
		return fmt.Errorf("duplicate plugin id %q", plugin.ID)
	}

	collector := &registrationCollector{plugin: plugin, declared: stringSet(plugin.Contributes)}
	if err := register(collector); err != nil {
		return fmt.Errorf("plugin %q registration failed: %w", plugin.ID, err)
	}
	if err := collector.validateDeclaredContributions(); err != nil {
		return fmt.Errorf("plugin %q: %w", plugin.ID, err)
	}
	for _, node := range collector.nodes {
		if previous, exists := r.nodes[node.desc.Type]; exists {
			return fmt.Errorf("node type %q conflicts between plugins %q and %q", node.desc.Type, previous.pluginID, plugin.ID)
		}
	}
	for key := range collector.resultPages {
		if _, exists := r.resultPages[key]; exists {
			return fmt.Errorf("duplicate result page %q", key)
		}
	}
	for key := range collector.routes {
		if _, exists := r.routes[key]; exists {
			return fmt.Errorf("duplicate plugin route %q", key)
		}
	}

	r.plugins[plugin.ID] = plugin
	for _, node := range collector.nodes {
		r.nodes[node.desc.Type] = node
	}
	for key, page := range collector.resultPages {
		r.resultPages[key] = page
	}
	for key, route := range collector.routes {
		r.routes[key] = route
	}
	return nil
}

func (r *Registry) Action(nodeType string) (NodeDescriptor, ActionHandler, bool) {
	node, ok := r.nodes[nodeType]
	if !ok || node.action == nil {
		return NodeDescriptor{}, nil, false
	}
	return node.desc, node.action, true
}

func (r *Registry) Trigger(nodeType string) (NodeDescriptor, TriggerHandler, bool) {
	node, ok := r.nodes[nodeType]
	if !ok || node.trigger == nil {
		return NodeDescriptor{}, nil, false
	}
	return node.desc, node.trigger, true
}

func (r *Registry) ResultPage(pluginID, pageKey string) (ResultPageDescriptor, bool) {
	page, ok := r.resultPages[pluginID+"/"+pageKey]
	return page, ok
}

func (r *Registry) Route(pluginID string, desc RouteDescriptor) (ScopedRouteHandler, bool) {
	handler, ok := r.routes[routeKey(pluginID, desc)]
	return handler, ok
}

func (r *Registry) Plugins() []PluginDescriptor {
	plugins := make([]PluginDescriptor, 0, len(r.plugins))
	for _, plugin := range r.plugins {
		plugin.Contributes = append([]string(nil), plugin.Contributes...)
		plugins = append(plugins, plugin)
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].ID < plugins[j].ID })
	return plugins
}

// Nodes returns an immutable, stable view of the compiled node catalog.
func (r *Registry) Nodes() []NodeDescriptor {
	nodes := make([]NodeDescriptor, 0, len(r.nodes))
	for _, node := range r.nodes {
		desc := node.desc
		desc.ConfigSchema = append(json.RawMessage(nil), desc.ConfigSchema...)
		desc.UISchema = append(json.RawMessage(nil), desc.UISchema...)
		desc.InputSchema = append(json.RawMessage(nil), desc.InputSchema...)
		desc.OutputSchema = append(json.RawMessage(nil), desc.OutputSchema...)
		nodes = append(nodes, desc)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Type < nodes[j].Type })
	return nodes
}

type registrationCollector struct {
	plugin      PluginDescriptor
	declared    map[string]bool
	used        map[string]bool
	nodes       []registeredNode
	resultPages map[string]ResultPageDescriptor
	routes      map[string]ScopedRouteHandler
}

func (c *registrationCollector) Action(desc NodeDescriptor, handler ActionHandler) error {
	if handler == nil {
		return errors.New("action handler is required")
	}
	if desc.Kind != NodeKindAction {
		return fmt.Errorf("action node %q must use kind %q", desc.Type, NodeKindAction)
	}
	return c.addNode("nodes", registeredNode{pluginID: c.plugin.ID, desc: desc, action: handler})
}

func (c *registrationCollector) Trigger(desc NodeDescriptor, handler TriggerHandler) error {
	if handler == nil {
		return errors.New("trigger handler is required")
	}
	if desc.Kind != NodeKindTrigger {
		return fmt.Errorf("trigger node %q must use kind %q", desc.Type, NodeKindTrigger)
	}
	return c.addNode("triggers", registeredNode{pluginID: c.plugin.ID, desc: desc, trigger: handler})
}

func (c *registrationCollector) addNode(contribution string, node registeredNode) error {
	if strings.HasPrefix(node.desc.Type, "core.") {
		return fmt.Errorf("node type %q is reserved for workflow core", node.desc.Type)
	}
	if err := validateNodeDescriptor(node.desc); err != nil {
		return err
	}
	for _, existing := range c.nodes {
		if existing.desc.Type == node.desc.Type {
			return fmt.Errorf("duplicate node type %q", node.desc.Type)
		}
	}
	c.markUsed(contribution)
	c.nodes = append(c.nodes, node)
	return nil
}

func (c *registrationCollector) ResultPage(desc ResultPageDescriptor) error {
	if !contributionKeyPattern.MatchString(desc.PageKey) || strings.TrimSpace(desc.Title) == "" || strings.TrimSpace(desc.ComponentEntry) == "" {
		return errors.New("result page requires a valid page key, title, and component entry")
	}
	componentEntry := path.Clean(desc.ComponentEntry)
	if strings.Contains(desc.ComponentEntry, `\`) || path.IsAbs(componentEntry) || windowsAbsolutePathPattern.MatchString(componentEntry) || componentEntry == "." || componentEntry == ".." || strings.HasPrefix(componentEntry, "../") {
		return errors.New("result page component entry must stay inside the plugin root")
	}
	if err := validateSchema("scopeSchema", desc.ScopeSchema, true); err != nil {
		return err
	}
	actions := make(map[string]bool, len(desc.Actions))
	for _, action := range desc.Actions {
		if !contributionKeyPattern.MatchString(action) || actions[action] {
			return fmt.Errorf("result page %q has an invalid or duplicate action %q", desc.PageKey, action)
		}
		actions[action] = true
	}
	if c.resultPages == nil {
		c.resultPages = make(map[string]ResultPageDescriptor)
	}
	key := c.plugin.ID + "/" + desc.PageKey
	if _, exists := c.resultPages[key]; exists {
		return fmt.Errorf("duplicate result page %q", key)
	}
	c.markUsed("resultPages")
	c.resultPages[key] = desc
	return nil
}

func (c *registrationCollector) Route(desc RouteDescriptor, handler ScopedRouteHandler) error {
	method := strings.ToUpper(strings.TrimSpace(desc.Method))
	if method == "" || strings.TrimSpace(desc.Pattern) == "" || !strings.HasPrefix(desc.Pattern, "/") {
		return errors.New("route requires an HTTP method and absolute pattern")
	}
	if desc.Scope != ScopeWorkflow && desc.Scope != ScopeResult && desc.Scope != ScopeSystem {
		return fmt.Errorf("route %s %s has invalid scope %q", method, desc.Pattern, desc.Scope)
	}
	if handler == nil {
		return errors.New("route handler is required")
	}
	if c.routes == nil {
		c.routes = make(map[string]ScopedRouteHandler)
	}
	desc.Method = method
	key := routeKey(c.plugin.ID, desc)
	if _, exists := c.routes[key]; exists {
		return fmt.Errorf("duplicate plugin route %q", key)
	}
	c.markUsed("apiRoutes")
	c.routes[key] = handler
	return nil
}

func routeKey(pluginID string, desc RouteDescriptor) string {
	return pluginID + "/" + string(desc.Scope) + "/" + strings.ToUpper(strings.TrimSpace(desc.Method)) + " " + desc.Pattern
}

func (c *registrationCollector) markUsed(contribution string) {
	if c.used == nil {
		c.used = make(map[string]bool)
	}
	c.used[contribution] = true
}

func (c *registrationCollector) validateDeclaredContributions() error {
	for contribution := range c.used {
		if !c.declared[contribution] {
			return fmt.Errorf("registered %s without declaring it in contributes", contribution)
		}
	}
	for contribution := range c.declared {
		if contribution != "migrations" && !c.used[contribution] {
			return fmt.Errorf("declared contribution %s has no registrations", contribution)
		}
	}
	return nil
}

func validatePluginDescriptor(plugin PluginDescriptor) error {
	if !contributionKeyPattern.MatchString(plugin.ID) || !strings.Contains(plugin.ID, ".") {
		return errors.New("invalid plugin id")
	}
	if strings.TrimSpace(plugin.Name) == "" {
		return errors.New("plugin name is required")
	}
	if _, err := semver.StrictNewVersion(plugin.Version); err != nil {
		return errors.New("plugin version must be strict SemVer")
	}
	return nil
}

func validateNodeDescriptor(desc NodeDescriptor) error {
	if !contributionKeyPattern.MatchString(desc.Type) || !strings.Contains(desc.Type, ".") {
		return errors.New("node type must be a dotted lowercase key")
	}
	if _, err := semver.StrictNewVersion(desc.Version); err != nil {
		return fmt.Errorf("node %q version must be strict SemVer", desc.Type)
	}
	if desc.Kind != NodeKindAction && desc.Kind != NodeKindTrigger {
		return fmt.Errorf("node %q has invalid kind %q", desc.Type, desc.Kind)
	}
	if desc.Pool != PoolStream && desc.Pool != PoolCompute {
		return fmt.Errorf("node %q has invalid pool %q", desc.Type, desc.Pool)
	}
	if desc.SideEffect != SideEffectNone && desc.SideEffect != SideEffectNotification && desc.SideEffect != SideEffectHumanAction && desc.SideEffect != SideEffectPaper {
		return fmt.Errorf("node %q has invalid side effect %q", desc.Type, desc.SideEffect)
	}
	if desc.State != StateStateless && desc.State != StatePersistent {
		return fmt.Errorf("node %q has invalid state mode %q", desc.Type, desc.State)
	}
	for name, schema := range map[string]json.RawMessage{
		"configSchema": desc.ConfigSchema, "inputSchema": desc.InputSchema, "outputSchema": desc.OutputSchema,
	} {
		if err := validateSchema(name, schema, true); err != nil {
			return fmt.Errorf("node %q: %w", desc.Type, err)
		}
	}
	if err := validateSchema("uiSchema", desc.UISchema, false); err != nil {
		return fmt.Errorf("node %q: %w", desc.Type, err)
	}
	return nil
}

func validateSchema(name string, raw json.RawMessage, requireDraft bool) error {
	var object map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &object) != nil || object == nil {
		return fmt.Errorf("%s must be a JSON object", name)
	}
	if requireDraft && object["$schema"] != jsonSchema202012 {
		return fmt.Errorf("%s must declare JSON Schema 2020-12", name)
	}
	if requireDraft {
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		if err := compiler.AddResource(name+".json", object); err != nil {
			return fmt.Errorf("%s is invalid: %w", name, err)
		}
		if _, err := compiler.Compile(name + ".json"); err != nil {
			return fmt.Errorf("%s is invalid: %w", name, err)
		}
	}
	return nil
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}
