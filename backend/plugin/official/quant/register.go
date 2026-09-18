package quant

import (
	"encoding/json"

	"coinsphere/backend/plugin/sdk"
	profileStore "coinsphere/backend/plugin/sdk/profile"
	"gorm.io/gorm"
)

const quantPluginID = "official.quant"

var emptyObjectSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`)
var quantStrategyConfigSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"strategyId":{"type":"string","title":"策略标识","const":"official.quant.sma-crossover"},"parameters":{"type":"object","title":"参数"}},"required":["strategyId","parameters"],"additionalProperties":false}`)

type quantRuntime struct {
	db         *gorm.DB
	registry   sdk.StrategyRegistry
	marketData sdk.MarketDataRegistry
	profiles   sdk.ProfileResolver
}

func Register(registrar sdk.Registrar, host sdk.Host) error {
	runtime := &quantRuntime{
		db: host.Store.DB(), registry: host.Strategies, marketData: host.MarketData, profiles: host.Profiles,
	}
	backtestProfiles, err := newQuantBacktestProfileProvider(host)
	if err != nil {
		return err
	}
	if err := registrar.Profile(backtestProfiles.store.Descriptor(), backtestProfiles); err != nil {
		return err
	}
	if err := profileStore.RegisterRoutes(registrar, backtestProfiles.store); err != nil {
		return err
	}
	return runtime.register(registrar)
}

func (q *quantRuntime) register(registrar sdk.Registrar) error {
	if err := registrar.WorkflowValidator(sdk.WorkflowValidatorFunc(validateQuantWorkflow)); err != nil {
		return err
	}
	if err := registerTemplates(registrar); err != nil {
		return err
	}
	if err := registrar.Strategy(smaCrossoverStrategy{}); err != nil {
		return err
	}
	if err := registrar.Action(quantNodeMeta(sdk.NodeDescriptor{
		Type: "official.quant.evaluate", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: quantStrategyConfigSchema,
		UISchema:     json.RawMessage(`{"ui:order":["strategyId","parameters"]}`),
		InputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"eventTime":{"type":"string","format":"date-time"}},"required":["eventTime"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"venue":{"type":"string"},"strategyId":{"type":"string"},"strategyVersion":{"type":"string"},"target":{"type":"string","pattern":"^-?[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"evaluatedAt":{"type":"string","format":"date-time"}},"required":["venue","strategyId","strategyVersion","target","evaluatedAt"],"additionalProperties":false}`),
		Pool:         sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
		Capabilities: sdk.NodeCapabilities{FrameSafe: true},
		ProfileSlots: []sdk.ProfileSlot{{Key: "market", Title: "行情 Profile", ProfileTypes: []string{"market.data"}, Required: true}},
	}, "量化策略评估", "使用行情 Provider 运行通用量化策略。", "strategy", "#2563eb", "chart-no-axes-combined"), quantEvaluateAction{runtime: q}); err != nil {
		return err
	}
	if err := registrar.Action(quantNodeMeta(sdk.NodeDescriptor{
		Type: "official.quant.indicator", Version: "2.0.0", Kind: sdk.NodeKindAction,
		Branches:     []string{"true", "false", "unavailable"},
		ConfigSchema: quantIndicatorConfigSchema(),
		UISchema:     json.RawMessage(`{"ui:order":["indicator","parameters"]}`),
		InputSchema:  quantIndicatorInputSchema, OutputSchema: quantIndicatorOutputSchema,
		Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
		Capabilities: sdk.NodeCapabilities{FrameSafe: true},
		Role:         sdk.NodeRoleCompute,
		ConfigGroups: []sdk.ConfigGroup{{Key: "indicator", Title: "指标", Fields: []string{"indicator", "parameters"}}},
		ProfileSlots: []sdk.ProfileSlot{{Key: "market", Title: "行情 Profile", ProfileTypes: []string{"market.data"}, Required: true}},
	}, "指标判断", "基于闭合 K 线确定性计算一个指标；RSI、MACD 等通过指标字段区分。", "compute", "#0f766e", "chart-candlestick"), quantIndicatorAction{runtime: q}); err != nil {
		return err
	}
	if err := q.registerMarketContext(registrar); err != nil {
		return err
	}
	if err := q.registerWorkflowStrategyNodes(registrar); err != nil {
		return err
	}
	if err := q.registerMarketSignals(registrar); err != nil {
		return err
	}
	if err := q.registerOrderIntent(registrar); err != nil {
		return err
	}
	if err := registrar.Page(sdk.PageDescriptor{PageKey: "profiles", Title: "量化 Profiles", Icon: "ri:settings-3-line"}); err != nil {
		return err
	}
	for _, route := range []struct {
		desc    sdk.RouteDescriptor
		handler sdk.ScopedRouteHandler
	}{
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/strategies", Scope: sdk.ScopeSystem}, q.handleQuantStrategies},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/backtests", Scope: sdk.ScopeSystem}, q.handleQuantBacktests},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/backtests/:backtestId", Scope: sdk.ScopeSystem}, q.handleQuantBacktest},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/market-signals", Scope: sdk.ScopeSystem}, q.handleQuantMarketSignals},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/signals", Scope: sdk.ScopeSystem}, q.handleQuantSignals},
	} {
		if err := registrar.Route(route.desc, route.handler); err != nil {
			return err
		}
	}
	if err := registrar.AssistantQuery(sdk.AssistantQueryDescriptor{
		Name: "query", Description: "查询 Quant 回测与信号的有界摘要。",
		InputSchema: quantAssistantQuerySchema,
	}, sdk.AssistantQueryHandlerFunc(q.assistantQuery)); err != nil {
		return err
	}
	return registrar.ResultPage(sdk.ResultPageDescriptor{
		PageKey: "quant", Title: "量化结果", ComponentEntry: "./official/quant/ResultPage.vue",
		ScopeSchema: emptyObjectSchema, Mobile: true,
	})
}
