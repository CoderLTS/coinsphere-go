package binance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"coinsphere/backend/plugin/sdk"
	profileStore "coinsphere/backend/plugin/sdk/profile"
)

// marketDataProfileConfig is the single source of truth for a Binance market
// series. Stream, backfill, indicator and replay nodes reference this profile
// instead of copying venue/instrument/interval settings into every node.
type marketDataProfileConfig struct {
	Market     string `json:"market"`
	Instrument string `json:"instrument"`
	Interval   string `json:"interval"`
	ProxyID    int64  `json:"proxyId"`
}

var marketDataProfileSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","title":"市场","enum":["spot","usdm"]},"instrument":{"type":"string","title":"交易对","pattern":"^[A-Z0-9]{2,32}$"},"interval":{"type":"string","title":"K 线周期","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]},"proxyId":{"type":"integer","title":"代理","minimum":0,"default":0,"x-coinsphere-proxy":true}},"required":["market","instrument","interval"],"additionalProperties":false}`)
var marketDataProfileUISchema = json.RawMessage(`{"ui:order":["market","instrument","interval","proxyId"]}`)

type marketDataProfileProvider struct{ store *profileStore.Store }

func newMarketDataProfileProvider(host sdk.Host) (*marketDataProfileProvider, error) {
	descriptor := sdk.ProfileDescriptor{
		Type: "market.data", Version: "v1", Title: "行情数据",
		Description:  "可复用于采集、指标、策略与回放的单一行情序列。",
		ConfigSchema: marketDataProfileSchema, UISchema: marketDataProfileUISchema,
	}
	store, err := profileStore.NewStore(host.Store, descriptor, profileStore.Options{
		TablePrefix: "plugin_binance",
		Validate: func(_ context.Context, raw json.RawMessage) error {
			return validateMarketDataProfileConfig(raw)
		},
	})
	if err != nil {
		return nil, err
	}
	if err := store.EnsurePublished(context.Background(), "btc-1m", "BTCUSDT · 1 分钟", "Binance Spot BTCUSDT 闭合 K 线", "v1", json.RawMessage(`{"market":"spot","instrument":"BTCUSDT","interval":"1m","proxyId":0}`)); err != nil {
		return nil, err
	}
	return &marketDataProfileProvider{store: store}, nil
}

func (p marketDataProfileProvider) List(ctx context.Context, query sdk.ProfileListQuery) ([]sdk.ProfileSummary, error) {
	return p.store.List(ctx, query)
}

func (p marketDataProfileProvider) ValidateNewReference(ctx context.Context, ref sdk.ProfileRef) error {
	return p.store.ValidateNewReference(ctx, ref)
}

func (p marketDataProfileProvider) Resolve(ctx context.Context, ref sdk.ProfileRef) (sdk.ResolvedProfile, error) {
	return p.store.Resolve(ctx, ref)
}

func validateMarketDataProfileConfig(raw json.RawMessage) error {
	var config marketDataProfileConfig
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || config.Market != "spot" && config.Market != "usdm" ||
		!instrumentPattern.MatchString(config.Instrument) {
		return errors.New("binance market profile configuration is invalid")
	}
	if _, ok := binanceIntervals[config.Interval]; !ok || config.ProxyID < 0 {
		return errors.New("binance market profile configuration is invalid")
	}
	return nil
}

func resolveMarketDataProfile(ctx context.Context, resolver sdk.ProfileResolver, ref sdk.ProfileRef) (marketDataProfileConfig, error) {
	if resolver == nil {
		return marketDataProfileConfig{}, errors.New("profile resolver is unavailable")
	}
	resolved, err := resolver.Resolve(ctx, ref)
	if err != nil {
		return marketDataProfileConfig{}, err
	}
	if err := validateMarketDataProfileConfig(resolved.Config); err != nil {
		return marketDataProfileConfig{}, err
	}
	var config marketDataProfileConfig
	decoder := json.NewDecoder(strings.NewReader(string(resolved.Config)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return marketDataProfileConfig{}, errors.New("resolved market profile configuration is invalid")
	}
	return config, nil
}

var _ sdk.ProfileProvider = marketDataProfileProvider{}
