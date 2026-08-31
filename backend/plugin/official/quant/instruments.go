package quant

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const quantInstrumentSyncLock int64 = 0x435351494e535452

type quantInstrumentSyncAction struct{ runtime *quantRuntime }

func (a quantInstrumentSyncAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	config, err := parseQuantInstrumentSyncConfig(request.Config)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	workflowID, err := strconv.ParseInt(request.Revision.WorkflowID, 10, 64)
	if err != nil || workflowID <= 0 {
		return sdk.ActionResult{}, errors.New("quant workflow identity is invalid")
	}
	now := time.Now().UTC()
	fetchedCount := 0
	instruments := make([]quantInstrument, 0, 4096)
	for _, market := range config.Markets {
		fetched, err := a.runtime.fetchQuantInstruments(ctx, market, config.ProxyID, now)
		if err != nil {
			return sdk.ActionResult{}, err
		}
		fetchedCount += len(fetched)
		for _, instrument := range fetched {
			if quantInstrumentMatches(config, instrument) {
				instruments = append(instruments, instrument)
			}
		}
	}

	deletedCount := int64(0)
	err = a.runtime.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", quantInstrumentSyncLock).Error; err != nil {
			return errors.New("lock Quant instrument sync failed")
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
		if err := tx.Where("workflow_id = ?", workflowID).Delete(&quantInstrumentSource{}).Error; err != nil {
			return errors.New("replace Quant instrument source snapshot failed")
		}
		sources := make([]quantInstrumentSource, len(instruments))
		for index, instrument := range instruments {
			sources[index] = quantInstrumentSource{
				WorkflowID: workflowID, Market: instrument.Market, Symbol: instrument.Symbol, SyncedAt: now,
			}
		}
		if len(sources) > 0 {
			if err := tx.CreateInBatches(&sources, 500).Error; err != nil {
				return errors.New("persist Quant instrument source snapshot failed")
			}
		}
		result := tx.Exec(`DELETE FROM plugin_quant.instruments AS instrument
WHERE NOT EXISTS (
    SELECT 1 FROM plugin_quant.instrument_sources AS source
    WHERE source.market = instrument.market AND source.symbol = instrument.symbol
)`)
		if result.Error != nil {
			return errors.New("delete unreferenced Quant instruments failed")
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

func (q *quantRuntime) fetchQuantInstruments(ctx context.Context, market string, proxyID int64, now time.Time) ([]quantInstrument, error) {
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
	if err := q.getQuantJSON(ctx, target, proxyID, &payload); err != nil {
		return nil, err
	}
	if len(payload.Symbols) == 0 {
		return nil, errors.New("binance instrument metadata is empty")
	}
	result := make([]quantInstrument, 0, len(payload.Symbols))
	for _, item := range payload.Symbols {
		instrument := quantInstrument{
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
		if !valid || !quantInstrumentPattern.MatchString(instrument.Symbol) ||
			!quantInstrumentPattern.MatchString(instrument.BaseAsset) || !quantInstrumentPattern.MatchString(instrument.QuoteAsset) ||
			instrument.Status == "" || instrument.PriceTick.Sign() <= 0 ||
			instrument.QuantityStep.Sign() <= 0 || instrument.MinQuantity.Sign() < 0 {
			return nil, errors.New("binance instrument metadata contains an invalid symbol")
		}
		result = append(result, instrument)
	}
	return result, nil
}

func quantInstrumentMatches(config quantInstrumentSyncConfig, instrument quantInstrument) bool {
	return quantFilterContains(config.QuoteAssets, instrument.QuoteAsset) &&
		(len(config.BaseAssetAllowlist) == 0 || quantFilterContains(config.BaseAssetAllowlist, instrument.BaseAsset)) &&
		(len(config.SymbolAllowlist) == 0 || quantFilterContains(config.SymbolAllowlist, instrument.Symbol)) &&
		!quantFilterContains(config.BaseAssetDenylist, instrument.BaseAsset) &&
		!quantFilterContains(config.SymbolDenylist, instrument.Symbol)
}

func quantFilterContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
