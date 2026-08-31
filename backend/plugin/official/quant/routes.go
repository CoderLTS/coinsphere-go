package quant

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/gin-gonic/gin"
)

func (q *quantRuntime) handleQuantInstruments(c *gin.Context, scope sdk.RouteScope) {
	if !validQuantSystemScope(scope) || !quantQueryKeys(c.Request, "market") {
		writeQuantProblem(c, http.StatusBadRequest, "invalid Quant instrument query")
		return
	}
	market := strings.ToLower(strings.TrimSpace(c.Query("market")))
	if market != "spot" && market != "usdm" {
		writeQuantProblem(c, http.StatusBadRequest, "market must be spot or usdm")
		return
	}
	var instruments []quantInstrument
	if err := q.db.WithContext(c.Request.Context()).Where("market = ?", market).Order("symbol").Limit(5000).Find(&instruments).Error; err != nil {
		writeQuantProblem(c, http.StatusInternalServerError, "list Quant instruments failed")
		return
	}
	items := make([]map[string]any, len(instruments))
	for index, instrument := range instruments {
		items[index] = map[string]any{
			"market": instrument.Market, "symbol": instrument.Symbol, "baseAsset": instrument.BaseAsset,
			"quoteAsset": instrument.QuoteAsset, "status": instrument.Status,
			"priceTick": instrument.PriceTick.String(), "quantityStep": instrument.QuantityStep.String(),
			"minQuantity": instrument.MinQuantity.String(), "updatedAt": instrument.UpdatedAt.UTC().Format(time.RFC3339Nano),
		}
	}
	writeQuantOK(c, map[string]any{"items": items})
}

func (q *quantRuntime) handleQuantCandles(c *gin.Context, scope sdk.RouteScope) {
	if !validQuantSystemScope(scope) || !quantQueryKeys(c.Request, "market", "instrument", "interval", "before", "limit") {
		writeQuantProblem(c, http.StatusBadRequest, "invalid Quant candle query")
		return
	}
	config, err := parseQuantSeriesConfig(mustMarshal(map[string]any{
		"market": c.Query("market"), "instrument": c.Query("instrument"), "interval": c.Query("interval"),
	}))
	if err != nil {
		writeQuantProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := quantQueryLimit(c.Request, 200, 500)
	if err != nil {
		writeQuantProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	end := time.Now().UTC().Add(quantIntervals[config.Interval])
	if raw := strings.TrimSpace(c.Query("before")); raw != "" {
		end, err = parseQuantUTCTime(raw)
		if err != nil {
			writeQuantProblem(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	candles, err := q.loadQuantCandles(c.Request.Context(), config, time.Time{}, end, limit)
	if err != nil {
		writeQuantProblem(c, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, len(candles))
	for index, candle := range candles {
		items[index] = quantCandleData(candle)
	}
	writeQuantOK(c, map[string]any{"items": items})
}

func (q *quantRuntime) handleQuantStrategies(c *gin.Context, scope sdk.RouteScope) {
	if !validQuantSystemScope(scope) || !quantQueryKeys(c.Request) {
		writeQuantProblem(c, http.StatusBadRequest, "invalid Quant strategy query")
		return
	}
	strategies := q.registry.Strategies()
	items := make([]map[string]any, 0, len(strategies))
	for _, strategy := range strategies {
		if !strings.HasPrefix(strategy.ID, quantPluginID+".") {
			continue
		}
		items = append(items, map[string]any{
			"id": strategy.ID, "version": strategy.Version, "name": strategy.Name,
			"minimumLookback": strategy.MinimumLookback, "parameterSchema": json.RawMessage(strategy.ParameterSchema),
		})
	}
	writeQuantOK(c, map[string]any{"items": items})
}

func (q *quantRuntime) handleQuantBacktests(c *gin.Context, scope sdk.RouteScope) {
	if !validQuantSystemScope(scope) || !quantQueryKeys(c.Request, "market", "instrument", "limit") {
		writeQuantProblem(c, http.StatusBadRequest, "invalid Quant backtest query")
		return
	}
	limit, err := quantQueryLimit(c.Request, 50, 200)
	if err != nil {
		writeQuantProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	query := q.db.WithContext(c.Request.Context()).Order("created_at DESC, id DESC").Limit(limit)
	if market := strings.ToLower(strings.TrimSpace(c.Query("market"))); market != "" {
		if market != "spot" && market != "usdm" {
			writeQuantProblem(c, http.StatusBadRequest, "market must be spot or usdm")
			return
		}
		query = query.Where("market = ?", market)
	}
	if instrument := strings.ToUpper(strings.TrimSpace(c.Query("instrument"))); instrument != "" {
		if !quantInstrumentPattern.MatchString(instrument) {
			writeQuantProblem(c, http.StatusBadRequest, "instrument is invalid")
			return
		}
		query = query.Where("instrument = ?", instrument)
	}
	var backtests []quantBacktest
	if err := query.Find(&backtests).Error; err != nil {
		writeQuantProblem(c, http.StatusInternalServerError, "list Quant backtests failed")
		return
	}
	items := make([]map[string]any, len(backtests))
	for index, backtest := range backtests {
		items[index] = quantBacktestView(backtest)
	}
	writeQuantOK(c, map[string]any{"items": items})
}

func validQuantSystemScope(scope sdk.RouteScope) bool {
	value, ok := scope.(sdk.SystemScope)
	return ok && value.PluginID == quantPluginID && value.UserID > 0
}

func quantQueryKeys(r *http.Request, allowed ...string) bool {
	valid := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		valid[key] = true
	}
	for key, values := range r.URL.Query() {
		if !valid[key] || len(values) != 1 {
			return false
		}
	}
	return true
}

func quantQueryLimit(r *http.Request, fallback, maximum int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maximum {
		return 0, errors.New("limit is outside the allowed range")
	}
	return value, nil
}

func writeQuantOK(c *gin.Context, data any) {
	writeQuantJSON(c, http.StatusOK, map[string]any{"code": 200, "msg": "success", "data": data})
}

func writeQuantProblem(c *gin.Context, status int, detail string) {
	c.Header("Content-Type", "application/problem+json")
	writeQuantJSON(c, status, map[string]any{
		"type": "about:blank", "title": http.StatusText(status), "status": status, "detail": detail,
		"requestId": c.Writer.Header().Get("X-Request-ID"),
	})
}

func writeQuantJSON(c *gin.Context, status int, value any) {
	if c.Writer.Header().Get("Content-Type") == "" {
		c.Header("Content-Type", "application/json; charset=utf-8")
	}
	c.Status(status)
	encoder := json.NewEncoder(c.Writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}
