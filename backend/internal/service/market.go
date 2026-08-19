package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
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
	Page       CursorPage
	Market     string
	QuoteAsset string
	Status     string
	Keyword    string
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
	quoteAsset := strings.ToUpper(strings.TrimSpace(query.QuoteAsset))
	if quoteAsset != "" && quoteAsset != "USDT" && quoteAsset != "USDC" && quoteAsset != "FDUSD" {
		return CursorResult[MarketSymbol]{}, invalidMarket("quoteAsset must be USDT, USDC or FDUSD")
	}
	status := strings.ToLower(strings.TrimSpace(query.Status))
	if status == "" {
		status = string(marketdata.InstrumentStatusTrading)
	}
	if status != "trading" && status != "suspended" && status != "all" {
		return CursorResult[MarketSymbol]{}, invalidMarket("status must be trading, suspended or all")
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
	if status != "all" {
		q = q.Where("status = ?", status)
	}
	if market != "" {
		q = q.Where("market_type = ?", market)
	}
	if quoteAsset != "" {
		q = q.Where("quote_asset = ?", quoteAsset)
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

type MarketSyncSettingsPayload struct {
	MarketTypes        []string `json:"marketTypes"`
	QuoteAssets        []string `json:"quoteAssets"`
	SpotRESTBaseURL    string   `json:"spotRestBaseUrl"`
	USDMRESTBaseURL    string   `json:"usdmRestBaseUrl"`
	ProxyEnabled       bool     `json:"proxyEnabled"`
	ProxyURL           string   `json:"proxyUrl"`
	ProxyUsername      string   `json:"proxyUsername"`
	ProxyPassword      *string  `json:"proxyPassword"`
	ClearProxyPassword bool     `json:"clearProxyPassword"`
}

type MarketSyncSettingsView struct {
	Venue                   string   `json:"venue"`
	MarketTypes             []string `json:"marketTypes"`
	QuoteAssets             []string `json:"quoteAssets"`
	SpotRESTBaseURL         string   `json:"spotRestBaseUrl"`
	USDMRESTBaseURL         string   `json:"usdmRestBaseUrl"`
	ProxyEnabled            bool     `json:"proxyEnabled"`
	ProxyURL                string   `json:"proxyUrl"`
	ProxyUsername           string   `json:"proxyUsername"`
	ProxyPasswordConfigured bool     `json:"proxyPasswordConfigured"`
	ProxyLastCheckStatus    string   `json:"proxyLastCheckStatus"`
	ProxyLastCheckedAt      *string  `json:"proxyLastCheckedAt"`
	ProxyLastLatencyMillis  *int     `json:"proxyLastLatencyMs"`
	ProxyLastError          string   `json:"proxyLastError"`
	UpdatedByUserID         *int64   `json:"updatedByUserId"`
	CreatedAt               string   `json:"createdAt"`
	UpdatedAt               string   `json:"updatedAt"`
}

type MarketProxyStatusView struct {
	Mode          string `json:"mode"`
	Status        string `json:"status"`
	LatencyMillis *int   `json:"latencyMs"`
	CheckedAt     string `json:"checkedAt"`
	Message       string `json:"message"`
}

const marketMetadataWorkflowCode = "binance_market_metadata_sync"

func (a *App) GetMarketSyncSettings(ctx context.Context) (MarketSyncSettingsView, error) {
	row, err := a.loadMarketSyncSettings(ctx)
	if err != nil {
		return MarketSyncSettingsView{}, err
	}
	return serializeMarketSyncSettings(row)
}

func (a *App) UpdateMarketSyncSettings(ctx context.Context, userID int64, payload MarketSyncSettingsPayload) (MarketSyncSettingsView, error) {
	marketTypes, err := normalizeOptions(payload.MarketTypes, map[string]bool{"spot": true, "usd_m": true}, "marketTypes")
	if err != nil {
		return MarketSyncSettingsView{}, err
	}
	quoteAssets, err := normalizeOptions(payload.QuoteAssets, map[string]bool{"USDT": true, "USDC": true, "FDUSD": true}, "quoteAssets")
	if err != nil {
		return MarketSyncSettingsView{}, err
	}
	spotRESTBaseURL, err := normalizeBinanceRESTBaseURL(payload.SpotRESTBaseURL, "spotRestBaseUrl")
	if err != nil {
		return MarketSyncSettingsView{}, err
	}
	usdmRESTBaseURL, err := normalizeBinanceRESTBaseURL(payload.USDMRESTBaseURL, "usdmRestBaseUrl")
	if err != nil {
		return MarketSyncSettingsView{}, err
	}
	proxyURL, err := normalizeMarketProxyURL(payload.ProxyURL, payload.ProxyEnabled)
	if err != nil {
		return MarketSyncSettingsView{}, err
	}
	proxyUsername := strings.TrimSpace(payload.ProxyUsername)
	if len(proxyUsername) > 255 {
		return MarketSyncSettingsView{}, invalidMarket("proxyUsername is too long")
	}
	if payload.ProxyPassword != nil && len(*payload.ProxyPassword) > 4096 {
		return MarketSyncSettingsView{}, invalidMarket("proxyPassword is too long")
	}
	if payload.ProxyPassword != nil && payload.ClearProxyPassword {
		return MarketSyncSettingsView{}, invalidMarket("proxyPassword and clearProxyPassword cannot be used together")
	}
	marketsJSON, _ := json.Marshal(marketTypes)
	quotesJSON, _ := json.Marshal(quoteAssets)
	fields := map[string]any{
		"market_types": string(marketsJSON), "quote_assets": string(quotesJSON),
		"spot_rest_base_url": spotRESTBaseURL, "usdm_rest_base_url": usdmRESTBaseURL,
		"proxy_enabled": payload.ProxyEnabled, "proxy_url": proxyURL, "proxy_username": proxyUsername,
		"proxy_last_check_status": "unchecked", "proxy_last_checked_at": nil,
		"proxy_last_latency_ms": nil, "proxy_last_error": "",
		"updated_by_user_id": userID, "updated_at": time.Now().UTC(),
	}
	if payload.ProxyPassword != nil {
		fields["proxy_password_ciphertext"] = a.Cipher.Encrypt(*payload.ProxyPassword)
	} else if payload.ClearProxyPassword {
		fields["proxy_password_ciphertext"] = ""
	}
	result := a.dbWithContext(ctx).Model(&db.MarketSyncSettings{}).Where("id = 1").Updates(fields)
	if result.Error != nil {
		return MarketSyncSettingsView{}, result.Error
	}
	if result.RowsAffected != 1 {
		return MarketSyncSettingsView{}, ErrMarketResourceMissing
	}
	if err := a.applyMarketDataAccess(ctx, true); err != nil {
		return MarketSyncSettingsView{}, err
	}
	return a.GetMarketSyncSettings(ctx)
}

func (a *App) loadMarketSyncSettings(ctx context.Context) (db.MarketSyncSettings, error) {
	var row db.MarketSyncSettings
	if err := a.dbWithContext(ctx).First(&row, 1).Error; err != nil {
		return db.MarketSyncSettings{}, err
	}
	return row, nil
}

func normalizeBinanceRESTBaseURL(value, field string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Opaque != "" || parsed.User != nil || parsed.Port() != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return "", invalidMarket(field + " must be an official Binance HTTPS origin")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "binance.com" && !strings.HasSuffix(host, ".binance.com") &&
		host != "binance.vision" && !strings.HasSuffix(host, ".binance.vision") {
		return "", invalidMarket(field + " must be an official Binance HTTPS origin")
	}
	return "https://" + host, nil
}

func normalizeMarketProxyURL(value string, enabled bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if enabled {
			return "", invalidMarket("proxyUrl is required when proxy is enabled")
		}
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil {
		return "", invalidMarket("proxyUrl must be an HTTP or SOCKS5 origin")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Path != "" && parsed.Path != "/" || scheme != "http" && scheme != "socks5" {
		return "", invalidMarket("proxyUrl must be an HTTP or SOCKS5 origin")
	}
	host, portText := strings.ToLower(parsed.Hostname()), parsed.Port()
	port, portErr := strconv.Atoi(portText)
	if host == "" || strings.ContainsAny(host, " \t\r\n") || portErr != nil || port < 1 || port > 65535 {
		return "", invalidMarket("proxyUrl must include a valid host and port")
	}
	return scheme + "://" + net.JoinHostPort(host, portText), nil
}

func normalizeOptions(values []string, allowed map[string]bool, field string) ([]string, error) {
	if len(values) == 0 {
		return nil, invalidMarket(field + " must not be empty")
	}
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !allowed[value] {
			return nil, invalidMarket(field + " contains an unsupported value")
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result, nil
}

func serializeMarketSyncSettings(row db.MarketSyncSettings) (MarketSyncSettingsView, error) {
	view := MarketSyncSettingsView{
		Venue: row.Venue, SpotRESTBaseURL: row.SpotRESTBaseURL, USDMRESTBaseURL: row.USDMRESTBaseURL,
		ProxyEnabled: row.ProxyEnabled, ProxyURL: row.ProxyURL, ProxyUsername: row.ProxyUsername,
		ProxyPasswordConfigured: row.ProxyPasswordCiphertext != "", ProxyLastCheckStatus: row.ProxyLastCheckStatus,
		ProxyLastLatencyMillis: row.ProxyLastLatencyMillis, ProxyLastError: row.ProxyLastError,
		UpdatedByUserID: row.UpdatedByUserID,
		CreatedAt:       formatUTC(row.CreatedAt), UpdatedAt: formatUTC(row.UpdatedAt),
	}
	if row.ProxyLastCheckedAt != nil {
		value := formatUTC(*row.ProxyLastCheckedAt)
		view.ProxyLastCheckedAt = &value
	}
	if err := json.Unmarshal([]byte(row.MarketTypesJSON), &view.MarketTypes); err != nil {
		return MarketSyncSettingsView{}, err
	}
	if err := json.Unmarshal([]byte(row.QuoteAssetsJSON), &view.QuoteAssets); err != nil {
		return MarketSyncSettingsView{}, err
	}
	return view, nil
}

func (a *App) marketSyncRuntimeSettings(ctx context.Context) (MarketSyncSettingsView, string, error) {
	row, err := a.loadMarketSyncSettings(ctx)
	if err != nil {
		return MarketSyncSettingsView{}, "", err
	}
	view, err := serializeMarketSyncSettings(row)
	if err != nil || !row.ProxyEnabled {
		return view, "", err
	}
	parsed, err := url.Parse(row.ProxyURL)
	if err != nil {
		return MarketSyncSettingsView{}, "", err
	}
	password := ""
	if row.ProxyPasswordCiphertext != "" {
		password, err = a.Cipher.Decrypt(row.ProxyPasswordCiphertext)
		if err != nil {
			return MarketSyncSettingsView{}, "", err
		}
	}
	if row.ProxyUsername != "" || password != "" {
		parsed.User = url.UserPassword(row.ProxyUsername, password)
	}
	return view, parsed.String(), nil
}

// InitializeMarketDataAccess 在行情运行时启动前装载已持久化的网络配置。
func (a *App) InitializeMarketDataAccess(ctx context.Context) error {
	return a.applyMarketDataAccess(ctx, false)
}

func (a *App) applyMarketDataAccess(ctx context.Context, restartSubscriptions bool) error {
	if a.MarketData == nil {
		return nil
	}
	settings, proxyURL, err := a.marketSyncRuntimeSettings(ctx)
	if err != nil {
		return err
	}
	return a.MarketData.ConfigurePublicAccess(map[marketdata.MarketType]string{
		marketdata.MarketTypeSpot: settings.SpotRESTBaseURL,
		marketdata.MarketTypeUSDM: settings.USDMRESTBaseURL,
	}, proxyURL, restartSubscriptions)
}

func (a *App) CheckMarketProxy(ctx context.Context) (MarketProxyStatusView, error) {
	settings, proxyURL, err := a.marketSyncRuntimeSettings(ctx)
	if err != nil {
		return MarketProxyStatusView{}, err
	}
	mode := "direct"
	if settings.ProxyEnabled {
		mode = "proxy"
	}
	checkedAt := time.Now().UTC()
	status := "healthy"
	message := "Binance Spot 连接正常"
	var latencyMillis *int
	if a.MarketData == nil {
		status, message = "failed", "行情运行时未启用"
	} else {
		latency, checkErr := a.MarketData.CheckConnectivity(ctx, map[marketdata.MarketType]string{
			marketdata.MarketTypeSpot: settings.SpotRESTBaseURL,
			marketdata.MarketTypeUSDM: settings.USDMRESTBaseURL,
		}, proxyURL, marketdata.MarketTypeSpot)
		if checkErr != nil {
			status, message = "failed", "无法访问 Binance Spot，请检查代理地址与网络"
		} else {
			value := int(latency.Milliseconds())
			latencyMillis = &value
		}
	}
	fields := map[string]any{
		"proxy_last_check_status": status, "proxy_last_checked_at": checkedAt,
		"proxy_last_latency_ms": nil, "proxy_last_error": "",
	}
	if latencyMillis != nil {
		fields["proxy_last_latency_ms"] = *latencyMillis
	}
	if status == "failed" {
		fields["proxy_last_error"] = message
	}
	if err := a.dbWithContext(ctx).Model(&db.MarketSyncSettings{}).Where("id = 1").Updates(fields).Error; err != nil {
		return MarketProxyStatusView{}, err
	}
	return MarketProxyStatusView{
		Mode: mode, Status: status, LatencyMillis: latencyMillis,
		CheckedAt: formatUTC(checkedAt), Message: message,
	}, nil
}

func (a *App) GetMarketSyncStatus(ctx context.Context) (M, error) {
	status := M{"lastSyncAt": nil, "nextSyncAt": nil, "lastExecution": nil}
	var state db.WorkflowRuntimeState
	if err := a.dbWithContext(ctx).Where("workflow_code = ?", marketMetadataWorkflowCode).First(&state).Error; err != nil {
		return status, err
	}
	if state.ActiveWorkflowDefinitionID == nil {
		return status, nil
	}
	var entry db.WorkflowRuntimeEntry
	if err := a.dbWithContext(ctx).Where(
		"workflow_definition_id = ? AND entry_key = ?", *state.ActiveWorkflowDefinitionID, "market.metadata.hourly",
	).First(&entry).Error; err == nil && entry.NextRunAt != nil {
		status["nextSyncAt"] = formatUTC(*entry.NextRunAt)
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var execution db.WorkflowExecution
	err := a.dbWithContext(ctx).Where("workflow_definition_id = ?", *state.ActiveWorkflowDefinitionID).
		Order("id DESC").First(&execution).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status, nil
	}
	if err != nil {
		return nil, err
	}
	status["lastExecution"] = a.serializeExecutionSummary(&execution)
	if execution.Status == "success" && execution.FinishedAt != nil {
		status["lastSyncAt"] = formatUTC(*execution.FinishedAt)
	}
	return status, nil
}

func (a *App) RunMarketMetadataSync(ctx context.Context, userID int64, idempotencyKey string) (M, error) {
	normalizedKey, err := normalizeIdempotencyKey(idempotencyKey)
	if err != nil {
		return nil, invalidMarket(err.Error())
	}
	var state db.WorkflowRuntimeState
	if err := a.dbWithContext(ctx).Where("workflow_code = ?", marketMetadataWorkflowCode).First(&state).Error; err != nil {
		return nil, err
	}
	if state.ActiveWorkflowDefinitionID == nil {
		return nil, bizErr("Market metadata workflow is not active")
	}
	executions, err := a.RunManualStarts(*state.ActiveWorkflowDefinitionID, []string{"market.metadata.manual"}, userID, M{}, normalizedKey)
	if err != nil {
		return nil, err
	}
	return executions[0], nil
}

// PublishMarketCandleClosed 把首次闭合 K 线写入 Outbox，后续策略只由事件工作流编排。
func (a *App) PublishMarketCandleClosed(ctx context.Context, candle marketdata.Candle) error {
	var instrument db.MarketInstrument
	if err := a.dbWithContext(ctx).Where("id = ? AND venue = ?", candle.InstrumentID, candle.Venue).First(&instrument).Error; err != nil {
		return err
	}
	payload := M{
		"instrumentId": candle.InstrumentID.String(), "venue": string(candle.Venue),
		"market": instrument.Market, "symbol": instrument.NativeSymbol,
		"baseAsset": instrument.BaseAsset, "quoteAsset": instrument.QuoteAsset,
		"interval": string(candle.Interval), "openTime": formatUTC(candle.OpenTime),
		"closeTime": formatUTC(candle.CloseTime), "open": candle.Open.String(),
		"high": candle.High.String(), "low": candle.Low.String(), "close": candle.Close.String(),
		"baseVolume": candle.BaseVolume.String(),
	}
	_, err := a.publishDomainEventWithDB(
		a.dbWithContext(ctx), "market.candle.closed", "market_candle", candle.InstrumentID.String(),
		payload, M{}, nil, nil,
	)
	return err
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
