package quant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
)

type orderIntentAction struct{ runtime *quantRuntime }

func (q *quantRuntime) registerOrderIntent(registrar sdk.Registrar) error {
	return registrar.Action(quantNodeMeta(sdk.NodeDescriptor{
		Type: "official.quant.order_intent", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"venue":{"type":"string","title":"Venue","pattern":"^[a-z][a-z0-9_-]{1,31}$","default":"binance"},"account":{"type":"string","title":"Account","pattern":"^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$"},"market":{"type":"string","title":"Market","minLength":1,"maxLength":32},"instrument":{"type":"string","title":"Instrument","pattern":"^[A-Z0-9]{2,32}$"},"quantity":{"type":"string","title":"Quantity","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true,"default":"0"},"quoteAmount":{"type":"string","title":"Quote amount","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true,"default":"0"},"positionEffect":{"type":"string","title":"Position effect","enum":["open","reduce"],"default":"open"}},"required":["venue","account","market","instrument","quantity","quoteAmount","positionEffect"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["venue","account","market","instrument","quantity","quoteAmount","positionEffect"]}`),
		InputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"action":{"type":"string","enum":["buy","sell","hold"],"x-coinsphere-field-source":true},"evaluatedAt":{"type":"string","format":"date-time","x-coinsphere-field-source":true}},"required":["action","evaluatedAt"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"venue":{"type":"string"},"account":{"type":"string"},"market":{"type":"string"},"instrument":{"type":"string"},"side":{"type":"string","enum":["buy","sell"]},"quantity":{"type":"string","x-coinsphere-decimal":true},"quoteAmount":{"type":"string","x-coinsphere-decimal":true},"positionEffect":{"type":"string","enum":["open","reduce"]},"clientOrderId":{"type":"string"},"referencePrice":{"type":"string","x-coinsphere-decimal":true},"quotedAt":{"type":"string","format":"date-time"}},"required":["venue","account","market","instrument","side","quantity","quoteAmount","positionEffect","clientOrderId","referencePrice","quotedAt"],"additionalProperties":false}`),
		Pool:         sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
	}, "生成订单意图", "将通用量化信号转换为不含交易所私有参数的订单意图。", "量化", "#2563eb", "file-output"), orderIntentAction{runtime: q})
}

func (a orderIntentAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	var config struct{ Venue, Account, Market, Instrument, Quantity, QuoteAmount, PositionEffect string }
	var input struct{ Action, EvaluatedAt string }
	if json.Unmarshal(request.Config, &config) != nil || json.Unmarshal(request.Input, &input) != nil || input.Action == "hold" {
		return sdk.ActionResult{}, errors.New("quant order intent requires a buy or sell signal")
	}
	quantity, quantityErr := decimal.NewFromString(config.Quantity)
	quoteAmount, quoteErr := decimal.NewFromString(config.QuoteAmount)
	if quantityErr != nil || quoteErr != nil || quantity.Sign() < 0 || quoteAmount.Sign() < 0 || quantity.Sign() == 0 && quoteAmount.Sign() == 0 || quantity.Sign() > 0 && quoteAmount.Sign() > 0 {
		return sdk.ActionResult{}, errors.New("quant order intent requires exactly one positive size")
	}
	if a.runtime.marketData == nil {
		return sdk.ActionResult{}, errors.New("quant market data registry is unavailable")
	}
	provider, ok := a.runtime.marketData.MarketDataProvider(strings.ToLower(config.Venue))
	if !ok {
		return sdk.ActionResult{}, errors.New("quant market data provider is unavailable")
	}
	quote, err := provider.Quote(ctx, sdk.QuoteQuery{Market: strings.ToLower(config.Market), Instrument: strings.ToUpper(config.Instrument)})
	if err != nil {
		return sdk.ActionResult{}, err
	}
	digest := sha256.Sum256([]byte(request.OperationKey))
	clientOrderID := "cs-" + hex.EncodeToString(digest[:])[:28]
	return sdk.ActionResult{Output: mustMarshal(map[string]any{
		"venue": strings.ToLower(config.Venue), "account": strings.TrimSpace(config.Account), "market": strings.ToLower(config.Market),
		"instrument": strings.ToUpper(config.Instrument), "side": input.Action, "quantity": quantity.String(), "quoteAmount": quoteAmount.String(),
		"positionEffect": config.PositionEffect, "clientOrderId": clientOrderID, "referencePrice": quote.Price.String(),
		"quotedAt": quote.QuotedAt.UTC().Format(time.RFC3339Nano),
	})}, nil
}

var _ sdk.ActionHandler = orderIntentAction{}
