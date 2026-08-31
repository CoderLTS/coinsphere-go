package quant

import (
	"context"
	"encoding/json"

	"coinsphere/backend/plugin/official/internal/safehttp"
	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
)

const quantPluginID = "official.quant"

var emptyObjectSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`)
var quantCandleSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","enum":["spot","usdm"]},"instrument":{"type":"string","pattern":"^[A-Z0-9]{2,32}$"},"interval":{"type":"string","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]},"openTime":{"type":"string","format":"date-time"},"closeTime":{"type":"string","format":"date-time"},"open":{"type":"string","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"high":{"type":"string","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"low":{"type":"string","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"close":{"type":"string","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"volume":{"type":"string","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true}},"required":["market","instrument","interval","openTime","closeTime","open","high","low","close","volume"],"additionalProperties":false}`)

var quantSeriesConfigSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","title":"Market","enum":["spot","usdm"]},"instrument":{"type":"string","title":"Instrument","pattern":"^[A-Z0-9]{2,32}$"},"interval":{"type":"string","title":"Interval","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]}},"required":["market","instrument","interval"],"additionalProperties":false}`)
var quantCandleStreamConfigSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","title":"Market","enum":["spot","usdm"],"default":"spot"},"proxyId":{"type":"integer","title":"Proxy","minimum":0,"default":0,"x-coinsphere-proxy":true},"instrument":{"type":"string","title":"Instrument","pattern":"^[A-Z0-9]{2,32}$","default":"BTCUSDT"},"intervals":{"type":"array","title":"Intervals","items":{"type":"string","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]},"minItems":1,"maxItems":14,"uniqueItems":true,"default":["1m"]}},"required":["market","instrument","intervals"],"additionalProperties":false}`)
var quantCandleBackfillConfigSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","title":"Market","enum":["spot","usdm"],"default":"spot"},"proxyId":{"type":"integer","title":"Proxy","minimum":0,"default":0,"x-coinsphere-proxy":true},"instrument":{"type":"string","title":"Instrument","pattern":"^[A-Z0-9]{2,32}$","default":"BTCUSDT"},"intervals":{"type":"array","title":"Intervals","items":{"type":"string","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]},"minItems":1,"maxItems":14,"uniqueItems":true,"default":["1m"]},"candleCount":{"type":"integer","title":"Candles per interval","minimum":1,"maximum":10000,"default":500},"endTime":{"type":"string","title":"End time (UTC)","anyOf":[{"const":""},{"format":"date-time","pattern":"(?:Z|[+-]00:00)$"}],"default":""}},"required":["market","instrument","intervals","candleCount"],"additionalProperties":false}`)
var quantCandleBackfillInputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"startTime":{"type":"string","format":"date-time"},"endTime":{"type":"string","format":"date-time"},"initialCapital":{"type":"string","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"feeRate":{"type":"string","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"slippageRate":{"type":"string","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true}},"additionalProperties":false}`)
var quantCandleBackfillOutputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","enum":["spot","usdm"]},"instrument":{"type":"string","pattern":"^[A-Z0-9]{2,32}$"},"intervals":{"type":"array","items":{"type":"string","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]},"minItems":1,"maxItems":14,"uniqueItems":true},"requestedCountPerInterval":{"type":"integer","minimum":1,"maximum":10000},"fetchedCount":{"type":"integer","minimum":0},"insertedCount":{"type":"integer","minimum":0},"completedAt":{"type":"string","format":"date-time"},"startTime":{"type":"string","format":"date-time"},"endTime":{"type":"string","format":"date-time"},"initialCapital":{"type":"string","x-coinsphere-decimal":true},"feeRate":{"type":"string","x-coinsphere-decimal":true},"slippageRate":{"type":"string","x-coinsphere-decimal":true}},"required":["market","instrument","intervals","requestedCountPerInterval","fetchedCount","insertedCount","completedAt"],"additionalProperties":false}`)

type quantRuntime struct {
	db           *gorm.DB
	client       *safehttp.Client
	registry     *sdk.Registry
	hub          *quantCandleHub
	quote        func(context.Context, quantSeriesConfig) (quantPublicQuote, error)
	resolveProxy func(context.Context, int64) (string, error)
}

func Register(registry *sdk.Registry, database *gorm.DB, resolveProxy func(context.Context, int64) (string, error)) error {
	client, err := safehttp.New([]string{
		"data-api.binance.vision", "fapi.binance.com", "data-stream.binance.vision", "fstream.binance.com",
	})
	if err != nil {
		return err
	}
	runtime := &quantRuntime{db: database, client: client, registry: registry, resolveProxy: resolveProxy}
	runtime.hub = newQuantCandleHub(runtime)
	runtime.quote = runtime.fetchQuantPublicQuote
	return registry.RegisterPlugin(sdk.PluginDescriptor{
		ID: quantPluginID, Name: "量化分析", Version: "2.1.0",
		Contributes: []string{"nodes", "triggers", "strategies", "apiRoutes", "pages", "resultPages", "assistantQueries"},
	}, func(registrar sdk.Registrar) error { return runtime.register(registrar) })
}

func (q *quantRuntime) register(registrar sdk.Registrar) error {
	for _, page := range []sdk.PageDescriptor{
		{PageKey: "instruments", Title: "币种数据", Icon: "ri:coins-line", KeepAlive: true},
		{PageKey: "candles", Title: "K 线数据", Icon: "ri:stock-line"},
	} {
		if err := registrar.Page(page); err != nil {
			return err
		}
	}
	if err := registrar.Strategy(smaCrossoverStrategy{}); err != nil {
		return err
	}
	if err := registrar.Trigger(sdk.NodeDescriptor{
		Type: "official.quant.realtime_candles", Version: "1.0.0", Kind: sdk.NodeKindTrigger,
		ConfigSchema: quantCandleStreamConfigSchema, UISchema: json.RawMessage(`{"ui:order":["market","proxyId","instrument","intervals"]}`),
		InputSchema: emptyObjectSchema, OutputSchema: quantCandleSchema,
		Pool: sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
	}, quantCandleRealtimeTrigger{runtime: q}); err != nil {
		return err
	}
	if err := registrar.Action(sdk.NodeDescriptor{
		Type: "official.quant.backfill_candles", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: quantCandleBackfillConfigSchema,
		UISchema:     json.RawMessage(`{"ui:order":["market","proxyId","instrument","intervals","candleCount","endTime"]}`),
		InputSchema:  quantCandleBackfillInputSchema, OutputSchema: quantCandleBackfillOutputSchema,
		Pool: sdk.PoolStream, SideEffect: sdk.SideEffectData, State: sdk.StateStateless,
	}, quantCandleBackfillAction{runtime: q}); err != nil {
		return err
	}
	if err := registrar.Action(sdk.NodeDescriptor{
		Type: "official.quant.sync_instruments", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"markets":{"type":"array","title":"Markets","items":{"type":"string","enum":["spot","usdm"],"enumLabels":["Spot","USD-M"]},"minItems":1,"maxItems":2,"uniqueItems":true,"default":["spot","usdm"]},"proxyId":{"type":"integer","title":"Proxy","minimum":0,"default":0,"x-coinsphere-proxy":true},"quoteAssets":{"type":"array","title":"Quote assets","items":{"type":"string","minLength":1,"maxLength":64},"minItems":1,"maxItems":100,"default":["USDT","USDC"]},"baseAssetAllowlist":{"type":"array","title":"Base asset allowlist","items":{"type":"string","minLength":1,"maxLength":64},"maxItems":1000,"default":[]},"baseAssetDenylist":{"type":"array","title":"Base asset denylist","items":{"type":"string","minLength":1,"maxLength":64},"maxItems":1000,"default":[]},"symbolAllowlist":{"type":"array","title":"Symbol allowlist","items":{"type":"string","minLength":1,"maxLength":64},"maxItems":1000,"default":[]},"symbolDenylist":{"type":"array","title":"Symbol denylist","items":{"type":"string","minLength":1,"maxLength":64},"maxItems":1000,"default":[]}},"required":["markets","quoteAssets","baseAssetAllowlist","baseAssetDenylist","symbolAllowlist","symbolDenylist"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["markets","proxyId","quoteAssets","baseAssetAllowlist","baseAssetDenylist","symbolAllowlist","symbolDenylist"]}`),
		InputSchema:  emptyObjectSchema,
		OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"fetchedCount":{"type":"integer","minimum":0},"matchedCount":{"type":"integer","minimum":0},"upsertedCount":{"type":"integer","minimum":0},"deletedCount":{"type":"integer","minimum":0},"syncedAt":{"type":"string","format":"date-time"}},"required":["fetchedCount","matchedCount","upsertedCount","deletedCount","syncedAt"],"additionalProperties":false}`),
		Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectData, State: sdk.StateStateless,
	}, quantInstrumentSyncAction{runtime: q}); err != nil {
		return err
	}
	if err := registrar.Action(sdk.NodeDescriptor{
		Type: "official.quant.evaluate", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"strategyId":{"type":"string","title":"Strategy","const":"official.quant.sma-crossover"},"market":{"type":"string","title":"Market","enum":["spot","usdm"]},"instrument":{"type":"string","title":"Instrument","pattern":"^[A-Z0-9]{2,32}$"},"interval":{"type":"string","title":"Interval","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]},"parameters":{"type":"object","title":"Parameters"}},"required":["strategyId","market","instrument","interval","parameters"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["strategyId","market","instrument","interval","parameters"]}`),
		InputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"eventTime":{"type":"string","format":"date-time"}},"required":["eventTime"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"strategyId":{"type":"string"},"strategyVersion":{"type":"string"},"target":{"type":"string","pattern":"^-?[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"evaluatedAt":{"type":"string","format":"date-time"}},"required":["strategyId","strategyVersion","target","evaluatedAt"],"additionalProperties":false}`),
		Pool:         sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
	}, quantEvaluateAction{runtime: q}); err != nil {
		return err
	}
	for _, indicator := range quantIndicatorDefinitions {
		if err := registrar.Action(sdk.NodeDescriptor{
			Type: indicator.NodeType, Version: "1.0.0", Kind: sdk.NodeKindAction,
			Branches: []string{"true", "false"}, ConfigSchema: quantIndicatorConfigSchema(indicator.Indicator),
			UISchema:    json.RawMessage(`{"ui:order":["market","instrument","checkInterval","name","interval","parameters"]}`),
			InputSchema: quantIndicatorInputSchema, OutputSchema: quantIndicatorOutputSchema,
			Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
		}, quantIndicatorAction{runtime: q, indicator: indicator.Indicator}); err != nil {
			return err
		}
	}
	if err := q.registerWorkflowStrategyNodes(registrar); err != nil {
		return err
	}
	if err := q.registerMarketSignals(registrar); err != nil {
		return err
	}
	if err := registrar.Action(sdk.NodeDescriptor{
		Type: "official.quant.backtest", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"strategyId":{"type":"string","title":"Strategy","const":"official.quant.sma-crossover"},"market":{"type":"string","title":"Market","enum":["spot","usdm"]},"instrument":{"type":"string","title":"Instrument","pattern":"^[A-Z0-9]{2,32}$"},"interval":{"type":"string","title":"Interval","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]},"startTime":{"type":"string","title":"Start (UTC)","format":"date-time"},"endTime":{"type":"string","title":"End (UTC)","format":"date-time"},"initialCapital":{"type":"string","title":"Initial capital","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"feeRate":{"type":"string","title":"Fee rate","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"slippageRate":{"type":"string","title":"Slippage rate","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"parameters":{"type":"object","title":"Parameters"}},"required":["strategyId","market","instrument","interval","startTime","endTime","initialCapital","feeRate","slippageRate","parameters"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["strategyId","market","instrument","interval","startTime","endTime","initialCapital","feeRate","slippageRate","parameters"]}`),
		InputSchema:  emptyObjectSchema,
		OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"backtestId":{"type":"integer"},"strategyId":{"type":"string"},"strategyVersion":{"type":"string"},"finalEquity":{"type":"string","x-coinsphere-decimal":true},"totalReturn":{"type":"string","x-coinsphere-decimal":true},"maxDrawdown":{"type":"string","x-coinsphere-decimal":true},"totalFees":{"type":"string","x-coinsphere-decimal":true},"tradeCount":{"type":"integer"},"candleCount":{"type":"integer"},"detailSha256":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["backtestId","strategyId","strategyVersion","finalEquity","totalReturn","maxDrawdown","totalFees","tradeCount","candleCount","detailSha256"],"additionalProperties":false}`),
		Pool:         sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
	}, quantBacktestAction{runtime: q}); err != nil {
		return err
	}
	if err := q.registerPaper(registrar); err != nil {
		return err
	}
	for _, route := range []struct {
		desc    sdk.RouteDescriptor
		handler sdk.ScopedRouteHandler
	}{
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/instruments", Scope: sdk.ScopeSystem}, q.handleQuantInstruments},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/candles", Scope: sdk.ScopeSystem}, q.handleQuantCandles},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/strategies", Scope: sdk.ScopeSystem}, q.handleQuantStrategies},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/backtests", Scope: sdk.ScopeSystem}, q.handleQuantBacktests},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/market-signals", Scope: sdk.ScopeSystem}, q.handleQuantMarketSignals},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/signals", Scope: sdk.ScopeSystem}, q.handleQuantSignals},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/paper-accounts", Scope: sdk.ScopeSystem}, q.handleQuantPaperAccounts},
		{sdk.RouteDescriptor{Method: "POST", Pattern: "/paper-accounts/{accountId}/rebuild", Scope: sdk.ScopeSystem}, q.handleQuantPaperAccountRebuild},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/paper", Scope: sdk.ScopeResult}, q.handleQuantPaperResult},
		{sdk.RouteDescriptor{Method: "POST", Pattern: "/signals/{signalId}/approve", Scope: sdk.ScopeResult, Action: "approve"}, q.handleQuantSignalApprove},
		{sdk.RouteDescriptor{Method: "POST", Pattern: "/signals/{signalId}/reject", Scope: sdk.ScopeResult, Action: "reject"}, q.handleQuantSignalReject},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/paper/export", Scope: sdk.ScopeResult, Action: "export"}, q.handleQuantPaperExport},
	} {
		if err := registrar.Route(route.desc, route.handler); err != nil {
			return err
		}
	}
	if err := registrar.AssistantQuery(sdk.AssistantQueryDescriptor{
		Name: "query", Description: "查询 Quant 币种、回测、信号或 Paper 账户的有界摘要。",
		InputSchema: quantAssistantQuerySchema,
	}, sdk.AssistantQueryHandlerFunc(q.assistantQuery)); err != nil {
		return err
	}
	if err := registrar.ResultPage(sdk.ResultPageDescriptor{
		PageKey: "quant", Title: "量化结果", ComponentEntry: "./official/quant/ResultPage.vue",
		ScopeSchema: emptyObjectSchema, Mobile: true,
	}); err != nil {
		return err
	}
	return registrar.ResultPage(sdk.ResultPageDescriptor{
		PageKey: "paper", Title: "Paper 结果", ComponentEntry: "./official/quant/PaperResultPage.vue",
		ScopeSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"workflowId":{"type":"integer","minimum":1},"signalNodeInstanceId":{"type":"string","minLength":1,"maxLength":128},"paperNodeInstanceId":{"type":"string","minLength":1,"maxLength":128}},"required":["workflowId","signalNodeInstanceId","paperNodeInstanceId"],"additionalProperties":false}`),
		FilterSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","enum":["spot","usdm"]},"instrument":{"type":"string","pattern":"^[A-Z0-9]{2,32}$"},"status":{"type":"string","enum":["pending","superseded","approved","rejected","executed"]}},"additionalProperties":false}`),
		Actions:      []string{"approve", "reject", "retry", "cancel", "pause", "export"}, Mobile: true,
	})
}
