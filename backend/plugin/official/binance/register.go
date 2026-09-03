package binance

import (
	"context"
	"encoding/json"

	"coinsphere/backend/plugin/sdk"
)

var candleSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"venue":{"type":"string","const":"binance"},"market":{"type":"string","enum":["spot","usdm"]},"instrument":{"type":"string"},"interval":{"type":"string"},"openTime":{"type":"string","format":"date-time"},"closeTime":{"type":"string","format":"date-time"},"open":{"type":"string","x-coinsphere-decimal":true},"high":{"type":"string","x-coinsphere-decimal":true},"low":{"type":"string","x-coinsphere-decimal":true},"close":{"type":"string","x-coinsphere-decimal":true},"volume":{"type":"string","x-coinsphere-decimal":true}},"required":["venue","market","instrument","interval","openTime","closeTime","open","high","low","close","volume"],"additionalProperties":false}`)
var candleStreamSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","title":"市场类型","enum":["spot","usdm"],"default":"spot"},"proxyId":{"type":"integer","title":"代理","minimum":0,"default":0,"x-coinsphere-proxy":true},"instrument":{"type":"string","title":"交易对","pattern":"^[A-Z0-9]{2,32}$","default":"BTCUSDT"},"intervals":{"type":"array","title":"K 线周期","items":{"type":"string","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]},"minItems":1,"maxItems":14,"uniqueItems":true,"default":["1m"]}},"required":["market","instrument","intervals"],"additionalProperties":false}`)
var candleBackfillSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","title":"市场类型","enum":["spot","usdm"],"default":"spot"},"proxyId":{"type":"integer","title":"代理","minimum":0,"default":0,"x-coinsphere-proxy":true},"instrument":{"type":"string","title":"交易对","pattern":"^[A-Z0-9]{2,32}$","default":"BTCUSDT"},"intervals":{"type":"array","title":"K 线周期","items":{"type":"string","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]},"minItems":1,"maxItems":14,"uniqueItems":true,"default":["1h"]},"candleCount":{"type":"integer","title":"每周期 K 线数量","minimum":1,"maximum":10000,"default":500},"endTime":{"type":"string","title":"结束时间（UTC）","default":""}},"required":["market","instrument","intervals","candleCount"],"additionalProperties":false}`)
var candleBackfillOutput = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string"},"instrument":{"type":"string"},"intervals":{"type":"array","items":{"type":"string"}},"requestedCountPerInterval":{"type":"integer"},"fetchedCount":{"type":"integer"},"insertedCount":{"type":"integer"},"completedAt":{"type":"string","format":"date-time"}},"required":["market","instrument","intervals","requestedCountPerInterval","fetchedCount","insertedCount","completedAt"],"additionalProperties":false}`)

func Register(registrar sdk.Registrar, host sdk.Host) error {
	client, err := host.Network.New([]string{"data-api.binance.vision", "api.binance.com", "fapi.binance.com", "data-stream.binance.vision", "stream.binance.com", "fstream.binance.com"})
	if err != nil {
		return err
	}
	var resolveProxy func(context.Context, int64) (string, error)
	if host.OutboundProxy != nil {
		resolveProxy = host.OutboundProxy.ResolveOutboundProxy
	}
	runtime := &binanceRuntime{db: host.Store.DB(), client: client, resolveProxy: resolveProxy}
	runtime.hub = newBinanceCandleHub(runtime)
	if err := registrar.MarketDataProvider(marketDataProvider{runtime: runtime}); err != nil {
		return err
	}
	if err := registrar.Trigger(withNodeMeta(sdk.NodeDescriptor{
		Type: "official.binance.realtime_candles", Version: "1.0.0", Kind: sdk.NodeKindTrigger,
		ConfigSchema: candleStreamSchema, UISchema: json.RawMessage(`{"ui:order":["market","proxyId","instrument","intervals"]}`),
		InputSchema: emptyObjectSchema, OutputSchema: candleSchema, Pool: sdk.PoolStream, SideEffect: sdk.SideEffectData, State: sdk.StateStateless,
	}, "Binance K 线实时采集", "采集并发布 Binance 已闭合 K 线。", "market", "#0f766e", "activity"), binanceCandleRealtimeTrigger{runtime: runtime}); err != nil {
		return err
	}
	if err := registrar.Action(withNodeMeta(sdk.NodeDescriptor{
		Type: "official.binance.backfill_candles", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: candleBackfillSchema, UISchema: json.RawMessage(`{"ui:order":["market","proxyId","instrument","intervals","candleCount","endTime"]}`),
		InputSchema: emptyObjectSchema, OutputSchema: candleBackfillOutput, Pool: sdk.PoolStream, SideEffect: sdk.SideEffectData, State: sdk.StateStateless,
	}, "Binance K 线补数", "补齐 Binance 历史闭合 K 线。", "market", "#0f766e", "database"), binanceCandleBackfillAction{runtime: runtime}); err != nil {
		return err
	}
	if err := registerInstrumentSync(registrar, runtime); err != nil {
		return err
	}
	if err := registerExecution(registrar, runtime); err != nil {
		return err
	}
	if err := registerPaper(registrar, runtime); err != nil {
		return err
	}
	if err := registerRoutes(registrar, runtime); err != nil {
		return err
	}
	if err := registerTemplates(registrar); err != nil {
		return err
	}
	if err := registrar.ResultPage(sdk.ResultPageDescriptor{
		PageKey: "paper", Title: "币安模拟交易结果", ComponentEntry: "./official/binance/PaperResultPage.vue",
		ScopeSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"workflowId":{"type":"integer","minimum":1},"paperNodeInstanceId":{"type":"string","minLength":1,"maxLength":128}},"required":["workflowId","paperNodeInstanceId"],"additionalProperties":false}`),
		FilterSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","enum":["spot","usdm"]},"instrument":{"type":"string","pattern":"^[A-Z0-9]{2,32}$"},"status":{"type":"string","enum":["new","partially_filled","filled","canceled","rejected","expired"]}},"additionalProperties":false}`),
		Actions:      []string{"export"}, Mobile: true,
	}); err != nil {
		return err
	}
	for _, page := range []sdk.PageDescriptor{{PageKey: "instruments", Title: "币安币种", Icon: "ri:coins-line", KeepAlive: true}, {PageKey: "candles", Title: "币安K线", Icon: "ri:stock-line"}, {PageKey: "live-accounts", Title: "币安账户", Icon: "ri:shield-keyhole-line"}} {
		if err := registrar.Page(page); err != nil {
			return err
		}
	}
	return nil
}

func withNodeMeta(desc sdk.NodeDescriptor, title, description, category, color, icon string) sdk.NodeDescriptor {
	desc.Title, desc.Description, desc.Category, desc.Color, desc.Icon = title, description, category, color, icon
	desc.Aliases = append([]string{title}, binanceNodeAliases[desc.Type]...)
	desc.Tags = append([]string{category}, binanceNodeTags[desc.Type]...)
	desc.SortOrder = binanceNodeOrder[desc.Type]
	if desc.SortOrder == 0 {
		desc.SortOrder = 100
	}
	desc.Width, desc.Height = 220, 72
	desc.Capabilities.Stateless = desc.State == sdk.StateStateless
	desc.Capabilities.Deterministic = desc.SideEffect == sdk.SideEffectNone
	return desc
}

var binanceNodeOrder = map[string]int{
	"official.binance.sync_instruments": 10,
	"official.binance.realtime_candles": 20,
	"official.binance.backfill_candles": 30,
	"official.binance.account_stream":   40,
	"official.binance.paper_execute":    10,
	"official.binance.live_execute":     20,
}

var binanceNodeAliases = map[string][]string{
	"official.binance.sync_instruments": {"币种同步", "交易规则", "交易对"},
	"official.binance.realtime_candles": {"实时 K 线", "行情采集", "K线"},
	"official.binance.backfill_candles": {"K 线补数", "历史行情", "回补"},
	"official.binance.account_stream":   {"账户流", "订单同步", "成交同步"},
	"official.binance.paper_execute":    {"模拟交易", "Paper", "纸面交易"},
	"official.binance.live_execute":     {"实盘交易", "真实下单"},
}

var binanceNodeTags = map[string][]string{
	"official.binance.sync_instruments": {"Binance", "数据"},
	"official.binance.realtime_candles": {"Binance", "K线", "实时"},
	"official.binance.backfill_candles": {"Binance", "K线", "历史"},
	"official.binance.account_stream":   {"Binance", "账户", "私有流"},
	"official.binance.paper_execute":    {"Binance", "Paper", "订单"},
	"official.binance.live_execute":     {"Binance", "Live", "风控"},
}
