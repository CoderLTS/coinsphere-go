package quant

import (
	"encoding/json"

	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
)

const quantPluginID = "official.quant"

var emptyObjectSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`)
var quantSeriesConfigSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"venue":{"type":"string","title":"Venue","pattern":"^[a-z][a-z0-9_-]{1,31}$","default":"binance"},"market":{"type":"string","title":"Market","minLength":1,"maxLength":32},"instrument":{"type":"string","title":"Instrument","pattern":"^[A-Z0-9]{2,32}$"},"interval":{"type":"string","title":"Interval","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]}},"required":["venue","market","instrument","interval"],"additionalProperties":false}`)
var quantStrategyConfigSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"venue":{"type":"string","title":"Venue","pattern":"^[a-z][a-z0-9_-]{1,31}$","default":"binance"},"strategyId":{"type":"string","title":"Strategy","const":"official.quant.sma-crossover"},"market":{"type":"string","title":"Market","minLength":1,"maxLength":32},"instrument":{"type":"string","title":"Instrument","pattern":"^[A-Z0-9]{2,32}$"},"interval":{"type":"string","title":"Interval","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]},"parameters":{"type":"object","title":"Parameters"}},"required":["venue","strategyId","market","instrument","interval","parameters"],"additionalProperties":false}`)
var quantBacktestConfigSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"venue":{"type":"string","title":"Venue","pattern":"^[a-z][a-z0-9_-]{1,31}$","default":"binance"},"strategyId":{"type":"string","title":"Strategy","const":"official.quant.sma-crossover"},"market":{"type":"string","title":"Market","minLength":1,"maxLength":32},"instrument":{"type":"string","title":"Instrument","pattern":"^[A-Z0-9]{2,32}$"},"interval":{"type":"string","title":"Interval","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]},"startTime":{"type":"string","title":"Start (UTC)","format":"date-time"},"endTime":{"type":"string","title":"End (UTC)","format":"date-time"},"initialCapital":{"type":"string","title":"Initial capital","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"feeRate":{"type":"string","title":"Fee rate","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"slippageRate":{"type":"string","title":"Slippage rate","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"parameters":{"type":"object","title":"Parameters"}},"required":["venue","strategyId","market","instrument","interval","startTime","endTime","initialCapital","feeRate","slippageRate","parameters"],"additionalProperties":false}`)

type quantRuntime struct {
	db         *gorm.DB
	registry   sdk.StrategyRegistry
	marketData sdk.MarketDataRegistry
}

func Register(registrar sdk.Registrar, host sdk.Host) error {
	runtime := &quantRuntime{
		db: host.Store.DB(), registry: host.Strategies, marketData: host.MarketData,
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
		UISchema:     json.RawMessage(`{"ui:order":["venue","strategyId","market","instrument","interval","parameters"]}`),
		InputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"eventTime":{"type":"string","format":"date-time"}},"required":["eventTime"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"venue":{"type":"string"},"strategyId":{"type":"string"},"strategyVersion":{"type":"string"},"target":{"type":"string","pattern":"^-?[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"evaluatedAt":{"type":"string","format":"date-time"}},"required":["venue","strategyId","strategyVersion","target","evaluatedAt"],"additionalProperties":false}`),
		Pool:         sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
		Capabilities: sdk.NodeCapabilities{FrameSafe: true},
	}, "策略评估", "使用行情 Provider 运行通用量化策略。", "策略", "#2563eb", "chart-no-axes-combined"), quantEvaluateAction{runtime: q}); err != nil {
		return err
	}
	for _, indicator := range quantIndicatorDefinitions {
		if err := registrar.Action(quantNodeMeta(sdk.NodeDescriptor{
			Type: indicator.NodeType, Version: "1.0.0", Kind: sdk.NodeKindAction,
			Branches: []string{"true", "false"}, ConfigSchema: quantIndicatorConfigSchema(indicator.Indicator),
			UISchema:    json.RawMessage(`{"ui:order":["venue","market","instrument","checkInterval","name","interval","parameters"]}`),
			InputSchema: quantIndicatorInputSchema, OutputSchema: quantIndicatorOutputSchema,
			Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
			Capabilities: sdk.NodeCapabilities{FrameSafe: true},
		}, indicator.Title, "基于闭合 K 线确定性计算 "+indicator.Title+"。", "指标", "#0f766e", indicator.Icon), quantIndicatorAction{runtime: q, indicator: indicator.Indicator}); err != nil {
			return err
		}
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
	if err := registrar.Action(quantNodeMeta(sdk.NodeDescriptor{
		Type: "official.quant.backtest", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: quantBacktestConfigSchema,
		UISchema:     json.RawMessage(`{"ui:order":["venue","strategyId","market","instrument","interval","startTime","endTime","initialCapital","feeRate","slippageRate","parameters"]}`),
		InputSchema:  emptyObjectSchema,
		OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"backtestId":{"type":"integer"},"venue":{"type":"string"},"strategyId":{"type":"string"},"strategyVersion":{"type":"string"},"finalEquity":{"type":"string","x-coinsphere-decimal":true},"totalReturn":{"type":"string","x-coinsphere-decimal":true},"maxDrawdown":{"type":"string","x-coinsphere-decimal":true},"totalFees":{"type":"string","x-coinsphere-decimal":true},"tradeCount":{"type":"integer"},"candleCount":{"type":"integer"}},"required":["backtestId","venue","strategyId","strategyVersion","finalEquity","totalReturn","maxDrawdown","totalFees","tradeCount","candleCount"],"additionalProperties":false}`),
		Pool:         sdk.PoolCompute, SideEffect: sdk.SideEffectData, State: sdk.StateStateless,
	}, "量化回测", "通过任意行情 Provider 执行确定性回测。", "回测", "#7c3aed", "history"), quantBacktestAction{runtime: q}); err != nil {
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
