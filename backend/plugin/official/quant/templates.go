package quant

import (
	"encoding/json"

	"coinsphere/backend/plugin/sdk"
)

var quantStrategyTemplate = json.RawMessage(`{
  "schemaVersion":1,
  "nodes":[
    {"nodeInstanceId":"candle-event","nodeType":"core.event","nodeVersion":"1.0.0","config":{"types":["market.candle.closed"]},"position":{"x":80,"y":220}},
    {"nodeInstanceId":"strategy","nodeType":"official.quant.evaluate","nodeVersion":"1.0.0","config":{"venue":"binance","strategyId":"official.quant.sma-crossover","market":"spot","instrument":"BTCUSDT","interval":"1h","parameters":{"fastPeriod":3,"slowPeriod":5}},"inputBindings":{"eventTime":{"kind":"cel","expression":"event.time"}},"position":{"x":380,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":700,"y":220}}
  ],
  "edges":[
    {"edgeId":"event-strategy","sourceNodeInstanceId":"candle-event","sourcePort":"out","targetNodeInstanceId":"strategy","targetPort":"in"},
    {"edgeId":"strategy-end","sourceNodeInstanceId":"strategy","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`)

var quantBacktestTemplate = json.RawMessage(`{
  "schemaVersion":1,
  "nodes":[
    {"nodeInstanceId":"manual","nodeType":"core.manual","nodeVersion":"1.0.0","config":{},"position":{"x":80,"y":220}},
    {"nodeInstanceId":"backtest","nodeType":"official.quant.backtest","nodeVersion":"1.0.0","config":{"venue":"binance","strategyId":"official.quant.sma-crossover","market":"spot","instrument":"BTCUSDT","interval":"1h","startTime":"2026-01-01T00:00:00Z","endTime":"2026-02-01T00:00:00Z","initialCapital":"10000","feeRate":"0.001","slippageRate":"0.0005","parameters":{"fastPeriod":3,"slowPeriod":5}},"position":{"x":380,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":700,"y":220}}
  ],
  "edges":[
    {"edgeId":"manual-backtest","sourceNodeInstanceId":"manual","sourcePort":"out","targetNodeInstanceId":"backtest","targetPort":"in"},
    {"edgeId":"backtest-end","sourceNodeInstanceId":"backtest","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`)

func registerTemplates(registrar sdk.Registrar) error {
	for _, template := range []sdk.TemplateDescriptor{
		{Key: "quant-strategy", Name: "通用量化策略", Description: "使用 venue 选择任意行情 Provider。", Mode: "event", Graph: quantStrategyTemplate},
		{Key: "quant-backtest", Name: "通用量化回测", Description: "通过行情 Provider 执行确定性回测。", Mode: "batch", Graph: quantBacktestTemplate},
	} {
		if err := registrar.Template(template); err != nil {
			return err
		}
	}
	return nil
}
