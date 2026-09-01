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
	"gorm.io/gorm"
)

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
	if !validQuantSystemScope(scope) || !quantQueryKeys(c.Request, "venue", "market", "instrument", "limit") {
		writeQuantProblem(c, http.StatusBadRequest, "invalid Quant backtest query")
		return
	}
	limit, err := quantQueryLimit(c.Request, 50, 200)
	if err != nil {
		writeQuantProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	query := q.db.WithContext(c.Request.Context()).Omit("detail").Order("created_at DESC, id DESC").Limit(limit)
	if venue := strings.ToLower(strings.TrimSpace(c.Query("venue"))); venue != "" {
		if !quantProviderPattern.MatchString(venue) {
			writeQuantProblem(c, http.StatusBadRequest, "venue is invalid")
			return
		}
		query = query.Where("venue = ?", venue)
	}
	if market := strings.ToLower(strings.TrimSpace(c.Query("market"))); market != "" {
		if market != "spot" && market != "usdm" {
			writeQuantProblem(c, http.StatusBadRequest, "market must be spot or usdm")
			return
		}
		query = query.Where("market = ?", market)
	}
	if instrument := strings.ToUpper(strings.TrimSpace(c.Query("instrument"))); instrument != "" {
		if !quantSeriesInstrumentPattern.MatchString(instrument) {
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

func (q *quantRuntime) handleQuantSignals(c *gin.Context, scope sdk.RouteScope) {
	if !validQuantSystemScope(scope) || !quantQueryKeys(c.Request, "venue", "market", "instrument", "status", "limit") {
		writeQuantProblem(c, http.StatusBadRequest, "invalid Quant signal query")
		return
	}
	limit, err := quantQueryLimit(c.Request, 100, 200)
	if err != nil {
		writeQuantProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	query := q.db.WithContext(c.Request.Context()).Order("created_at DESC, id DESC").Limit(limit)
	for column, value := range map[string]string{
		"venue":      strings.ToLower(strings.TrimSpace(c.Query("venue"))),
		"market":     strings.ToLower(strings.TrimSpace(c.Query("market"))),
		"instrument": strings.ToUpper(strings.TrimSpace(c.Query("instrument"))),
		"status":     strings.ToLower(strings.TrimSpace(c.Query("status"))),
	} {
		if value != "" {
			query = query.Where(column+" = ?", value)
		}
	}
	var signals []quantSignal
	if err := query.Find(&signals).Error; err != nil {
		writeQuantProblem(c, http.StatusInternalServerError, "list Quant signals failed")
		return
	}
	items := make([]map[string]any, len(signals))
	for index, signal := range signals {
		items[index] = map[string]any{
			"id": signal.ID, "strategyId": signal.StrategyID, "strategyVersion": signal.StrategyVersion,
			"venue": signal.Venue, "market": signal.Market, "instrument": signal.Instrument,
			"target": signal.Target.String(), "status": signal.Status,
			"evaluatedAt": signal.EvaluatedAt.UTC().Format(time.RFC3339Nano),
			"createdAt":   signal.CreatedAt.UTC().Format(time.RFC3339Nano),
		}
	}
	writeQuantOK(c, map[string]any{"items": items})
}

func (q *quantRuntime) handleQuantBacktest(c *gin.Context, scope sdk.RouteScope) {
	if !validQuantSystemScope(scope) || !quantQueryKeys(c.Request) {
		writeQuantProblem(c, http.StatusBadRequest, "invalid Quant backtest detail request")
		return
	}
	backtestID, err := quantPathInt64(c.Param("backtestId"))
	if err != nil {
		writeQuantProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	var backtest quantBacktest
	if err := q.db.WithContext(c.Request.Context()).Select("detail").First(&backtest, backtestID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeQuantProblem(c, http.StatusNotFound, "Quant backtest not found")
			return
		}
		writeQuantProblem(c, http.StatusInternalServerError, "load Quant backtest detail failed")
		return
	}
	writeQuantOK(c, json.RawMessage(backtest.Detail))
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
