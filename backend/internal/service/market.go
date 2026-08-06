package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/marketdata"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInvalidMarketRequest  = errors.New("invalid market request")
	ErrMarketResourceMissing = errors.New("market resource was not found")
	ErrWatchlistExists       = errors.New("watchlist item already exists")
)

type CursorResult[T any] struct {
	Records    []T    `json:"records"`
	NextCursor string `json:"nextCursor"`
	HasMore    bool   `json:"hasMore"`
	Total      int64  `json:"total"`
}

type MarketSymbolQuery struct {
	Page    CursorPage
	Market  string
	Keyword string
}

type MarketSymbol struct {
	ID           string `json:"id"`
	Venue        string `json:"venue"`
	Market       string `json:"market"`
	NativeSymbol string `json:"nativeSymbol"`
	BaseAsset    string `json:"baseAsset"`
	QuoteAsset   string `json:"quoteAsset"`
	Status       string `json:"status"`
	PriceTick    string `json:"priceTick"`
	QuantityStep string `json:"quantityStep"`
	MinQuantity  string `json:"minQuantity"`
	MinNotional  string `json:"minNotional"`
	UpdatedAt    string `json:"updatedAt"`
}

type CandleListQuery struct {
	Page         CursorPage
	InstrumentID string
	Interval     string
	StartTime    string
	EndTime      string
}

type MarketCandle struct {
	InstrumentID string `json:"instrumentId"`
	Interval     string `json:"interval"`
	OpenTime     string `json:"openTime"`
	CloseTime    string `json:"closeTime"`
	Open         string `json:"open"`
	High         string `json:"high"`
	Low          string `json:"low"`
	Close        string `json:"close"`
	BaseVolume   string `json:"baseVolume"`
	IsClosed     bool   `json:"isClosed"`
}

type WatchlistCreatePayload struct {
	InstrumentID string `json:"instrumentId"`
	Interval     string `json:"interval"`
}

type WatchlistItem struct {
	ID           string `json:"id"`
	OwnerUserID  int64  `json:"ownerUserId"`
	InstrumentID string `json:"instrumentId"`
	Interval     string `json:"interval"`
	CreatedAt    string `json:"createdAt"`
}

func (a *App) ListMarketSymbols(ctx context.Context, query MarketSymbolQuery) (CursorResult[MarketSymbol], error) {
	market := strings.TrimSpace(query.Market)
	if err := validateCursorLimit(query.Page); err != nil {
		return CursorResult[MarketSymbol]{}, err
	}
	if market != "" && market != string(marketdata.MarketTypeSpot) && market != string(marketdata.MarketTypeUSDM) {
		return CursorResult[MarketSymbol]{}, invalidMarket("market must be spot or usd_m")
	}
	keyword := strings.ToUpper(strings.TrimSpace(query.Keyword))
	if len(keyword) > 64 {
		return CursorResult[MarketSymbol]{}, invalidMarket("keyword must not exceed 64 characters")
	}
	after, err := parseOptionalUUIDv7(query.Page.After, "cursor")
	if err != nil {
		return CursorResult[MarketSymbol]{}, err
	}

	q := a.dbWithContext(ctx).Model(&db.MarketInstrument{}).Where("venue = ?", marketdata.VenueBinance)
	if market != "" {
		q = q.Where("market_type = ?", market)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("native_symbol ILIKE ? OR base_asset ILIKE ? OR quote_asset ILIKE ?", like, like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return CursorResult[MarketSymbol]{}, err
	}
	if after != uuid.Nil {
		q = q.Where("id > ?", after)
	}
	var rows []db.MarketInstrument
	if err := q.Order("id ASC").Limit(query.Page.Limit + 1).Find(&rows).Error; err != nil {
		return CursorResult[MarketSymbol]{}, err
	}
	hasMore := len(rows) > query.Page.Limit
	if hasMore {
		rows = rows[:query.Page.Limit]
	}
	records := make([]MarketSymbol, 0, len(rows))
	for i := range rows {
		records = append(records, serializeMarketSymbol(rows[i]))
	}
	lastKey := ""
	if len(rows) > 0 {
		lastKey = rows[len(rows)-1].ID.String()
	}
	return typedCursorResult(records, query.Page, lastKey, hasMore, total), nil
}

func (a *App) ListMarketCandles(ctx context.Context, query CandleListQuery) (CursorResult[MarketCandle], error) {
	if err := validateCursorLimit(query.Page); err != nil {
		return CursorResult[MarketCandle]{}, err
	}
	instrumentID, err := parseRequiredUUIDv7(query.InstrumentID, "instrumentId")
	if err != nil {
		return CursorResult[MarketCandle]{}, err
	}
	interval := strings.TrimSpace(query.Interval)
	if _, ok := marketdata.CandleIntervalDuration(marketdata.CandleInterval(interval)); !ok {
		return CursorResult[MarketCandle]{}, invalidMarket("interval is not supported")
	}
	startTime, err := parseOptionalUTCTime(query.StartTime, "startTime")
	if err != nil {
		return CursorResult[MarketCandle]{}, err
	}
	endTime, err := parseOptionalUTCTime(query.EndTime, "endTime")
	if err != nil {
		return CursorResult[MarketCandle]{}, err
	}
	if startTime != nil && endTime != nil && !startTime.Before(*endTime) {
		return CursorResult[MarketCandle]{}, invalidMarket("startTime must be before endTime")
	}
	after, err := parseOptionalUTCTime(query.Page.After, "cursor")
	if err != nil {
		return CursorResult[MarketCandle]{}, err
	}
	if after != nil && (startTime != nil && after.Before(*startTime) || endTime != nil && !after.Before(*endTime)) {
		return CursorResult[MarketCandle]{}, invalidMarket("cursor is outside the requested time range")
	}

	q := a.dbWithContext(ctx).Model(&db.MarketCandle{}).
		Where("venue = ? AND instrument_id = ? AND interval_code = ?", marketdata.VenueBinance, instrumentID, interval)
	if startTime != nil {
		q = q.Where("open_time >= ?", *startTime)
	}
	if endTime != nil {
		q = q.Where("open_time < ?", *endTime)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return CursorResult[MarketCandle]{}, err
	}
	if after != nil {
		q = q.Where("open_time < ?", *after)
	}
	var rows []db.MarketCandle
	if err := q.Order("open_time DESC").Limit(query.Page.Limit + 1).Find(&rows).Error; err != nil {
		return CursorResult[MarketCandle]{}, err
	}
	hasMore := len(rows) > query.Page.Limit
	if hasMore {
		rows = rows[:query.Page.Limit]
	}
	records := make([]MarketCandle, 0, len(rows))
	for i := range rows {
		records = append(records, serializeMarketCandle(rows[i]))
	}
	lastKey := ""
	if len(rows) > 0 {
		lastKey = formatUTC(rows[len(rows)-1].OpenTime)
	}
	return typedCursorResult(records, query.Page, lastKey, hasMore, total), nil
}

func (a *App) ListWatchlistItems(ctx context.Context, ownerUserID int64, page CursorPage) (CursorResult[WatchlistItem], error) {
	if ownerUserID <= 0 {
		return CursorResult[WatchlistItem]{}, invalidMarket("owner is required")
	}
	if err := validateCursorLimit(page); err != nil {
		return CursorResult[WatchlistItem]{}, err
	}
	after, err := parseOptionalUUIDv7(page.After, "cursor")
	if err != nil {
		return CursorResult[WatchlistItem]{}, err
	}
	database := a.dbWithContext(ctx)
	query := database.Model(&db.WatchlistItem{}).Where("owner_user_id = ?", ownerUserID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return CursorResult[WatchlistItem]{}, err
	}
	if after != uuid.Nil {
		query = query.Where("id < ?", after)
	}
	var rows []db.WatchlistItem
	if err := query.Order("id DESC").Limit(page.Limit + 1).Find(&rows).Error; err != nil {
		return CursorResult[WatchlistItem]{}, err
	}
	hasMore := len(rows) > page.Limit
	if hasMore {
		rows = rows[:page.Limit]
	}
	items := make([]WatchlistItem, 0, len(rows))
	for i := range rows {
		items = append(items, serializeWatchlistItem(rows[i]))
	}
	lastKey := ""
	if len(rows) > 0 {
		lastKey = rows[len(rows)-1].ID.String()
	}
	return typedCursorResult(items, page, lastKey, hasMore, total), nil
}

func (a *App) CreateWatchlistItem(ctx context.Context, ownerUserID int64, payload WatchlistCreatePayload) (WatchlistItem, error) {
	if ownerUserID <= 0 {
		return WatchlistItem{}, invalidMarket("owner is required")
	}
	instrumentID, err := parseRequiredUUIDv7(payload.InstrumentID, "instrumentId")
	if err != nil {
		return WatchlistItem{}, err
	}
	interval := strings.TrimSpace(payload.Interval)
	if _, ok := marketdata.CandleIntervalDuration(marketdata.CandleInterval(interval)); !ok {
		return WatchlistItem{}, invalidMarket("interval is not supported")
	}
	database := a.dbWithContext(ctx)
	var instrument db.MarketInstrument
	if err := database.Select("id").Where("id = ? AND venue = ?", instrumentID, marketdata.VenueBinance).Take(&instrument).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return WatchlistItem{}, ErrMarketResourceMissing
		}
		return WatchlistItem{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return WatchlistItem{}, err
	}
	row := db.WatchlistItem{
		ID: id, OwnerUserID: ownerUserID, InstrumentID: instrumentID,
		Interval: interval, CreatedAt: time.Now().UTC(),
	}
	result := database.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "owner_user_id"}, {Name: "instrument_id"}, {Name: "interval_code"}},
		DoNothing: true,
	}).Create(&row)
	if result.Error != nil {
		return WatchlistItem{}, result.Error
	}
	if result.RowsAffected == 0 {
		return WatchlistItem{}, ErrWatchlistExists
	}
	return serializeWatchlistItem(row), nil
}

func (a *App) DeleteWatchlistItem(ctx context.Context, ownerUserID int64, rawID string) error {
	if ownerUserID <= 0 {
		return invalidMarket("owner is required")
	}
	id, err := parseRequiredUUIDv7(rawID, "watchlistId")
	if err != nil {
		return err
	}
	result := a.dbWithContext(ctx).Where("id = ? AND owner_user_id = ?", id, ownerUserID).Delete(&db.WatchlistItem{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMarketResourceMissing
	}
	return nil
}

func typedCursorResult[T any](records []T, page CursorPage, lastKey string, hasMore bool, total int64) CursorResult[T] {
	metadata := cursorResult([]M{}, page, lastKey, hasMore, total)
	return CursorResult[T]{
		Records: records, NextCursor: metadata["nextCursor"].(string),
		HasMore: hasMore, Total: total,
	}
}

func validateCursorLimit(page CursorPage) error {
	if page.Limit < 1 || page.Limit > 200 {
		return invalidMarket("limit must be between 1 and 200")
	}
	return nil
}

func parseRequiredUUIDv7(raw, field string) (uuid.UUID, error) {
	id, err := parseOptionalUUIDv7(raw, field)
	if err != nil {
		return uuid.Nil, err
	}
	if id == uuid.Nil {
		return uuid.Nil, invalidMarket(field + " is required")
	}
	return id, nil
}

func parseOptionalUUIDv7(raw, field string) (uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil || len(raw) != 36 || id.String() != strings.ToLower(raw) || marketdata.ValidateUUIDv7(id) != nil {
		return uuid.Nil, invalidMarket(field + " must be UUIDv7")
	}
	return id, nil
}

func parseOptionalUTCTime(raw, field string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if !strings.HasSuffix(raw, "Z") {
		return nil, invalidMarket(field + " must be UTC RFC3339Nano")
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, invalidMarket(field + " must be UTC RFC3339Nano")
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func invalidMarket(detail string) error {
	return fmt.Errorf("%w: %s", ErrInvalidMarketRequest, detail)
}

func formatUTC(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func serializeMarketSymbol(row db.MarketInstrument) MarketSymbol {
	return MarketSymbol{
		ID: row.ID.String(), Venue: row.Venue, Market: row.Market, NativeSymbol: row.NativeSymbol,
		BaseAsset: row.BaseAsset, QuoteAsset: row.QuoteAsset, Status: row.Status,
		PriceTick: row.PriceTick.String(), QuantityStep: row.QuantityStep.String(),
		MinQuantity: row.MinQuantity.String(), MinNotional: row.MinNotional.String(), UpdatedAt: formatUTC(row.UpdatedAt),
	}
}

func serializeMarketCandle(row db.MarketCandle) MarketCandle {
	return MarketCandle{
		InstrumentID: row.InstrumentID.String(), Interval: row.Interval,
		OpenTime: formatUTC(row.OpenTime), CloseTime: formatUTC(row.CloseTime),
		Open: row.Open.String(), High: row.High.String(), Low: row.Low.String(), Close: row.Close.String(),
		BaseVolume: row.BaseVolume.String(), IsClosed: row.IsClosed,
	}
}

func serializeWatchlistItem(row db.WatchlistItem) WatchlistItem {
	return WatchlistItem{
		ID: row.ID.String(), OwnerUserID: row.OwnerUserID, InstrumentID: row.InstrumentID.String(),
		Interval: row.Interval, CreatedAt: formatUTC(row.CreatedAt),
	}
}
