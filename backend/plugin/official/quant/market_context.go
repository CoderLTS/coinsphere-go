package quant

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"coinsphere/backend/plugin/sdk"
)

type quantMarketContext struct {
	AsOf     string                       `json:"asOf"`
	Sources  map[string]quantSeriesConfig `json:"sources"`
	Problems map[string]string            `json:"problems"`
}

type quantMarketContextAction struct{ runtime *quantRuntime }

var quantContextOutputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"context":{"type":"object","properties":{"asOf":{"type":"string","format":"date-time"},"sources":{"type":"object"},"problems":{"type":"object","additionalProperties":{"type":"string"}}},"required":["asOf","sources","problems"]},"eventTime":{"type":"string","format":"date-time"}},"required":["context","eventTime"],"additionalProperties":false}`)

func quantContextOutput(value quantMarketContext) json.RawMessage {
	return mustMarshal(map[string]any{"context": value, "eventTime": value.AsOf})
}

func (q *quantRuntime) registerMarketContext(registrar sdk.Registrar) error {
	descriptor := quantNodeMeta(sdk.NodeDescriptor{Type: "official.quant.market_context", Version: "1.0.0", Kind: sdk.NodeKindAction, Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless, Capabilities: sdk.NodeCapabilities{FrameSafe: true},
		ConfigSchema: emptyObjectSchema,
		UISchema:     json.RawMessage(`{"ui:order":[]}`), InputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"context":{"type":"object","title":"已有行情上下文"},"asOf":{"type":"string","format":"date-time","title":"评估时间"}},"additionalProperties":false}`), OutputSchema: quantContextOutputSchema,
		ProfileSlots: []sdk.ProfileSlot{{Key: "market", Title: "行情 Profile", ProfileTypes: []string{"market.data"}, Required: true}},
	}, "行情上下文", "集中选择其他品种、市场及周期，统一评估时间。", "market", "#0f766e", "chart-candlestick")
	if err := registrar.Action(descriptor, quantMarketContextAction{q}); err != nil {
		return err
	}
	return nil
}

func (a quantMarketContextAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	var input struct {
		Context *quantMarketContext `json:"context"`
		AsOf    string              `json:"asOf"`
	}
	var config struct{}
	if !decodeQuantStrict(request.Config, &config) || !decodeQuantStrict(request.Input, &input) {
		return sdk.ActionResult{}, errors.New("invalid market context")
	}
	asOf := request.TriggeredAt
	result := quantMarketContext{Sources: map[string]quantSeriesConfig{}, Problems: map[string]string{}}
	if input.Context != nil {
		result.Sources = input.Context.Sources
		input.AsOf = input.Context.AsOf
	}
	if input.AsOf != "" {
		var err error
		asOf, err = parseQuantUTCTime(input.AsOf)
		if err != nil {
			return sdk.ActionResult{}, err
		}
	}
	if asOf.IsZero() {
		return sdk.ActionResult{}, errors.New("market context requires asOf")
	}
	result.AsOf = asOf.UTC().Format(time.RFC3339Nano)
	if result.Sources == nil {
		result.Sources = map[string]quantSeriesConfig{}
	}
	profileSeries, err := resolveQuantMarketProfile(ctx, request.Profiles, request.ProfileBindings, "market")
	if err != nil {
		return sdk.ActionResult{}, err
	}
	result.Sources["main"] = profileSeries
	for alias, source := range result.Sources {
		if _, err := parseQuantSeriesConfig(mustMarshal(source)); err != nil {
			return sdk.ActionResult{}, err
		}
		candles, err := a.runtime.loadQuantCandlesThroughClose(ctx, source, asOf, 1)
		if err != nil {
			return sdk.ActionResult{}, err
		}
		if !quantLatestCandleAvailable(candles, asOf, source.Interval) {
			result.Problems[alias] = "应有闭合 K 线尚未到达，请检查采集工作流"
		}
	}
	deadline := request.TriggeredAt.Add(30 * time.Second)
	if len(result.Problems) > 0 && request.ExecutionMode != sdk.ExecutionModeBacktestFrame && time.Now().UTC().Before(deadline) {
		return sdk.ActionResult{Output: quantContextOutput(result), Wait: &sdk.WaitRequest{BlockFollowingRuns: true, Key: request.OperationKey, Until: deadline, WakeAt: time.Now().UTC().Add(time.Second), Data: json.RawMessage(`{}`)}}, nil
	}
	return sdk.ActionResult{Output: quantContextOutput(result)}, nil
}

func quantLatestCandleAvailable(candles []quantCandle, asOf time.Time, interval string) bool {
	if len(candles) == 0 {
		return false
	}
	latest := candles[len(candles)-1]
	return !latest.CloseTime.After(asOf) && latest.CloseTime.Add(quantIntervals[interval]).After(asOf)
}
