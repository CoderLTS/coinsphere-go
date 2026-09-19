package quant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	profileStore "coinsphere/backend/plugin/sdk/profile"
	"github.com/shopspring/decimal"
)

// quantBacktestProfileConfig contains reusable simulation assumptions. The
// market series stays in a market.data profile so the same strategy graph can
// switch between realtime and replay without copying instrument settings.
type quantBacktestProfileConfig struct {
	StartTime      string `json:"startTime"`
	EndTime        string `json:"endTime"`
	InitialCapital string `json:"initialCapital"`
	FeeRate        string `json:"feeRate"`
	SlippageRate   string `json:"slippageRate"`
}

var quantBacktestProfileSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"startTime":{"type":"string","title":"开始时间（UTC）","format":"date-time"},"endTime":{"type":"string","title":"结束时间（UTC）","format":"date-time"},"initialCapital":{"type":"string","title":"初始资金","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"feeRate":{"type":"string","title":"手续费率","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"slippageRate":{"type":"string","title":"滑点率","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true}},"required":["startTime","endTime","initialCapital","feeRate","slippageRate"],"additionalProperties":false}`)
var quantBacktestProfileUISchema = json.RawMessage(`{"ui:order":["startTime","endTime","initialCapital","feeRate","slippageRate"]}`)

type quantBacktestProfileProvider struct{ store *profileStore.Store }

func newQuantBacktestProfileProvider(host sdk.Host) (*quantBacktestProfileProvider, error) {
	descriptor := sdk.ProfileDescriptor{
		Type: "quant.backtest", Version: "v1", Title: "量化回放参数",
		Description:  "复用回放时间范围、资金、手续费和滑点设置。",
		ConfigSchema: quantBacktestProfileSchema, UISchema: quantBacktestProfileUISchema,
	}
	store, err := profileStore.NewStore(host.Store, descriptor, profileStore.Options{
		TablePrefix: "plugin_quant",
		Validate:    func(_ context.Context, raw json.RawMessage) error { return validateQuantBacktestProfileConfig(raw) },
	})
	if err != nil {
		return nil, err
	}
	if err := store.EnsurePublished(context.Background(), "default-backtest", "默认回放参数", "默认 2024 年 1 月回放，创建后可复制调整", "v1", json.RawMessage(`{"startTime":"2024-01-01T00:00:00Z","endTime":"2024-01-31T00:00:00Z","initialCapital":"10000","feeRate":"0.001","slippageRate":"0"}`)); err != nil {
		return nil, err
	}
	return &quantBacktestProfileProvider{store: store}, nil
}

func (p quantBacktestProfileProvider) List(ctx context.Context, query sdk.ProfileListQuery) ([]sdk.ProfileSummary, error) {
	return p.store.List(ctx, query)
}

func (p quantBacktestProfileProvider) ValidateNewReference(ctx context.Context, ref sdk.ProfileRef) error {
	return p.store.ValidateNewReference(ctx, ref)
}

func (p quantBacktestProfileProvider) Resolve(ctx context.Context, ref sdk.ProfileRef) (sdk.ResolvedProfile, error) {
	return p.store.Resolve(ctx, ref)
}

func validateQuantBacktestProfileConfig(raw json.RawMessage) error {
	var config quantBacktestProfileConfig
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil {
		return errors.New("quant backtest profile configuration is invalid")
	}
	start, startErr := time.Parse(time.RFC3339, config.StartTime)
	end, endErr := time.Parse(time.RFC3339, config.EndTime)
	initial, initialErr := decimal.NewFromString(config.InitialCapital)
	fee, feeErr := decimal.NewFromString(config.FeeRate)
	slippage, slippageErr := decimal.NewFromString(config.SlippageRate)
	if startErr != nil || endErr != nil || !start.Before(end) || initialErr != nil || initial.Sign() <= 0 ||
		feeErr != nil || fee.Sign() < 0 || fee.GreaterThan(quantOne) || slippageErr != nil || slippage.Sign() < 0 || slippage.GreaterThan(quantOne) {
		return errors.New("quant backtest profile values are invalid")
	}
	return nil
}

func resolveQuantBacktestProfile(ctx context.Context, resolver sdk.ProfileResolver, bindings map[string]sdk.ProfileRef, slot string) (quantBacktestProfileConfig, error) {
	ref, ok := bindings[slot]
	if !ok || ref.Type != "quant.backtest" {
		return quantBacktestProfileConfig{}, errors.New("backtest profile binding is required")
	}
	if resolver == nil {
		return quantBacktestProfileConfig{}, errors.New("profile resolver is unavailable")
	}
	resolved, err := resolver.Resolve(ctx, ref)
	if err != nil {
		return quantBacktestProfileConfig{}, err
	}
	if err := validateQuantBacktestProfileConfig(resolved.Config); err != nil {
		return quantBacktestProfileConfig{}, err
	}
	var config quantBacktestProfileConfig
	if err := json.Unmarshal(resolved.Config, &config); err != nil {
		return quantBacktestProfileConfig{}, errors.New("resolved Quant backtest profile is invalid")
	}
	return config, nil
}

var _ sdk.ProfileProvider = quantBacktestProfileProvider{}
