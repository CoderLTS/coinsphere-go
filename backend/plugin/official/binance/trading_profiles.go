package binance

import (
	"context"
	"encoding/json"
	"errors"

	"coinsphere/backend/plugin/sdk"
	profileStore "coinsphere/backend/plugin/sdk/profile"
	"github.com/shopspring/decimal"
)

var tradingAccountProfileSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"account":{"type":"string","title":"账户标识","pattern":"^[A-Za-z0-9._-]{1,64}$"},"market":{"type":"string","title":"市场","enum":["spot","usdm"],"default":"spot"}},"required":["account","market"],"additionalProperties":false}`)
var tradingRiskProfileSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"initialBalance":{"type":"string","title":"初始余额","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"feeRate":{"type":"string","title":"手续费率","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"maxOrderNotional":{"type":"string","title":"最大订单名义金额","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"maxInstrumentNotional":{"type":"string","title":"单交易对最大名义金额","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true}},"required":["initialBalance","feeRate","maxOrderNotional","maxInstrumentNotional"],"additionalProperties":false}`)

type tradingProfileProvider struct{ store *profileStore.Store }

func newTradingProfileProvider(host sdk.Host, descriptor sdk.ProfileDescriptor, validate func(context.Context, json.RawMessage) error, seedID, seedName, seedSummary string, seed json.RawMessage) (*tradingProfileProvider, error) {
	store, err := profileStore.NewStore(host.Store, descriptor, profileStore.Options{TablePrefix: "plugin_binance", Validate: validate})
	if err != nil {
		return nil, err
	}
	if err := store.EnsurePublished(context.Background(), seedID, seedName, seedSummary, "v1", seed); err != nil {
		return nil, err
	}
	return &tradingProfileProvider{store: store}, nil
}
func (p tradingProfileProvider) List(ctx context.Context, q sdk.ProfileListQuery) ([]sdk.ProfileSummary, error) {
	return p.store.List(ctx, q)
}
func (p tradingProfileProvider) ValidateNewReference(ctx context.Context, ref sdk.ProfileRef) error {
	return p.store.ValidateNewReference(ctx, ref)
}
func (p tradingProfileProvider) Resolve(ctx context.Context, ref sdk.ProfileRef) (sdk.ResolvedProfile, error) {
	return p.store.Resolve(ctx, ref)
}

func validateTradingAccountProfile(_ context.Context, raw json.RawMessage) error {
	var value struct{ Account, Market string }
	if json.Unmarshal(raw, &value) != nil || !accountIDPattern.MatchString(value.Account) || value.Market != "spot" && value.Market != "usdm" {
		return errors.New("Binance account profile is invalid")
	}
	return nil
}
func validateTradingRiskProfile(_ context.Context, raw json.RawMessage) error {
	var value struct{ InitialBalance, FeeRate, MaxOrderNotional, MaxInstrumentNotional string }
	if json.Unmarshal(raw, &value) != nil {
		return errors.New("Binance risk profile is invalid")
	}
	initial, e1 := decimal.NewFromString(value.InitialBalance)
	fee, e2 := decimal.NewFromString(value.FeeRate)
	order, e3 := decimal.NewFromString(value.MaxOrderNotional)
	instrument, e4 := decimal.NewFromString(value.MaxInstrumentNotional)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || initial.Sign() <= 0 || fee.Sign() < 0 || order.Sign() <= 0 || instrument.Sign() <= 0 {
		return errors.New("Binance risk profile Decimal values are invalid")
	}
	return nil
}
func resolveTradingProfile(ctx context.Context, resolver sdk.ProfileResolver, bindings map[string]sdk.ProfileRef, slot, profileType string) (json.RawMessage, error) {
	ref, ok := bindings[slot]
	if !ok || ref.Type != profileType || resolver == nil {
		return nil, errors.New("trading profile binding is required")
	}
	value, err := resolver.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), value.Config...), nil
}

func registerTradingProfiles(registrar sdk.Registrar, host sdk.Host) error {
	account, err := newTradingProfileProvider(host, sdk.ProfileDescriptor{Type: "trading.account", Version: "v1", Title: "交易账户", Description: "Paper 或受门禁执行使用的账户身份。", ConfigSchema: tradingAccountProfileSchema, UISchema: json.RawMessage(`{"ui:order":["account","market"]}`)}, validateTradingAccountProfile, "default-paper-account", "默认 Paper 账户", "默认 spot Paper 账户", json.RawMessage(`{"account":"default","market":"spot"}`))
	if err != nil {
		return err
	}
	risk, err := newTradingProfileProvider(host, sdk.ProfileDescriptor{Type: "trading.risk", Version: "v1", Title: "交易风控", Description: "Paper 执行的余额、费率和名义金额上限。", ConfigSchema: tradingRiskProfileSchema, UISchema: json.RawMessage(`{"ui:order":["initialBalance","feeRate","maxOrderNotional","maxInstrumentNotional"]}`)}, validateTradingRiskProfile, "default-paper-risk", "默认 Paper 风控", "默认 Paper 风控上限", json.RawMessage(`{"initialBalance":"10000","feeRate":"0.001","maxOrderNotional":"1000","maxInstrumentNotional":"5000"}`))
	if err != nil {
		return err
	}
	for _, item := range []struct {
		descriptor sdk.ProfileDescriptor
		provider   sdk.ProfileProvider
	}{{account.store.Descriptor(), account}, {risk.store.Descriptor(), risk}} {
		if err := registrar.Profile(item.descriptor, item.provider); err != nil {
			return err
		}
		if err := profileStore.RegisterRoutes(registrar, item.provider.(*tradingProfileProvider).store); err != nil {
			return err
		}
	}
	return nil
}

var _ sdk.ProfileProvider = tradingProfileProvider{}
