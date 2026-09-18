package quant

import (
	"encoding/json"

	"coinsphere/backend/plugin/sdk"
)

// Realtime and replay templates bind the same profile and downstream graph.
var quantStrategyTemplate = json.RawMessage(`{
  "schemaVersion":3,
  "profileRefs":[{"pluginId":"official.binance","profileId":"btc-1m","version":"v1","type":"market.data"}],
  "nodes":[
    {"nodeInstanceId":"market","nodeType":"official.binance.realtime_candles","nodeVersion":"1.0.0","profileBindings":{"market":{"pluginId":"official.binance","profileId":"btc-1m","version":"v1","type":"market.data"}},"config":{},"position":{"x":80,"y":220}},
    {"nodeInstanceId":"context","nodeType":"official.quant.market_context","nodeVersion":"1.0.0","profileBindings":{"market":{"pluginId":"official.binance","profileId":"btc-1m","version":"v1","type":"market.data"}},"config":{},"position":{"x":260,"y":220},"inputBindings":{"asOf":{"kind":"event","fieldPath":["closeTime"]}}},
    {"nodeInstanceId":"strategy","nodeType":"official.quant.code_strategy","nodeVersion":"1.0.0","config":{"series":[{"alias":"main","lookback":30}],"parameters":{"target":"1"},"source":"{\"long\": decimalGt(last(ohlcv.main.close), sma(ohlcv.main.close, 20)), \"target\": params.target}","booleanOutputs":["long"],"decimalOutputs":["target"],"branchField":"long"},"position":{"x":520,"y":220},"inputBindings":{"context":{"kind":"node","nodeInstanceId":"context","fieldPath":["context"]}}},
    {"nodeInstanceId":"position","nodeType":"official.quant.position","nodeVersion":"1.0.0","config":{"market":"spot","targetMode":"input"},"position":{"x":760,"y":220},"inputBindings":{"target":{"kind":"node","nodeInstanceId":"strategy","fieldPath":["decimals","target"]},"evaluatedAt":{"kind":"node","nodeInstanceId":"strategy","fieldPath":["evaluatedAt"]}}},
    {"nodeInstanceId":"signal","nodeType":"official.quant.output_signal","nodeVersion":"1.0.0","profileBindings":{"market":{"pluginId":"official.binance","profileId":"btc-1m","version":"v1","type":"market.data"}},"config":{},"position":{"x":1040,"y":220}}
  ],
  "edges":[
    {"edgeId":"market-context","sourceNodeInstanceId":"market","sourcePort":"out","targetNodeInstanceId":"context","targetPort":"in"},
    {"edgeId":"context-strategy","sourceNodeInstanceId":"context","sourcePort":"out","targetNodeInstanceId":"strategy","targetPort":"in"},
    {"edgeId":"strategy-position","sourceNodeInstanceId":"strategy","sourcePort":"true","targetNodeInstanceId":"position","targetPort":"in"},
    {"edgeId":"position-signal","sourceNodeInstanceId":"position","sourcePort":"out","targetNodeInstanceId":"signal","targetPort":"in"}
  ]
}`)

var quantReplayTemplate = json.RawMessage(`{
  "schemaVersion":3,
  "profileRefs":[
    {"pluginId":"official.binance","profileId":"btc-1m","version":"v1","type":"market.data"},
    {"pluginId":"official.quant","profileId":"default-backtest","version":"v1","type":"quant.backtest"}
  ],
  "nodes":[
    {"nodeInstanceId":"replay","nodeType":"official.quant.replay","nodeVersion":"1.0.0","profileBindings":{"market":{"pluginId":"official.binance","profileId":"btc-1m","version":"v1","type":"market.data"},"backtest":{"pluginId":"official.quant","profileId":"default-backtest","version":"v1","type":"quant.backtest"}},"config":{},"position":{"x":80,"y":220}},
    {"nodeInstanceId":"context","nodeType":"official.quant.market_context","nodeVersion":"1.0.0","profileBindings":{"market":{"pluginId":"official.binance","profileId":"btc-1m","version":"v1","type":"market.data"}},"config":{},"position":{"x":300,"y":220},"inputBindings":{"asOf":{"kind":"event","fieldPath":["closeTime"]}}},
    {"nodeInstanceId":"strategy","nodeType":"official.quant.code_strategy","nodeVersion":"1.0.0","config":{"series":[{"alias":"main","lookback":30}],"parameters":{"target":"1"},"source":"{\"long\": decimalGt(last(ohlcv.main.close), sma(ohlcv.main.close, 20)), \"target\": params.target}","booleanOutputs":["long"],"decimalOutputs":["target"],"branchField":"long"},"position":{"x":560,"y":220},"inputBindings":{"context":{"kind":"node","nodeInstanceId":"context","fieldPath":["context"]}}},
    {"nodeInstanceId":"position","nodeType":"official.quant.position","nodeVersion":"1.0.0","config":{"market":"spot","targetMode":"input"},"position":{"x":800,"y":220},"inputBindings":{"target":{"kind":"node","nodeInstanceId":"strategy","fieldPath":["decimals","target"]},"evaluatedAt":{"kind":"node","nodeInstanceId":"strategy","fieldPath":["evaluatedAt"]}}},
    {"nodeInstanceId":"signal","nodeType":"official.quant.output_signal","nodeVersion":"1.0.0","profileBindings":{"market":{"pluginId":"official.binance","profileId":"btc-1m","version":"v1","type":"market.data"}},"config":{},"position":{"x":1080,"y":220}}
  ],
  "edges":[
    {"edgeId":"replay-context","sourceNodeInstanceId":"replay","sourcePort":"each","targetNodeInstanceId":"context","targetPort":"in"},
    {"edgeId":"context-strategy","sourceNodeInstanceId":"context","sourcePort":"out","targetNodeInstanceId":"strategy","targetPort":"in"},
    {"edgeId":"strategy-position","sourceNodeInstanceId":"strategy","sourcePort":"true","targetNodeInstanceId":"position","targetPort":"in"},
    {"edgeId":"position-signal","sourceNodeInstanceId":"position","sourcePort":"out","targetNodeInstanceId":"signal","targetPort":"in"}
  ]
}`)

var quantObservationTemplate = json.RawMessage(`{
  "schemaVersion":3,
  "profileRefs":[{"pluginId":"official.binance","profileId":"btc-1m","version":"v1","type":"market.data"}],
  "nodes":[
    {"nodeInstanceId":"market","nodeType":"official.binance.realtime_candles","nodeVersion":"1.0.0","profileBindings":{"market":{"pluginId":"official.binance","profileId":"btc-1m","version":"v1","type":"market.data"}},"config":{},"position":{"x":80,"y":200}},
    {"nodeInstanceId":"indicator","nodeType":"official.quant.indicator","nodeVersion":"2.0.0","profileBindings":{"market":{"pluginId":"official.binance","profileId":"btc-1m","version":"v1","type":"market.data"}},"config":{"indicator":"price_change","parameters":{"lookback":1,"mode":"rise","threshold":"1"}},"position":{"x":360,"y":200}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":680,"y":200}}
  ],
  "edges":[
    {"edgeId":"market-indicator","sourceNodeInstanceId":"market","sourcePort":"out","targetNodeInstanceId":"indicator","targetPort":"in"},
    {"edgeId":"indicator-end","sourceNodeInstanceId":"indicator","sourcePort":"true","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`)

func registerTemplates(registrar sdk.Registrar) error {
	for _, template := range []sdk.TemplateDescriptor{
		{Key: "quant-strategy", Name: "量化策略与实时行情", Description: "实时行情触发策略；行情配置由 Profile 统一提供。", Graph: quantStrategyTemplate},
		{Key: "quant-replay", Name: "量化回放", Description: "手动回放入口与实时策略共享同一组下游节点。", Graph: quantReplayTemplate},
		{Key: "quant-observation", Name: "指标观察", Description: "单节点单指标，行情配置由 Profile 统一提供。", Graph: quantObservationTemplate},
	} {
		if err := registrar.Template(template); err != nil {
			return err
		}
	}
	return nil
}
