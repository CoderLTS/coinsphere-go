package binance

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
)

func registerRoutes(registrar sdk.Registrar, runtime *binanceRuntime) error {
	for _, route := range []struct {
		desc    sdk.RouteDescriptor
		handler sdk.ScopedRouteHandler
	}{
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/instruments", Scope: sdk.ScopeSystem}, runtime.handleInstruments},
		{sdk.RouteDescriptor{Method: "GET", Pattern: "/candles", Scope: sdk.ScopeSystem}, runtime.handleCandles},
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
	if !validSystemScope(scope) {
		writeProblem(c, http.StatusForbidden, "invalid Binance scope")
		return
	}
	before := time.Time{}
	if raw := strings.TrimSpace(c.Query("before")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeProblem(c, http.StatusBadRequest, "before must use RFC3339 UTC")
			return
		}
		before = parsed.UTC()
	}
	items, err := (marketDataProvider{runtime: q}).Candles(c.Request.Context(), sdk.CandleQuery{Market: c.Query("market"), Instrument: c.Query("instrument"), Interval: c.Query("interval"), EndTime: before, Limit: queryLimit(c, 500, 5000)})
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	result := make([]map[string]any, len(items))
	for i, item := range items {
		result[i] = map[string]any{"venue": "binance", "market": c.Query("market"), "instrument": strings.ToUpper(c.Query("instrument")), "interval": c.Query("interval"), "openTime": item.OpenTime.Format(time.RFC3339Nano), "closeTime": item.CloseTime.Format(time.RFC3339Nano), "open": item.Open.String(), "high": item.High.String(), "low": item.Low.String(), "close": item.Close.String(), "volume": item.Volume.String()}
	}
	writeOK(c, map[string]any{"items": result})
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
