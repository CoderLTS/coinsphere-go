package binance

import (
	"encoding/json"

	"coinsphere/backend/plugin/sdk"
)

func registerTemplates(registrar sdk.Registrar) error {
	for _, template := range []sdk.TemplateDescriptor{
		{
			Key: "binance-market-data", Name: "Binance 行情采集", Description: "采集 Binance Spot 或 USD-M 闭合 K 线。", Mode: "stream",
			Graph: json.RawMessage(`{"schemaVersion":1,"nodes":[{"nodeInstanceId":"market-stream","nodeType":"official.binance.realtime_candles","nodeVersion":"1.0.0","config":{"market":"spot","instrument":"BTCUSDT","intervals":["1h"]},"position":{"x":140,"y":220}},{"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":520,"y":220}}],"edges":[{"edgeId":"market-end","sourceNodeInstanceId":"market-stream","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}]}`),
		},
		{
			Key: "binance-private-account", Name: "Binance 私有账户同步", Description: "恢复并持续对账 Binance 订单、成交、持仓和账户快照。", Mode: "stream",
			Graph: json.RawMessage(`{"schemaVersion":1,"nodes":[{"nodeInstanceId":"account-stream","nodeType":"official.binance.account_stream","nodeVersion":"1.0.0","config":{"account":"default","market":"spot","proxyId":0,"reconciliationSeconds":60},"position":{"x":140,"y":220}},{"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":520,"y":220}}],"edges":[{"edgeId":"account-end","sourceNodeInstanceId":"account-stream","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}]}`),
		},
	} {
		if err := registrar.Template(template); err != nil {
			return err
		}
	}
	return nil
}
