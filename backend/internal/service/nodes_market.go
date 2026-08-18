package service

import (
	"errors"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/marketdata"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func init() {
	registerNode(&workflowNodeDefinition{
		TypeCode: "market.metadata.sync", Label: "同步币种元数据",
		ConfigSchema: M{"type": "object", "properties": M{}}, Execute: marketMetadataSyncExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "market.candles.subscribe", Label: "订阅 K 线",
		ConfigSchema: M{"type": "object", "properties": M{
			"instrumentId": M{"type": "string", "title": "标的"},
			"interval":     M{"type": "string", "title": "周期", "enum": []string{"1m", "5m", "15m", "1h", "4h", "1d"}},
		}, "required": []string{"instrumentId", "interval"}}, Execute: marketCandlesSubscribeExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "market.candles.backfill", Label: "补齐 K 线",
		ConfigSchema: M{"type": "object", "properties": M{
			"instrumentId": M{"type": "string", "title": "标的"},
			"interval":     M{"type": "string", "title": "周期", "enum": []string{"1m", "5m", "15m", "1h", "4h", "1d"}},
			"startTime":    M{"type": "string", "title": "开始时间(UTC)"},
			"endTime":      M{"type": "string", "title": "结束时间(UTC)"},
		}, "required": []string{"instrumentId", "interval", "startTime", "endTime"}}, Execute: marketCandlesBackfillExecute,
	})
}

func marketMetadataSyncExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	if ctx.App.MarketData == nil {
		return nil, permanentErr(errors.New("market data runtime is disabled"))
	}
	settings, err := ctx.App.GetMarketSyncSettings(ctx.Ctx)
	if err != nil {
		return nil, retryableErr(err)
	}
	markets := make([]marketdata.MarketType, len(settings.MarketTypes))
	for i, value := range settings.MarketTypes {
		markets[i] = marketdata.MarketType(value)
	}
	startedAt := time.Now().UTC()
	result, err := ctx.App.MarketData.SyncInstruments(ctx.Ctx, markets, settings.QuoteAssets)
	if err != nil {
		return nil, retryableErr(err)
	}
	output := M{
		"marketTypes": settings.MarketTypes, "quoteAssets": settings.QuoteAssets,
		"syncedCount": result.SyncedCount, "byMarket": result.ByMarket,
		"startedAt": formatUTC(startedAt), "finishedAt": formatUTC(time.Now().UTC()),
	}
	setNodeOutput(ctx, output)
	return &nodeExecResult{Output: output}, nil
}

func marketCandlesSubscribeExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	instrumentID, interval, _, err := marketNodeInstrument(ctx, config)
	if err != nil {
		return nil, permanentErr(err)
	}
	now := time.Now().UTC()
	row := db.MarketWorkflowSubscription{
		WorkflowDefinitionID: ctx.Definition.ID, NodeID: asString(ctx.Node["id"]),
		InstrumentID: instrumentID, Interval: string(interval), CreatedAt: now, UpdatedAt: now,
	}
	err = ctx.App.dbWithContext(ctx.Ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "workflow_definition_id"}, {Name: "node_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"instrument_id": instrumentID, "interval_code": interval, "updated_at": now,
		}),
	}).Create(&row).Error
	if err != nil {
		return nil, retryableErr(err)
	}
	output := M{"instrumentId": instrumentID.String(), "interval": interval, "subscribed": true}
	setNodeOutput(ctx, output)
	return &nodeExecResult{Output: output}, nil
}

func marketCandlesBackfillExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	if ctx.App.MarketData == nil {
		return nil, permanentErr(errors.New("market data runtime is disabled"))
	}
	config := nodeConfig(ctx)
	_, interval, instrument, err := marketNodeInstrument(ctx, config)
	if err != nil {
		return nil, permanentErr(err)
	}
	start, err := parseOptionalUTCTime(asString(config["startTime"]), "startTime")
	if err != nil || start == nil {
		return nil, permanentErr(invalidMarket("startTime is required and must be UTC RFC3339Nano"))
	}
	end, err := parseOptionalUTCTime(asString(config["endTime"]), "endTime")
	if err != nil || end == nil || !start.Before(*end) {
		return nil, permanentErr(invalidMarket("endTime must be UTC and after startTime"))
	}
	written, err := ctx.App.MarketData.Backfill(ctx.Ctx, instrument, interval, *start, *end)
	if err != nil {
		return nil, retryableErr(err)
	}
	output := M{"instrumentId": instrument.ID.String(), "interval": interval, "writtenCount": written}
	setNodeOutput(ctx, output)
	return &nodeExecResult{Output: output}, nil
}

func marketNodeInstrument(ctx *nodeExecContext, config M) (uuid.UUID, marketdata.CandleInterval, marketdata.Instrument, error) {
	instrumentID, err := parseRequiredUUIDv7(strings.TrimSpace(asString(config["instrumentId"])), "instrumentId")
	if err != nil {
		return uuid.Nil, "", marketdata.Instrument{}, err
	}
	interval := marketdata.CandleInterval(strings.TrimSpace(asString(config["interval"])))
	if _, ok := marketdata.CandleIntervalDuration(interval); !ok {
		return uuid.Nil, "", marketdata.Instrument{}, invalidMarket("interval is not supported")
	}
	var row db.MarketInstrument
	err = ctx.App.dbWithContext(ctx.Ctx).Where("id = ? AND venue = ?", instrumentID, marketdata.VenueBinance).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return uuid.Nil, "", marketdata.Instrument{}, ErrMarketResourceMissing
	}
	if err != nil {
		return uuid.Nil, "", marketdata.Instrument{}, err
	}
	instrument := marketdata.Instrument{
		ID: row.ID, Venue: marketdata.Venue(row.Venue), MarketType: marketdata.MarketType(row.Market),
		NativeSymbol: row.NativeSymbol, BaseAsset: row.BaseAsset, QuoteAsset: row.QuoteAsset,
		Status: marketdata.InstrumentStatus(row.Status), PriceTick: row.PriceTick,
		QuantityStep: row.QuantityStep, MinQuantity: row.MinQuantity, MinNotional: row.MinNotional,
		UpdatedAt: row.UpdatedAt.UTC(),
	}
	return instrumentID, interval, instrument, nil
}
