package official

import (
	"context"
	"encoding/json"

	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
)

const quantPluginID = "official.quant"

var quantCandleSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","enum":["spot","usdm"]},"instrument":{"type":"string","pattern":"^[A-Z0-9]{2,32}$"},"interval":{"type":"string","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]},"openTime":{"type":"string","format":"date-time"},"closeTime":{"type":"string","format":"date-time"},"open":{"type":"string","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"high":{"type":"string","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"low":{"type":"string","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"close":{"type":"string","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"volume":{"type":"string","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true}},"required":["market","instrument","interval","openTime","closeTime","open","high","low","close","volume"],"additionalProperties":false}`)

var quantSeriesConfigSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","title":"Market","enum":["spot","usdm"]},"instrument":{"type":"string","title":"Instrument","pattern":"^[A-Z0-9]{2,32}$"},"interval":{"type":"string","title":"Interval","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]}},"required":["market","instrument","interval"],"additionalProperties":false}`)

type quantRuntime struct {
	db       *gorm.DB
	client   *safeHTTPClient
	registry *sdk.Registry
	hub      *quantCandleHub
	quote    func(context.Context, quantSeriesConfig) (quantPublicQuote, error)
}

func RegisterQuant(registry *sdk.Registry, database *gorm.DB) error {
	client, err := newSafeHTTPClient([]string{
		"data-api.binance.vision", "fapi.binance.com", "data-stream.binance.vision", "fstream.binance.com",
	})
	if err != nil {
		return err
	}
	runtime := &quantRuntime{db: database, client: client, registry: registry}
	runtime.hub = newQuantCandleHub(runtime)
	runtime.quote = runtime.fetchQuantPublicQuote
	return registry.RegisterPlugin(sdk.PluginDescriptor{
		ID: quantPluginID, Name: "CoinSphere Quant", Version: "1.0.0",
		Contributes: []string{"nodes", "triggers", "strategies", "apiRoutes", "pages", "resultPages"},
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
		Type: "official.quant.binance_candles", Version: "1.0.0", Kind: sdk.NodeKindTrigger,
		ConfigSchema: quantSeriesConfigSchema, UISchema: json.RawMessage(`{"ui:order":["market","instrument","interval"]}`),
		InputSchema: emptyObjectSchema, OutputSchema: quantCandleSchema,
		Pool: sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
	}, quantCandleTrigger{runtime: q}); err != nil {
		return err
	}
	if err := registrar.Action(sdk.NodeDescriptor{
		Type: "official.quant.sync_instruments", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"markets":{"type":"array","title":"Markets","items":{"type":"string","enum":["spot","usdm"],"enumLabels":["Spot","USD-M"]},"minItems":1,"maxItems":2,"uniqueItems":true,"default":["spot","usdm"]},"quoteAssets":{"type":"array","title":"Quote assets","items":{"type":"string","pattern":"^[A-Z0-9]{2,32}$"},"minItems":1,"maxItems":100,"uniqueItems":true,"default":["USDT","USDC"]},"baseAssetAllowlist":{"type":"array","title":"Base asset allowlist","items":{"type":"string","pattern":"^[A-Z0-9]{2,32}$"},"maxItems":1000,"uniqueItems":true,"default":[]},"baseAssetDenylist":{"type":"array","title":"Base asset denylist","items":{"type":"string","pattern":"^[A-Z0-9]{2,32}$"},"maxItems":1000,"uniqueItems":true,"default":[]},"symbolAllowlist":{"type":"array","title":"Symbol allowlist","items":{"type":"string","pattern":"^[A-Z0-9]{2,32}$"},"maxItems":1000,"uniqueItems":true,"default":[]},"symbolDenylist":{"type":"array","title":"Symbol denylist","items":{"type":"string","pattern":"^[A-Z0-9]{2,32}$"},"maxItems":1000,"uniqueItems":true,"default":[]}},"required":["markets","quoteAssets","baseAssetAllowlist","baseAssetDenylist","symbolAllowlist","symbolDenylist"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["markets","quoteAssets","baseAssetAllowlist","baseAssetDenylist","symbolAllowlist","symbolDenylist"]}`),
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
	if err := registrar.ResultPage(sdk.ResultPageDescriptor{
		PageKey: "quant", Title: "Quant results", ComponentEntry: "./official/quant/ResultPage.vue",
		ScopeSchema: emptyObjectSchema, Mobile: true,
	}); err != nil {
		return err
	}
	return registrar.ResultPage(sdk.ResultPageDescriptor{
		PageKey: "paper", Title: "Paper results", ComponentEntry: "./official/quant/PaperResultPage.vue",
		ScopeSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"workflowId":{"type":"integer","minimum":1},"signalNodeInstanceId":{"type":"string","minLength":1,"maxLength":128},"paperNodeInstanceId":{"type":"string","minLength":1,"maxLength":128}},"required":["workflowId","signalNodeInstanceId","paperNodeInstanceId"],"additionalProperties":false}`),
		FilterSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","enum":["spot","usdm"]},"instrument":{"type":"string","pattern":"^[A-Z0-9]{2,32}$"},"status":{"type":"string","enum":["pending","superseded","approved","rejected","executed"]}},"additionalProperties":false}`),
		Actions:      []string{"approve", "reject", "retry", "cancel", "pause", "export"}, Mobile: true,
	})
}
