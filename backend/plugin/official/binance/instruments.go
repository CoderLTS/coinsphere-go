package binance

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const binanceInstrumentSyncLock int64 = 0x435351494e535452

func registerInstrumentSync(registrar sdk.Registrar, runtime *binanceRuntime) error {
	return registrar.Action(withNodeMeta(sdk.NodeDescriptor{
		Type: "official.binance.sync_instruments", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"markets":{"type":"array","title":"市场类型","items":{"type":"string","enum":["spot","usdm"]},"minItems":1,"maxItems":2,"uniqueItems":true,"default":["spot","usdm"]},"proxyId":{"type":"integer","title":"代理","minimum":0,"default":0,"x-coinsphere-proxy":true},"quoteAssets":{"type":"array","title":"报价资产","items":{"type":"string"},"minItems":1,"maxItems":100,"default":["USDT","USDC"]},"baseAssetAllowlist":{"type":"array","title":"基础资产白名单","items":{"type":"string"},"maxItems":1000,"default":[]},"baseAssetDenylist":{"type":"array","title":"基础资产黑名单","items":{"type":"string"},"maxItems":1000,"default":[]},"symbolAllowlist":{"type":"array","title":"交易对白名单","items":{"type":"string"},"maxItems":1000,"default":[]},"symbolDenylist":{"type":"array","title":"交易对黑名单","items":{"type":"string"},"maxItems":1000,"default":[]}},"required":["markets","quoteAssets","baseAssetAllowlist","baseAssetDenylist","symbolAllowlist","symbolDenylist"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["markets","proxyId","quoteAssets","baseAssetAllowlist","baseAssetDenylist","symbolAllowlist","symbolDenylist"]}`),
		InputSchema:  emptyObjectSchema,
		OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"fetchedCount":{"type":"integer"},"matchedCount":{"type":"integer"},"upsertedCount":{"type":"integer"},"deletedCount":{"type":"integer"},"syncedAt":{"type":"string","format":"date-time"}},"required":["fetchedCount","matchedCount","upsertedCount","deletedCount","syncedAt"],"additionalProperties":false}`),
		Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectData, State: sdk.StateStateless,
	}, "Binance 币种元数据采集", "同步 Binance 现货与 USD-M 交易规则。", "market", "#0f766e", "list-filter"), binanceInstrumentSyncAction{runtime: runtime})
}

type binanceInstrumentSyncAction struct{ runtime *binanceRuntime }

func (a binanceInstrumentSyncAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	config, err := parseBinanceInstrumentSyncConfig(request.Config)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	workflowID, err := strconv.ParseInt(request.Revision.WorkflowID, 10, 64)
	if err != nil || workflowID <= 0 {
		return sdk.ActionResult{}, errors.New("Binance workflow identity is invalid")
	}
	now := time.Now().UTC()
	fetchedCount := 0
	instruments := make([]binanceInstrument, 0, 4096)
	for _, market := range config.Markets {
		fetched, err := a.runtime.fetchBinanceInstruments(ctx, market, config.ProxyID, now)
		if err != nil {
			return sdk.ActionResult{}, err
		}
		fetchedCount += len(fetched)
		for _, instrument := range fetched {
			if binanceInstrumentMatches(config, instrument) {
				instruments = append(instruments, instrument)
			}
		}
	}

	deletedCount := int64(0)
	err = a.runtime.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", binanceInstrumentSyncLock).Error; err != nil {
			return errors.New("lock Binance instrument sync failed")
		}
		if len(instruments) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "market"}, {Name: "symbol"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"base_asset", "quote_asset", "status", "price_tick", "quantity_step", "min_quantity", "updated_at",
				}),
			}).CreateInBatches(&instruments, 500).Error; err != nil {
				return errors.New("persist Binance instrument metadata failed")
			}
		}
		if err := tx.Where("workflow_id = ?", workflowID).Delete(&binanceInstrumentSource{}).Error; err != nil {
			return errors.New("replace Binance instrument source snapshot failed")
		}
		sources := make([]binanceInstrumentSource, len(instruments))
		for index, instrument := range instruments {
			sources[index] = binanceInstrumentSource{
				WorkflowID: workflowID, Market: instrument.Market, Symbol: instrument.Symbol, SyncedAt: now,
			}
		}
		if len(sources) > 0 {
			if err := tx.CreateInBatches(&sources, 500).Error; err != nil {
				return errors.New("persist Binance instrument source snapshot failed")
			}
		}
		result := tx.Exec(`DELETE FROM plugin_binance.instruments AS instrument
WHERE NOT EXISTS (
    SELECT 1 FROM plugin_binance.instrument_sources AS source
    WHERE source.market = instrument.market AND source.symbol = instrument.symbol
)`)
		if result.Error != nil {
			return errors.New("delete unreferenced Binance instruments failed")
		}
		deletedCount = result.RowsAffected
		return nil
	})
	if err != nil {
		return sdk.ActionResult{}, err
	}
	request.Logger.Info("Binance 币种元数据同步完成",
		"fetched_count", fetchedCount, "matched_count", len(instruments), "upserted_count", len(instruments),
		"deleted_count", deletedCount, "synced_at", now)
	return sdk.ActionResult{Output: mustMarshal(map[string]any{
		"fetchedCount": fetchedCount, "matchedCount": len(instruments), "upsertedCount": len(instruments),
		"deletedCount": deletedCount, "syncedAt": now.Format(time.RFC3339Nano),
	})}, nil
}

func (q *binanceRuntime) fetchBinanceInstruments(ctx context.Context, market string, proxyID int64, now time.Time) ([]binanceInstrument, error) {
	target := "https://data-api.binance.vision/api/v3/exchangeInfo"
	if market == "usdm" {
		target = "https://fapi.binance.com/fapi/v1/exchangeInfo"
	}
	var payload struct {
		Symbols []struct {
			Symbol, BaseAsset, QuoteAsset, Status string
			Filters                               []struct {
				FilterType, TickSize, StepSize, MinQty string
			} `json:"filters"`
		} `json:"symbols"`
	}
	if err := q.getBinanceJSON(ctx, target, proxyID, &payload); err != nil {
		return nil, err
	}
	if len(payload.Symbols) == 0 {
		return nil, errors.New("binance instrument metadata is empty")
	}
	result := make([]binanceInstrument, 0, len(payload.Symbols))
	for _, item := range payload.Symbols {
		instrument := binanceInstrument{
			Market: market, Symbol: strings.ToUpper(strings.TrimSpace(item.Symbol)),
			BaseAsset: strings.ToUpper(strings.TrimSpace(item.BaseAsset)), QuoteAsset: strings.ToUpper(strings.TrimSpace(item.QuoteAsset)),
			Status: strings.TrimSpace(item.Status), UpdatedAt: now,
		}
		valid := true
		for _, filter := range item.Filters {
			switch filter.FilterType {
			case "PRICE_FILTER":
				value, parseErr := decimal.NewFromString(filter.TickSize)
				instrument.PriceTick, valid = value, valid && parseErr == nil
			case "LOT_SIZE":
				step, stepErr := decimal.NewFromString(filter.StepSize)
				minimum, minimumErr := decimal.NewFromString(filter.MinQty)
				instrument.QuantityStep, instrument.MinQuantity = step, minimum
				valid = valid && stepErr == nil && minimumErr == nil
			}
		}
		if !valid || !binanceInstrumentPattern.MatchString(instrument.Symbol) ||
			!binanceInstrumentPattern.MatchString(instrument.BaseAsset) || !binanceInstrumentPattern.MatchString(instrument.QuoteAsset) ||
			instrument.Status == "" || instrument.PriceTick.Sign() <= 0 ||
			instrument.QuantityStep.Sign() <= 0 || instrument.MinQuantity.Sign() < 0 {
			return nil, errors.New("binance instrument metadata contains an invalid symbol")
		}
		result = append(result, instrument)
	}
	return result, nil
}

func binanceInstrumentMatches(config binanceInstrumentSyncConfig, instrument binanceInstrument) bool {
	return binanceFilterContains(config.QuoteAssets, instrument.QuoteAsset) &&
		(len(config.BaseAssetAllowlist) == 0 || binanceFilterContains(config.BaseAssetAllowlist, instrument.BaseAsset)) &&
		(len(config.SymbolAllowlist) == 0 || binanceFilterContains(config.SymbolAllowlist, instrument.Symbol)) &&
		!binanceFilterContains(config.BaseAssetDenylist, instrument.BaseAsset) &&
		!binanceFilterContains(config.SymbolDenylist, instrument.Symbol)
}

func binanceFilterContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
