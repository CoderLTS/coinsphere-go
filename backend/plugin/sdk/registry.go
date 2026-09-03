package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const jsonSchema202012 = "https://json-schema.org/draft/2020-12/schema"
const assistantQueryResultLimit = 64 << 10

var contributionKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
var windowsAbsolutePathPattern = regexp.MustCompile(`^[A-Za-z]:/`)
var assistantQueryNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

type PluginDescriptor struct {
	ID              string
	Name            string
	Menu            PluginMenuDescriptor
	Version         string
	Contributes     []string
	RequiresPlugins map[string]string
}

type RegisterFunc func(Registrar, Host) error

type Registrar interface {
	Action(NodeDescriptor, ActionHandler) error
	Trigger(NodeDescriptor, TriggerHandler) error
	Strategy(Strategy) error
	Page(PageDescriptor) error
	ResultPage(ResultPageDescriptor) error
	Route(RouteDescriptor, ScopedRouteHandler) error
	AssistantQuery(AssistantQueryDescriptor, AssistantQueryHandler) error
	MarketDataProvider(MarketDataProvider) error
	ExecutionProvider(ExecutionProvider) error
	WorkflowValidator(WorkflowValidator) error
	Template(TemplateDescriptor) error
}

type Registry struct {
	plugins          map[string]PluginDescriptor
	nodes            map[string]registeredNode
	strategies       map[string]registeredStrategy
	pages            map[string]PageDescriptor
	resultPages      map[string]ResultPageDescriptor
	routes           map[string]registeredRoute
	assistantQueries map[string]registeredAssistantQuery
	marketData       map[string]registeredMarketDataProvider
	execution        map[string]registeredExecutionProvider
	validators       map[string]WorkflowValidator
	templates        map[string]registeredTemplate
}

type registeredNode struct {
	pluginID string
	desc     NodeDescriptor
	action   ActionHandler
	trigger  TriggerHandler
}

type registeredStrategy struct {
	pluginID string
	desc     StrategyDescriptor
	strategy Strategy
}

type registeredRoute struct {
	desc    RouteDescriptor
	handler ScopedRouteHandler
}

type registeredAssistantQuery struct {
	pluginID string
	toolName string
	desc     AssistantQueryDescriptor
	handler  AssistantQueryHandler
	schema   *jsonschema.Schema
}

type registeredMarketDataProvider struct {
	pluginID string
	provider MarketDataProvider
}

type registeredExecutionProvider struct {
	pluginID string
	provider ExecutionProvider
}

type registeredTemplate struct {
	pluginID string
	desc     TemplateDescriptor
}

func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]PluginDescriptor), nodes: make(map[string]registeredNode),
		strategies: make(map[string]registeredStrategy),
		pages:      make(map[string]PageDescriptor), resultPages: make(map[string]ResultPageDescriptor),
		routes: make(map[string]registeredRoute), assistantQueries: make(map[string]registeredAssistantQuery),
		marketData: make(map[string]registeredMarketDataProvider), execution: make(map[string]registeredExecutionProvider),
		validators: make(map[string]WorkflowValidator), templates: make(map[string]registeredTemplate),
	}
}

func (r *Registry) RegisterPlugin(plugin PluginDescriptor, host Host, register RegisterFunc) error {
	if err := validatePluginDescriptor(plugin); err != nil {
		return fmt.Errorf("plugin %q: %w", plugin.ID, err)
	}
	plugin.Menu.Title = strings.TrimSpace(plugin.Menu.Title)
	plugin.Menu.Parent = strings.TrimSpace(plugin.Menu.Parent)
	if plugin.Menu.Title == "" {
		plugin.Menu.Title = plugin.Name
	}
	if strings.TrimSpace(plugin.Menu.Icon) == "" {
		plugin.Menu.Icon = "ri:puzzle-2-line"
	}
	if plugin.Menu.Mode == "" {
		plugin.Menu.Mode = PluginMenuOwn
	}
	if register == nil {
		return fmt.Errorf("plugin %q: register function is required", plugin.ID)
	}
	if _, exists := r.plugins[plugin.ID]; exists {
		return fmt.Errorf("duplicate plugin id %q", plugin.ID)
	}
	for requiredID, constraintText := range plugin.RequiresPlugins {
		required, exists := r.plugins[requiredID]
		if !exists {
			return fmt.Errorf("plugin %q requires plugin %q", plugin.ID, requiredID)
		}
		constraint, err := semver.NewConstraint(constraintText)
		version, versionErr := semver.StrictNewVersion(required.Version)
		if err != nil || versionErr != nil || !constraint.Check(version) {
			return fmt.Errorf("plugin %q requires plugin %q version %s", plugin.ID, requiredID, constraintText)
		}
	}

	pluginHost := host
	if host.Stores != nil {
		pluginHost.Store = host.Stores.ForPlugin(plugin.ID)
	}
	pluginHost.Stores = nil
	collector := &registrationCollector{plugin: plugin, declared: stringSet(plugin.Contributes)}
	if err := register(collector, pluginHost); err != nil {
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
	for _, strategy := range collector.strategies {
		if previous, exists := r.strategies[strategy.desc.ID]; exists {
			return fmt.Errorf("strategy %q conflicts between plugins %q and %q", strategy.desc.ID, previous.pluginID, plugin.ID)
		}
	}
	for key := range collector.pages {
		if _, exists := r.pages[key]; exists {
			return fmt.Errorf("duplicate page %q", key)
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
	for key := range collector.assistantQueries {
		if previous, exists := r.assistantQueries[key]; exists {
			return fmt.Errorf("assistant query %q conflicts between plugins %q and %q", key, previous.pluginID, plugin.ID)
		}
	}
	for id := range collector.marketData {
		if previous, exists := r.marketData[id]; exists {
			return fmt.Errorf("market data provider %q conflicts between plugins %q and %q", id, previous.pluginID, plugin.ID)
		}
	}
	for id := range collector.execution {
		if previous, exists := r.execution[id]; exists {
			return fmt.Errorf("execution provider %q conflicts between plugins %q and %q", id, previous.pluginID, plugin.ID)
		}
	}
	for key := range collector.templates {
		if previous, exists := r.templates[key]; exists {
			return fmt.Errorf("workflow template %q conflicts between plugins %q and %q", key, previous.pluginID, plugin.ID)
		}
	}

	r.plugins[plugin.ID] = plugin
	for _, node := range collector.nodes {
		r.nodes[node.desc.Type] = node
	}
	for _, strategy := range collector.strategies {
		r.strategies[strategy.desc.ID] = strategy
	}
	for key, page := range collector.pages {
		r.pages[key] = page
	}
	for key, page := range collector.resultPages {
		r.resultPages[key] = page
	}
	for key, route := range collector.routes {
		r.routes[key] = route
	}
	for key, query := range collector.assistantQueries {
		r.assistantQueries[key] = query
	}
	for id, provider := range collector.marketData {
		r.marketData[id] = provider
	}
	for id, provider := range collector.execution {
		r.execution[id] = provider
	}
	if collector.validator != nil {
		r.validators[plugin.ID] = collector.validator
	}
	for key, template := range collector.templates {
		r.templates[key] = template
	}
	return nil
}

func (r *Registry) MarketDataProvider(id string) (MarketDataProvider, bool) {
	provider, ok := r.marketData[id]
	return provider.provider, ok
}

func (r *Registry) ExecutionProvider(id string) (ExecutionProvider, bool) {
	provider, ok := r.execution[id]
	return provider.provider, ok
}

func (r *Registry) WorkflowValidators() []WorkflowValidator {
	ids := make([]string, 0, len(r.validators))
	for id := range r.validators {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]WorkflowValidator, 0, len(ids))
	for _, id := range ids {
		result = append(result, r.validators[id])
	}
	return result
}

func (r *Registry) Templates() []TemplateDescriptor {
	keys := make([]string, 0, len(r.templates))
	for key := range r.templates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]TemplateDescriptor, 0, len(keys))
	for _, key := range keys {
		desc := r.templates[key].desc
		desc.Graph = append(json.RawMessage(nil), desc.Graph...)
		result = append(result, desc)
	}
	return result
}

func (r *Registry) Action(nodeType string) (NodeDescriptor, ActionHandler, bool) {
	node, ok := r.nodes[nodeType]
	if !ok || node.action == nil {
		return NodeDescriptor{}, nil, false
	}
	desc := node.desc
	desc.Branches = append([]string(nil), desc.Branches...)
	return desc, node.action, true
}

func (r *Registry) Trigger(nodeType string) (NodeDescriptor, TriggerHandler, bool) {
	node, ok := r.nodes[nodeType]
	if !ok || node.trigger == nil {
		return NodeDescriptor{}, nil, false
	}
	desc := node.desc
	desc.Branches = append([]string(nil), desc.Branches...)
	return desc, node.trigger, true
}

func (r *Registry) Strategy(strategyID string) (StrategyDescriptor, Strategy, bool) {
	strategy, ok := r.strategies[strategyID]
	if !ok {
		return StrategyDescriptor{}, nil, false
	}
	return strategy.desc, strategy.strategy, true
}

func (r *Registry) Strategies() []StrategyDescriptor {
	strategies := make([]StrategyDescriptor, 0, len(r.strategies))
	for _, strategy := range r.strategies {
		desc := strategy.desc
		desc.ParameterSchema = append(json.RawMessage(nil), desc.ParameterSchema...)
		strategies = append(strategies, desc)
	}
	sort.Slice(strategies, func(i, j int) bool { return strategies[i].ID < strategies[j].ID })
	return strategies
}

func (r *Registry) ResultPage(pluginID, pageKey string) (ResultPageDescriptor, bool) {
	page, ok := r.resultPages[pluginID+"/"+pageKey]
	return page, ok
}

func (r *Registry) PluginResultPages(pluginID string) []ResultPageDescriptor {
	pages := make([]ResultPageDescriptor, 0)
	for key, page := range r.resultPages {
		if !strings.HasPrefix(key, pluginID+"/") {
			continue
		}
		page.ScopeSchema = append(json.RawMessage(nil), page.ScopeSchema...)
		page.FilterSchema = append(json.RawMessage(nil), page.FilterSchema...)
		page.Actions = append([]string(nil), page.Actions...)
		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].PageKey < pages[j].PageKey })
	return pages
}

func (r *Registry) Pages() []RegisteredPage {
	pages := make([]RegisteredPage, 0, len(r.pages))
	for key, page := range r.pages {
		pluginID, _, _ := strings.Cut(key, "/")
		pages = append(pages, RegisteredPage{PluginID: pluginID, Menu: r.plugins[pluginID].Menu, PageDescriptor: page})
	}
	sort.Slice(pages, func(i, j int) bool {
		return pages[i].PluginID+"/"+pages[i].PageKey < pages[j].PluginID+"/"+pages[j].PageKey
	})
	return pages
}

func (r *Registry) Route(pluginID string, desc RouteDescriptor) (ScopedRouteHandler, bool) {
	route, ok := r.routes[routeKey(pluginID, desc)]
	return route.handler, ok
}

func (r *Registry) Routes() []RegisteredRoute {
	routes := make([]RegisteredRoute, 0, len(r.routes))
	for _, plugin := range r.Plugins() {
		prefix := plugin.ID + "/"
		for key, route := range r.routes {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			rest := strings.TrimPrefix(key, prefix)
			parts := strings.SplitN(rest, "/", 2)
			methodPattern := strings.SplitN(parts[1], " ", 2)
			routes = append(routes, RegisteredRoute{
				PluginID:   plugin.ID,
				Descriptor: RouteDescriptor{Scope: ScopeKind(parts[0]), Method: methodPattern[0], Pattern: methodPattern[1], Action: route.desc.Action, WebSocket: route.desc.WebSocket},
				Handler:    route.handler,
			})
		}
	}
	sort.Slice(routes, func(i, j int) bool {
		left, right := routes[i], routes[j]
		return left.PluginID+left.Descriptor.Method+left.Descriptor.Pattern < right.PluginID+right.Descriptor.Method+right.Descriptor.Pattern
	})
	return routes
}

func (r *Registry) AssistantQueries() []RegisteredAssistantQuery {
	queries := make([]RegisteredAssistantQuery, 0, len(r.assistantQueries))
	for _, query := range r.assistantQueries {
		desc := query.desc
		desc.InputSchema = append(json.RawMessage(nil), desc.InputSchema...)
		queries = append(queries, RegisteredAssistantQuery{PluginID: query.pluginID, ToolName: query.toolName, Descriptor: desc})
	}
	sort.Slice(queries, func(i, j int) bool { return queries[i].ToolName < queries[j].ToolName })
	return queries
}

func (r *Registry) RunAssistantQuery(ctx context.Context, toolName string, input json.RawMessage, scope SystemScope) (json.RawMessage, error) {
	query, ok := r.assistantQueries[toolName]
	if !ok {
		return nil, fmt.Errorf("unknown assistant query %q", toolName)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, errors.New("assistant query input does not match its JSON Schema")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || query.schema.Validate(value) != nil {
		return nil, errors.New("assistant query input does not match its JSON Schema")
	}
	scope.PluginID = query.pluginID
	result, err := query.handler.Query(ctx, input, scope)
	if err != nil {
		return nil, err
	}
	if len(result) > assistantQueryResultLimit || !json.Valid(result) {
		return nil, errors.New("assistant query result must be valid JSON within 64 KiB")
	}
	return append(json.RawMessage(nil), result...), nil
}

func (r *Registry) Plugins() []PluginDescriptor {
	plugins := make([]PluginDescriptor, 0, len(r.plugins))
	for _, plugin := range r.plugins {
		plugin.Contributes = append([]string(nil), plugin.Contributes...)
		plugin.RequiresPlugins = cloneStringMap(plugin.RequiresPlugins)
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
		desc.Branches = append([]string(nil), desc.Branches...)
		desc.ConfigSchema = append(json.RawMessage(nil), desc.ConfigSchema...)
		desc.UISchema = append(json.RawMessage(nil), desc.UISchema...)
		desc.InputSchema = append(json.RawMessage(nil), desc.InputSchema...)
		desc.OutputSchema = append(json.RawMessage(nil), desc.OutputSchema...)
		nodes = append(nodes, desc)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Type < nodes[j].Type })
	return nodes
}

func (r *Registry) PluginNodes(pluginID string) []NodeDescriptor {
	nodes := make([]NodeDescriptor, 0)
	for _, node := range r.Nodes() {
		if r.nodes[node.Type].pluginID == pluginID {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

type registrationCollector struct {
	plugin           PluginDescriptor
	declared         map[string]bool
	used             map[string]bool
	nodes            []registeredNode
	strategies       []registeredStrategy
	pages            map[string]PageDescriptor
	resultPages      map[string]ResultPageDescriptor
	routes           map[string]registeredRoute
	assistantQueries map[string]registeredAssistantQuery
	marketData       map[string]registeredMarketDataProvider
	execution        map[string]registeredExecutionProvider
	validator        WorkflowValidator
	templates        map[string]registeredTemplate
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

func (c *registrationCollector) Strategy(strategy Strategy) error {
	if strategy == nil {
		return errors.New("strategy is required")
	}
	desc := strategy.Descriptor()
	if !contributionKeyPattern.MatchString(desc.ID) || !strings.Contains(desc.ID, ".") {
		return errors.New("strategy id must be a dotted lowercase key")
	}
	if _, err := semver.StrictNewVersion(desc.Version); err != nil {
		return fmt.Errorf("strategy %q version must be strict SemVer", desc.ID)
	}
	if strings.TrimSpace(desc.Name) == "" || desc.MinimumLookback < 1 {
		return fmt.Errorf("strategy %q requires a name and positive minimum lookback", desc.ID)
	}
	if err := validateSchema("parameterSchema", desc.ParameterSchema, true); err != nil {
		return fmt.Errorf("strategy %q: %w", desc.ID, err)
	}
	for _, existing := range c.strategies {
		if existing.desc.ID == desc.ID {
			return fmt.Errorf("duplicate strategy %q", desc.ID)
		}
	}
	c.markUsed("strategies")
	c.strategies = append(c.strategies, registeredStrategy{pluginID: c.plugin.ID, desc: desc, strategy: strategy})
	return nil
}

func (c *registrationCollector) addNode(contribution string, node registeredNode) error {
	if strings.HasPrefix(node.desc.Type, "core.") {
		return fmt.Errorf("node type %q is reserved for workflow core", node.desc.Type)
	}
	node.desc = normalizeNodeDescriptor(node.desc, c.plugin.Name)
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

func normalizeNodeDescriptor(desc NodeDescriptor, pluginName string) NodeDescriptor {
	name := strings.TrimSpace(strings.TrimPrefix(desc.Type, strings.Split(desc.Type, ".")[0]+"."))
	if desc.Title == "" {
		desc.Title = name
	}
	if desc.Description == "" {
		desc.Description = desc.Title
	}
	if desc.Category == "" {
		desc.Category = pluginName
	}
	if desc.Color == "" {
		desc.Color = "#64748b"
	}
	if desc.Icon == "" {
		desc.Icon = "box"
	}
	if desc.Width == 0 {
		desc.Width = 220
	}
	if desc.Height == 0 {
		desc.Height = 72
	}
	desc.Capabilities.Stateless = desc.State == StateStateless
	if desc.SideEffect == SideEffectNone {
		desc.Capabilities.Deterministic = true
	}
	return desc
}

func (c *registrationCollector) Page(desc PageDescriptor) error {
	if !contributionKeyPattern.MatchString(desc.PageKey) || strings.TrimSpace(desc.Title) == "" || strings.TrimSpace(desc.Icon) == "" {
		return errors.New("page requires a valid page key, title, and icon")
	}
	if c.pages == nil {
		c.pages = make(map[string]PageDescriptor)
	}
	key := c.plugin.ID + "/" + desc.PageKey
	if _, exists := c.pages[key]; exists {
		return fmt.Errorf("duplicate page %q", key)
	}
	c.markUsed("pages")
	c.pages[key] = desc
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
	if len(desc.FilterSchema) == 0 {
		desc.FilterSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`)
	}
	if err := validateSchema("filterSchema", desc.FilterSchema, true); err != nil {
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
	if strings.ContainsAny(desc.Pattern, "{}") {
		return errors.New("route parameters must use Gin :name syntax")
	}
	if desc.Scope != ScopeWorkflow && desc.Scope != ScopeResult && desc.Scope != ScopeSystem {
		return fmt.Errorf("route %s %s has invalid scope %q", method, desc.Pattern, desc.Scope)
	}
	desc.Action = strings.TrimSpace(desc.Action)
	if desc.Action != "" && (!contributionKeyPattern.MatchString(desc.Action) || desc.Scope != ScopeResult) {
		return fmt.Errorf("route %s %s has invalid result action %q", method, desc.Pattern, desc.Action)
	}
	if desc.WebSocket && (method != "GET" || desc.Scope != ScopeSystem || desc.Action != "") {
		return fmt.Errorf("route %s %s has invalid WebSocket configuration", method, desc.Pattern)
	}
	if handler == nil {
		return errors.New("route handler is required")
	}
	if c.routes == nil {
		c.routes = make(map[string]registeredRoute)
	}
	desc.Method = method
	key := routeKey(c.plugin.ID, desc)
	if _, exists := c.routes[key]; exists {
		return fmt.Errorf("duplicate plugin route %q", key)
	}
	c.markUsed("apiRoutes")
	c.routes[key] = registeredRoute{desc: desc, handler: handler}
	return nil
}

func (c *registrationCollector) AssistantQuery(desc AssistantQueryDescriptor, handler AssistantQueryHandler) error {
	desc.Name = strings.TrimSpace(desc.Name)
	desc.Description = strings.TrimSpace(desc.Description)
	if !assistantQueryNamePattern.MatchString(desc.Name) || desc.Description == "" || len(desc.Description) > 512 {
		return errors.New("assistant query requires a lowercase name and description")
	}
	if handler == nil {
		return errors.New("assistant query handler is required")
	}
	if err := validateSchema("inputSchema", desc.InputSchema, true); err != nil {
		return fmt.Errorf("assistant query %q: %w", desc.Name, err)
	}
	var schemaValue any
	decoder := json.NewDecoder(bytes.NewReader(desc.InputSchema))
	decoder.UseNumber()
	if decoder.Decode(&schemaValue) != nil {
		return fmt.Errorf("assistant query %q input schema is invalid", desc.Name)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	resource := c.plugin.ID + "-" + desc.Name + ".json"
	if err := compiler.AddResource(resource, schemaValue); err != nil {
		return fmt.Errorf("assistant query %q input schema is invalid", desc.Name)
	}
	compiled, err := compiler.Compile(resource)
	if err != nil {
		return fmt.Errorf("assistant query %q input schema is invalid", desc.Name)
	}
	toolName := strings.ReplaceAll(c.plugin.ID, ".", "_") + "_" + desc.Name
	if len(toolName) > 64 {
		return fmt.Errorf("assistant query %q produces a tool name longer than 64 characters", desc.Name)
	}
	if c.assistantQueries == nil {
		c.assistantQueries = make(map[string]registeredAssistantQuery)
	}
	if _, exists := c.assistantQueries[toolName]; exists {
		return fmt.Errorf("duplicate assistant query %q", toolName)
	}
	c.markUsed("assistantQueries")
	c.assistantQueries[toolName] = registeredAssistantQuery{
		pluginID: c.plugin.ID, toolName: toolName, desc: desc, handler: handler, schema: compiled,
	}
	return nil
}

func (c *registrationCollector) MarketDataProvider(provider MarketDataProvider) error {
	if provider == nil || !contributionKeyPattern.MatchString(provider.ID()) {
		return errors.New("market data provider requires a valid id")
	}
	if c.marketData == nil {
		c.marketData = make(map[string]registeredMarketDataProvider)
	}
	if _, exists := c.marketData[provider.ID()]; exists {
		return fmt.Errorf("duplicate market data provider %q", provider.ID())
	}
	c.markUsed("marketDataProviders")
	c.marketData[provider.ID()] = registeredMarketDataProvider{pluginID: c.plugin.ID, provider: provider}
	return nil
}

func (c *registrationCollector) ExecutionProvider(provider ExecutionProvider) error {
	if provider == nil || !contributionKeyPattern.MatchString(provider.ID()) {
		return errors.New("execution provider requires a valid id")
	}
	if c.execution == nil {
		c.execution = make(map[string]registeredExecutionProvider)
	}
	if _, exists := c.execution[provider.ID()]; exists {
		return fmt.Errorf("duplicate execution provider %q", provider.ID())
	}
	c.markUsed("executionProviders")
	c.execution[provider.ID()] = registeredExecutionProvider{pluginID: c.plugin.ID, provider: provider}
	return nil
}

func (c *registrationCollector) WorkflowValidator(validator WorkflowValidator) error {
	if validator == nil || c.validator != nil {
		return errors.New("plugin may register exactly one non-nil workflow validator")
	}
	c.markUsed("workflowValidators")
	c.validator = validator
	return nil
}

func (c *registrationCollector) Template(desc TemplateDescriptor) error {
	if !contributionKeyPattern.MatchString(desc.Key) || strings.TrimSpace(desc.Name) == "" ||
		strings.TrimSpace(desc.Description) == "" || strings.TrimSpace(desc.Mode) == "" || !json.Valid(desc.Graph) {
		return errors.New("workflow template requires a valid key, metadata, and graph")
	}
	if c.templates == nil {
		c.templates = make(map[string]registeredTemplate)
	}
	if _, exists := c.templates[desc.Key]; exists {
		return fmt.Errorf("duplicate workflow template %q", desc.Key)
	}
	c.markUsed("templates")
	c.templates[desc.Key] = registeredTemplate{pluginID: c.plugin.ID, desc: desc}
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
	if plugin.Menu.Mode != "" && plugin.Menu.Mode != PluginMenuOwn && plugin.Menu.Mode != PluginMenuExisting && plugin.Menu.Mode != PluginMenuDirect {
		return fmt.Errorf("invalid plugin menu mode %q", plugin.Menu.Mode)
	}
	if plugin.Menu.Mode == PluginMenuExisting && strings.TrimSpace(plugin.Menu.Parent) == "" {
		return errors.New("existing plugin menu requires a parent")
	}
	if plugin.Menu.Mode != PluginMenuExisting && strings.TrimSpace(plugin.Menu.Parent) != "" {
		return errors.New("plugin menu parent is only valid for existing menu mode")
	}
	if _, err := semver.StrictNewVersion(plugin.Version); err != nil {
		return errors.New("plugin version must be strict SemVer")
	}
	for id, constraint := range plugin.RequiresPlugins {
		if id == plugin.ID || !contributionKeyPattern.MatchString(id) || !strings.Contains(id, ".") {
			return fmt.Errorf("invalid required plugin id %q", id)
		}
		if _, err := semver.NewConstraint(constraint); err != nil {
			return fmt.Errorf("invalid version constraint for required plugin %q", id)
		}
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
	if strings.TrimSpace(desc.Title) == "" || strings.TrimSpace(desc.Description) == "" ||
		strings.TrimSpace(desc.Category) == "" || strings.TrimSpace(desc.Color) == "" || strings.TrimSpace(desc.Icon) == "" {
		return fmt.Errorf("node %q requires title, description, category, color, and icon", desc.Type)
	}
	if desc.Width < 120 || desc.Width > 640 || desc.Height < 48 || desc.Height > 480 {
		return fmt.Errorf("node %q has invalid editor dimensions", desc.Type)
	}
	if desc.Capabilities.Stateless != (desc.State == StateStateless) {
		return fmt.Errorf("node %q stateless capability must match state mode", desc.Type)
	}
	if desc.Capabilities.FrameSafe && (!desc.Capabilities.Deterministic || !desc.Capabilities.Stateless ||
		desc.SideEffect != SideEffectNone && !desc.Capabilities.FrameResult) {
		return fmt.Errorf("node %q frame-safe capability requires deterministic stateless execution without frame side effects", desc.Type)
	}
	if desc.Pool != PoolStream && desc.Pool != PoolCompute {
		return fmt.Errorf("node %q has invalid pool %q", desc.Type, desc.Pool)
	}
	if desc.SideEffect != SideEffectNone && desc.SideEffect != SideEffectData && desc.SideEffect != SideEffectNotification && desc.SideEffect != SideEffectHumanAction && desc.SideEffect != SideEffectPaper {
		return fmt.Errorf("node %q has invalid side effect %q", desc.Type, desc.SideEffect)
	}
	if desc.State != StateStateless && desc.State != StatePersistent {
		return fmt.Errorf("node %q has invalid state mode %q", desc.Type, desc.State)
	}
	branches := map[string]bool{}
	for _, branch := range desc.Branches {
		if branch == "out" || !contributionKeyPattern.MatchString(branch) || branches[branch] {
			return fmt.Errorf("node %q has invalid or duplicate branch %q", desc.Type, branch)
		}
		branches[branch] = true
	}
	if len(desc.Branches) == 1 {
		return fmt.Errorf("node %q must declare zero or at least two branches", desc.Type)
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

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
