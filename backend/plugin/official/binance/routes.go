package binance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"gorm.io/gorm/clause"
)

func registerRoutes(registrar sdk.Registrar, runtime *binanceRuntime) error {
	for _, route := range []struct {
		desc    sdk.RouteDescriptor
		handler sdk.ScopedRouteHandler
	}{
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/instruments", Scope: sdk.ScopeSystem}, runtime.handleInstruments},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/candles", Scope: sdk.ScopeSystem}, runtime.handleCandles},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/candles/indicators", Scope: sdk.ScopeSystem}, runtime.handleIndicators},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/candles/stream", Scope: sdk.ScopeSystem, WebSocket: true}, runtime.handleCandleStream},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/orders", Scope: sdk.ScopeSystem}, runtime.handleOrders},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/live-accounts", Scope: sdk.ScopeSystem}, runtime.handleLiveAccounts},
		{sdk.RouteDescriptor{Method: "PUT", Pattern: "/live-accounts/:account", Scope: sdk.ScopeSystem}, runtime.handleLiveAccountRelease},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/paper", Scope: sdk.ScopeResult}, runtime.handlePaperResult},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/paper/export", Scope: sdk.ScopeResult, Action: "export"}, runtime.handlePaperExport},
	} {
		if err := registrar.Route(route.desc, route.handler); err != nil {
			return err
		}
	}
	return nil
}

func (q *binanceRuntime) handleLiveAccounts(c *gin.Context, scope sdk.RouteScope) {
	if !validSystemScope(scope) {
		writeProblem(c, http.StatusForbidden, "invalid Binance scope")
		return
	}
	var rows []liveAccountRelease
	if err := q.db.WithContext(c.Request.Context()).Order("account, market").Find(&rows).Error; err != nil {
		writeProblem(c, http.StatusInternalServerError, "list Binance live account releases failed")
		return
	}
	items := make([]map[string]any, len(rows))
	for index, row := range rows {
		items[index] = map[string]any{"account": row.Account, "market": row.Market, "enabled": row.Enabled, "confirmedBy": row.ConfirmedBy, "confirmedAt": row.ConfirmedAt.UTC().Format(time.RFC3339Nano), "updatedAt": row.UpdatedAt.UTC().Format(time.RFC3339Nano)}
	}
	writeOK(c, map[string]any{"items": items})
}

func (q *binanceRuntime) handleLiveAccountRelease(c *gin.Context, scope sdk.RouteScope) {
	principal, ok := scope.(sdk.SystemScope)
	account := strings.TrimSpace(c.Param("account"))
	var payload struct {
		Market       string `json:"market"`
		Enabled      bool   `json:"enabled"`
		Confirmation string `json:"confirmation"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&payload)
	trailingErr := decoder.Decode(&struct{}{})
	if !ok || principal.PluginID != pluginID || principal.UserID <= 0 || !accountIDPattern.MatchString(account) || decodeErr != nil || !errors.Is(trailingErr, io.EOF) {
		writeProblem(c, http.StatusBadRequest, "invalid Binance live account release")
		return
	}
	payload.Market = strings.ToLower(strings.TrimSpace(payload.Market))
	if payload.Market != "spot" && payload.Market != "usdm" {
		writeProblem(c, http.StatusBadRequest, "invalid Binance live account market")
		return
	}
	if payload.Enabled && payload.Confirmation != "ENABLE LIVE "+account+" "+payload.Market {
		writeProblem(c, http.StatusBadRequest, "Binance live account confirmation does not match")
		return
	}
	now := time.Now().UTC()
	release := liveAccountRelease{Account: account, Market: payload.Market, Enabled: payload.Enabled, ConfirmedBy: principal.UserID, ConfirmedAt: now, UpdatedAt: now}
	if err := q.db.WithContext(c.Request.Context()).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "account"}, {Name: "market"}}, DoUpdates: clause.AssignmentColumns([]string{"enabled", "confirmed_by", "confirmed_at", "updated_at"})}).Create(&release).Error; err != nil {
		writeProblem(c, http.StatusInternalServerError, "persist Binance live account release failed")
		return
	}
	slog.Info("Binance live account release changed", "component", "plugin.binance", "account", account, "market", payload.Market, "enabled", payload.Enabled, "confirmed_by", principal.UserID)
	writeOK(c, map[string]any{"account": account, "market": payload.Market, "enabled": payload.Enabled, "confirmedAt": now.Format(time.RFC3339Nano)})
}

func (q *binanceRuntime) handleOrders(c *gin.Context, scope sdk.RouteScope) {
	if !validSystemScope(scope) {
		writeProblem(c, http.StatusForbidden, "invalid Binance scope")
		return
	}
	query := q.db.WithContext(c.Request.Context()).Order("created_at DESC, id DESC").Limit(queryLimit(c, 100, 500))
	if account := strings.TrimSpace(c.Query("account")); account != "" {
		query = query.Where("account = ?", account)
	}
	if instrument := strings.TrimSpace(c.Query("instrument")); instrument != "" {
		query = query.Where("instrument = ?", strings.ToUpper(instrument))
	}
	var rows []tradingOrder
	if err := query.Find(&rows).Error; err != nil {
		writeProblem(c, http.StatusInternalServerError, "list Binance orders failed")
		return
	}
	items := make([]json.RawMessage, len(rows))
	for i := range rows {
		items[i] = marshalOrder(rows[i])
	}
	writeOK(c, map[string]any{"items": items})
}

func (q *binanceRuntime) handleInstruments(c *gin.Context, scope sdk.RouteScope) {
	if !validSystemScope(scope) {
		writeProblem(c, http.StatusForbidden, "invalid Binance scope")
		return
	}
	limit := queryLimit(c, 500, 10000)
	items, err := (marketDataProvider{runtime: q}).Instruments(c.Request.Context(), sdk.InstrumentQuery{Markets: queryValues(c, "market"), Limit: limit})
	if err != nil {
		writeProblem(c, http.StatusInternalServerError, err.Error())
		return
	}
	result := make([]map[string]any, len(items))
	for i, item := range items {
		result[i] = map[string]any{"market": item.Market, "symbol": item.Symbol, "baseAsset": item.BaseAsset, "quoteAsset": item.QuoteAsset, "status": item.Status, "priceTick": item.PriceTick.String(), "quantityStep": item.QuantityStep.String(), "minQuantity": item.MinQuantity.String(), "updatedAt": item.UpdatedAt.Format(time.RFC3339Nano)}
	}
	writeOK(c, map[string]any{"items": result})
}

func (q *binanceRuntime) handleCandles(c *gin.Context, scope sdk.RouteScope) {
	if !validSystemScope(scope) || !binanceQueryKeys(c, "market", "instrument", "interval", "before", "startTime", "endTime", "limit") {
		writeProblem(c, http.StatusForbidden, "invalid Binance scope")
		return
	}
	before, err := parseOptionalUTCTime(c.Query("before"))
	if err != nil {
		writeProblem(c, http.StatusBadRequest, "before must use RFC3339 UTC")
		return
	}
	startTime, err := parseOptionalUTCTime(c.Query("startTime"))
	if err != nil {
		writeProblem(c, http.StatusBadRequest, "startTime must use RFC3339 UTC")
		return
	}
	endTime, err := parseOptionalUTCTime(c.Query("endTime"))
	if err != nil {
		writeProblem(c, http.StatusBadRequest, "endTime must use RFC3339 UTC")
		return
	}
	if !before.IsZero() {
		endTime = before
	}
	limit := queryLimit(c, 500, 5000)
	items, err := (marketDataProvider{runtime: q}).Candles(c.Request.Context(), sdk.CandleQuery{Market: c.Query("market"), Instrument: c.Query("instrument"), Interval: c.Query("interval"), StartTime: startTime, EndTime: endTime, Limit: limit + 1})
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	result := make([]map[string]any, len(items))
	for i, item := range items {
		result[i] = map[string]any{"venue": "binance", "market": c.Query("market"), "instrument": strings.ToUpper(c.Query("instrument")), "interval": c.Query("interval"), "openTime": item.OpenTime.Format(time.RFC3339Nano), "closeTime": item.CloseTime.Format(time.RFC3339Nano), "open": item.Open.String(), "high": item.High.String(), "low": item.Low.String(), "close": item.Close.String(), "volume": item.Volume.String()}
	}
	nextBefore := ""
	if hasMore && len(result) > 0 {
		nextBefore = result[0]["openTime"].(string)
	}
	writeOK(c, map[string]any{"items": result, "nextBefore": nextBefore, "hasMore": hasMore})
}

func parseOptionalUTCTime(raw string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, nil
	}
	value, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, err
	}
	_, offset := value.Zone()
	if offset != 0 {
		return time.Time{}, errors.New("time must use UTC")
	}
	return value.UTC(), nil
}

func binanceQueryKeys(c *gin.Context, allowed ...string) bool {
	keys := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		keys[key] = true
	}
	for key, values := range c.Request.URL.Query() {
		if !keys[key] || len(values) != 1 {
			return false
		}
	}
	return true
}

func (q *binanceRuntime) handleIndicators(c *gin.Context, scope sdk.RouteScope) {
	if !validSystemScope(scope) || !binanceQueryKeys(c, "market", "instrument", "interval", "startTime", "endTime", "limit") {
		writeProblem(c, http.StatusBadRequest, "invalid Binance indicator query")
		return
	}
	startTime, err := parseOptionalUTCTime(c.Query("startTime"))
	if err != nil {
		writeProblem(c, http.StatusBadRequest, "startTime must use RFC3339 UTC")
		return
	}
	endTime, err := parseOptionalUTCTime(c.Query("endTime"))
	if err != nil {
		writeProblem(c, http.StatusBadRequest, "endTime must use RFC3339 UTC")
		return
	}
	items, err := (marketDataProvider{runtime: q}).Candles(c.Request.Context(), sdk.CandleQuery{Market: c.Query("market"), Instrument: c.Query("instrument"), Interval: c.Query("interval"), StartTime: startTime, EndTime: endTime, Limit: queryLimit(c, 500, 1_000_001)})
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	writeOK(c, map[string]any{"items": calculateBinanceIndicators(items)})
}

var binanceCandleWSUpgrader = websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 4096, CheckOrigin: checkBinanceCandleOrigin, Subprotocols: []string{"coinsphere.plugin.official.binance.v1"}}

func checkBinanceCandleOrigin(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	if len(origins) != 1 {
		return false
	}
	origin, err := url.Parse(strings.TrimSpace(origins[0]))
	if err != nil || origin.Scheme != "http" && origin.Scheme != "https" || origin.Host == "" ||
		origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if forwarded := r.Header.Values("X-Forwarded-Proto"); len(forwarded) == 1 {
		scheme = strings.ToLower(strings.TrimSpace(forwarded[0]))
		if scheme != "http" && scheme != "https" {
			return false
		}
	} else if len(r.Header.Values("X-Forwarded-Proto")) > 1 {
		return false
	}
	requestOrigin, err := url.Parse(scheme + "://" + r.Host)
	if err != nil || requestOrigin.Host == "" {
		return false
	}
	originPort, originPortOK := binanceOriginPort(origin, scheme)
	requestPort, requestPortOK := binanceOriginPort(requestOrigin, scheme)
	return originPortOK && requestPortOK && strings.EqualFold(origin.Scheme, requestOrigin.Scheme) &&
		strings.EqualFold(origin.Hostname(), requestOrigin.Hostname()) && originPort == requestPort
}

func binanceOriginPort(value *url.URL, scheme string) (string, bool) {
	if port := value.Port(); port != "" {
		parsed, err := strconv.Atoi(port)
		if err != nil || parsed < 1 || parsed > 65535 {
			return "", false
		}
		return port, true
	}
	if scheme == "https" {
		return "443", true
	}
	return "80", true
}

func (q *binanceRuntime) handleCandleStream(c *gin.Context, scope sdk.RouteScope) {
	if !validSystemScope(scope) || !binanceQueryKeys(c, "market", "instrument", "interval") {
		writeProblem(c, http.StatusForbidden, "invalid Binance scope")
		return
	}
	market, instrument, interval := strings.ToLower(strings.TrimSpace(c.Query("market"))), strings.ToUpper(strings.TrimSpace(c.Query("instrument"))), strings.TrimSpace(c.Query("interval"))
	if market != "spot" && market != "usdm" || !instrumentPattern.MatchString(instrument) {
		writeProblem(c, http.StatusBadRequest, "invalid Binance candle stream parameters")
		return
	}
	if _, ok := binanceIntervals[interval]; !ok {
		writeProblem(c, http.StatusBadRequest, "unsupported Binance candle interval")
		return
	}
	connection, err := binanceCandleWSUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(1024)
	const pongWait = 70 * time.Second
	_ = connection.SetReadDeadline(time.Now().Add(pongWait))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(pongWait))
	})
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	stop := make(chan struct{})
	go func() {
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				close(stop)
				return
			}
		}
	}()
	updates := make(chan struct {
		candle binanceCandle
		closed bool
	}, 8)
	config := binanceCandleStreamConfig{Market: market, Instrument: instrument, Intervals: []string{interval}}
	historyRows, _ := q.fetchBinanceKlinePage(ctx, binanceSeriesConfig{Market: market, Instrument: instrument, Interval: interval}, time.Now().UTC(), 120)
	history := make([]sdk.Candle, len(historyRows))
	for index := range historyRows {
		history[index] = binanceSDKCandle(historyRows[index])
	}
	sort.Slice(history, func(i, j int) bool { return history[i].OpenTime.Before(history[j].OpenTime) })
	go func() {
		_ = q.hub.subscribeBrowser(ctx, config, func(candle binanceCandle, closed bool) {
			select {
			case updates <- struct {
				candle binanceCandle
				closed bool
			}{candle: candle, closed: closed}:
			default:
			}
		})
	}()
	pingTicker := time.NewTicker(54 * time.Second)
	defer pingTicker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-pingTicker.C:
			if err := connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
				return
			}
		case update := <-updates:
			candle := update.candle
			sdkCandle := binanceSDKCandle(candle)
			index := sort.Search(len(history), func(index int) bool {
				return !history[index].OpenTime.Before(sdkCandle.OpenTime)
			})
			if index < len(history) && history[index].OpenTime.Equal(sdkCandle.OpenTime) {
				history[index] = sdkCandle
			} else {
				history = append(history, sdk.Candle{})
				copy(history[index+1:], history[index:])
				history[index] = sdkCandle
			}
			if len(history) > 120 {
				history = history[len(history)-120:]
			}
			indicatorRows := calculateBinanceIndicators(history)
			indicator := indicatorRows[len(indicatorRows)-1]
			if err := connection.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
				return
			}
			writeErr := connection.WriteJSON(map[string]any{"type": "kline", "version": 1, "occurredAt": time.Now().UTC().Format(time.RFC3339Nano), "data": map[string]any{"market": market, "instrument": instrument, "interval": interval, "openTime": candle.OpenTime.UTC().Format(time.RFC3339Nano), "closeTime": candle.CloseTime.UTC().Format(time.RFC3339Nano), "open": candle.Open.String(), "high": candle.High.String(), "low": candle.Low.String(), "close": candle.Close.String(), "volume": candle.Volume.String(), "closed": update.closed, "indicators": indicator}})
			_ = connection.SetWriteDeadline(time.Time{})
			if writeErr != nil {
				return
			}
		}
	}
}

func validSystemScope(scope sdk.RouteScope) bool {
	value, ok := scope.(sdk.SystemScope)
	return ok && value.PluginID == pluginID && value.UserID > 0
}
func queryValues(c *gin.Context, key string) []string {
	if value := strings.TrimSpace(c.Query(key)); value != "" {
		return []string{value}
	}
	return nil
}
func queryLimit(c *gin.Context, fallback, maximum int) int {
	value, err := strconv.Atoi(c.Query("limit"))
	if err != nil || value < 1 || value > maximum {
		return fallback
	}
	return value
}
func writeOK(c *gin.Context, data any) {
	writeJSON(c, http.StatusOK, map[string]any{"code": 200, "msg": "success", "data": data})
}
func writeProblem(c *gin.Context, status int, detail string) {
	c.Header("Content-Type", "application/problem+json")
	writeJSON(c, status, map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "detail": detail, "requestId": c.Writer.Header().Get("X-Request-ID")})
}
func writeJSON(c *gin.Context, status int, value any) {
	if c.Writer.Header().Get("Content-Type") == "" {
		c.Header("Content-Type", "application/json; charset=utf-8")
	}
	c.Status(status)
	encoder := json.NewEncoder(c.Writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}
