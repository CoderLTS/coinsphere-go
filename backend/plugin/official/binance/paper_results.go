package binance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type paperResultScope struct {
	WorkflowID          int64  `json:"workflowId"`
	PaperNodeInstanceID string `json:"paperNodeInstanceId"`
}

type paperResultFilters struct {
	Market     string `json:"market"`
	Instrument string `json:"instrument"`
	Status     string `json:"status"`
}

func (q *binanceRuntime) handlePaperResult(c *gin.Context, routeScope sdk.RouteScope) {
	scope, filters, ok := parsePaperResultScope(routeScope, c.Request)
	if !ok {
		writeProblem(c, http.StatusBadRequest, "invalid Binance Paper result scope")
		return
	}
	orders, accounts, err := q.paperResult(c.Request.Context(), scope, filters)
	if err != nil {
		writeProblem(c, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(c, map[string]any{"orders": orders, "accounts": accounts})
}

func (q *binanceRuntime) handlePaperExport(c *gin.Context, routeScope sdk.RouteScope) {
	scope, filters, ok := parsePaperResultScope(routeScope, c.Request)
	if !ok {
		writeProblem(c, http.StatusBadRequest, "invalid Binance Paper export scope")
		return
	}
	orders, accounts, err := q.paperResult(c.Request.Context(), scope, filters)
	if err != nil {
		writeProblem(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.Header("Content-Disposition", `attachment; filename="binance-paper-results.json"`)
	writeJSON(c, http.StatusOK, map[string]any{"schemaVersion": 1, "exportedAt": time.Now().UTC().Format(time.RFC3339Nano), "orders": orders, "accounts": accounts})
}

func parsePaperResultScope(routeScope sdk.RouteScope, request *http.Request) (paperResultScope, paperResultFilters, bool) {
	scope, ok := routeScope.(sdk.ResultScope)
	if !ok || scope.PluginID != pluginID || scope.PageKey != "paper" {
		return paperResultScope{}, paperResultFilters{}, false
	}
	var fixed paperResultScope
	var filters paperResultFilters
	if json.Unmarshal(scope.Scope, &fixed) != nil || json.Unmarshal(scope.Filters, &filters) != nil || fixed.WorkflowID <= 0 || strings.TrimSpace(fixed.PaperNodeInstanceID) == "" {
		return paperResultScope{}, paperResultFilters{}, false
	}
	for key, values := range request.URL.Query() {
		if key != "market" && key != "instrument" && key != "status" || len(values) != 1 {
			return paperResultScope{}, paperResultFilters{}, false
		}
	}
	filters.Market = strings.ToLower(strings.TrimSpace(firstNonEmpty(request.URL.Query().Get("market"), filters.Market)))
	filters.Instrument = strings.ToUpper(strings.TrimSpace(firstNonEmpty(request.URL.Query().Get("instrument"), filters.Instrument)))
	filters.Status = strings.ToLower(strings.TrimSpace(firstNonEmpty(request.URL.Query().Get("status"), filters.Status)))
	if filters.Market != "" && filters.Market != "spot" && filters.Market != "usdm" ||
		filters.Instrument != "" && !instrumentPattern.MatchString(filters.Instrument) {
		return paperResultScope{}, paperResultFilters{}, false
	}
	return fixed, filters, true
}

func firstNonEmpty(first, fallback string) string {
	if strings.TrimSpace(first) != "" {
		return first
	}
	return fallback
}

func (q *binanceRuntime) paperResult(ctx context.Context, scope paperResultScope, filters paperResultFilters) ([]map[string]any, []map[string]any, error) {
	query := q.db.WithContext(ctx).Where("workflow_id = ? AND node_instance_id = ? AND mode = 'paper'", scope.WorkflowID, scope.PaperNodeInstanceID).Order("created_at DESC, id DESC").Limit(200)
	for column, value := range map[string]string{"market": filters.Market, "instrument": filters.Instrument, "status": filters.Status} {
		if value != "" {
			query = query.Where(column+" = ?", value)
		}
	}
	var rows []tradingOrder
	if err := query.Find(&rows).Error; err != nil {
		return nil, nil, errors.New("list Binance Paper orders failed")
	}
	orders := make([]map[string]any, len(rows))
	accountIDs := map[string]bool{}
	for index, row := range rows {
		accountIDs[row.Account] = true
		orders[index] = orderView(row)
	}
	accounts, err := q.paperAccounts(ctx, accountIDs)
	return orders, accounts, err
}

func orderView(row tradingOrder) map[string]any {
	return map[string]any{"id": row.ID, "account": row.Account, "clientOrderId": row.ClientOrderID, "market": row.Market, "instrument": row.Instrument, "side": row.Side, "quantity": row.Quantity.String(), "executed": row.Executed.String(), "averagePrice": row.AveragePrice.String(), "notional": row.Notional.String(), "status": row.Status, "createdAt": row.CreatedAt.UTC().Format(time.RFC3339Nano), "updatedAt": row.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func (q *binanceRuntime) paperAccounts(ctx context.Context, accountIDs map[string]bool) ([]map[string]any, error) {
	ids := make([]string, 0, len(accountIDs))
	for id := range accountIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]map[string]any, 0, len(ids))
	for _, accountID := range ids {
		var cash decimal.Decimal
		if err := q.db.WithContext(ctx).Model(&paperLedgerEntry{}).Where("account = ?", accountID).Select("COALESCE(SUM(amount), 0)").Scan(&cash).Error; err != nil {
			return nil, errors.New("load Binance Paper balance failed")
		}
		var positions []tradingPosition
		if err := q.db.WithContext(ctx).Where("account = ? AND mode = 'paper' AND quantity <> 0", accountID).Order("market, instrument").Find(&positions).Error; err != nil {
			return nil, errors.New("load Binance Paper positions failed")
		}
		equity := cash
		positionViews := make([]map[string]any, len(positions))
		for index, position := range positions {
			quote, err := (marketDataProvider{runtime: q}).Quote(ctx, sdk.QuoteQuery{Market: position.Market, Instrument: position.Instrument})
			if err != nil || quote.Price.Sign() <= 0 {
				return nil, errors.New("load Binance Paper position quote failed")
			}
			if position.Market == "spot" {
				equity = equity.Add(position.Quantity.Mul(quote.Price))
			} else {
				equity = equity.Add(quote.Price.Sub(position.AveragePrice).Mul(position.Quantity))
			}
			positionViews[index] = map[string]any{"market": position.Market, "instrument": position.Instrument, "quantity": position.Quantity.String(), "averagePrice": position.AveragePrice.String(), "updatedAt": position.UpdatedAt.UTC().Format(time.RFC3339Nano)}
		}
		result = append(result, map[string]any{"id": accountID, "cashBalance": cash.String(), "equity": equity.String(), "positions": positionViews})
	}
	return result, nil
}
