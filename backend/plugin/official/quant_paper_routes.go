package official

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type quantPaperResultScope struct {
	WorkflowID           int64  `json:"workflowId"`
	SignalNodeInstanceID string `json:"signalNodeInstanceId"`
	PaperNodeInstanceID  string `json:"paperNodeInstanceId"`
}

type quantPaperResultFilters struct {
	Market     string `json:"market"`
	Instrument string `json:"instrument"`
	Status     string `json:"status"`
}

func (q *quantRuntime) handleQuantSignals(w http.ResponseWriter, r *http.Request, scope sdk.RouteScope) {
	if !validQuantSystemScope(scope) || !quantQueryKeys(r, "market", "instrument", "status", "limit") {
		writeQuantProblem(w, http.StatusBadRequest, "invalid Quant signal query")
		return
	}
	limit, err := quantQueryLimit(r, 100, 200)
	if err != nil {
		writeQuantProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	filters := quantPaperResultFilters{
		Market:     strings.ToLower(strings.TrimSpace(r.URL.Query().Get("market"))),
		Instrument: strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("instrument"))),
		Status:     strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status"))),
	}
	items, err := q.listQuantSignals(r, 0, "", filters, limit)
	if err != nil {
		writeQuantProblem(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeQuantOK(w, map[string]any{"items": items})
}

func (q *quantRuntime) handleQuantPaperAccounts(w http.ResponseWriter, r *http.Request, scope sdk.RouteScope) {
	if !validQuantSystemScope(scope) || !quantQueryKeys(r, "limit") {
		writeQuantProblem(w, http.StatusBadRequest, "invalid Quant Paper account query")
		return
	}
	limit, err := quantQueryLimit(r, 100, 200)
	if err != nil {
		writeQuantProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := q.listQuantPaperAccounts(r, 0, "", limit)
	if err != nil {
		writeQuantProblem(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeQuantOK(w, map[string]any{"items": items})
}

func (q *quantRuntime) handleQuantPaperAccountRebuild(w http.ResponseWriter, r *http.Request, scope sdk.RouteScope) {
	if !validQuantSystemScope(scope) || !quantQueryKeys(r) {
		writeQuantProblem(w, http.StatusBadRequest, "invalid Quant Paper rebuild request")
		return
	}
	accountID, err := quantPathInt64(r.PathValue("accountId"))
	if err != nil {
		writeQuantProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := q.rebuildQuantPaperAccount(r.Context(), accountID); err != nil {
		writeQuantProblem(w, http.StatusConflict, err.Error())
		return
	}
	writeQuantOK(w, map[string]any{"accountId": accountID, "rebuilt": true})
}

func (q *quantRuntime) handleQuantPaperResult(w http.ResponseWriter, r *http.Request, routeScope sdk.RouteScope) {
	scope, filters, ok := quantPaperScope(routeScope)
	if !ok || !quantQueryKeys(r) {
		writeQuantProblem(w, http.StatusBadRequest, "invalid Quant Paper result scope")
		return
	}
	signals, err := q.listQuantSignals(r, scope.WorkflowID, scope.SignalNodeInstanceID, filters, 200)
	if err != nil {
		writeQuantProblem(w, http.StatusInternalServerError, err.Error())
		return
	}
	accounts, err := q.listQuantPaperAccounts(r, scope.WorkflowID, scope.PaperNodeInstanceID, 20)
	if err != nil {
		writeQuantProblem(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeQuantOK(w, map[string]any{"signals": signals, "accounts": accounts})
}

func (q *quantRuntime) handleQuantSignalApprove(w http.ResponseWriter, r *http.Request, scope sdk.RouteScope) {
	q.handleQuantSignalDecision(w, r, scope, "approve")
}

func (q *quantRuntime) handleQuantSignalReject(w http.ResponseWriter, r *http.Request, scope sdk.RouteScope) {
	q.handleQuantSignalDecision(w, r, scope, "reject")
}

func (q *quantRuntime) handleQuantSignalDecision(w http.ResponseWriter, r *http.Request, routeScope sdk.RouteScope, action string) {
	scope, filters, ok := quantPaperScope(routeScope)
	resultScope, scopeOK := routeScope.(sdk.ResultScope)
	if !ok || !scopeOK || !quantQueryKeys(r) || resultScope.HumanTasks == nil {
		writeQuantProblem(w, http.StatusBadRequest, "invalid Quant signal decision scope")
		return
	}
	signalID, err := quantPathInt64(r.PathValue("signalId"))
	if err != nil {
		writeQuantProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	var signal quantSignal
	query := q.db.WithContext(r.Context()).Where(
		"id = ? AND workflow_id = ? AND node_instance_id = ? AND status = 'pending'",
		signalID, scope.WorkflowID, scope.SignalNodeInstanceID,
	)
	query = applyQuantSignalFilters(query, filters)
	if err := query.First(&signal).Error; err != nil {
		writeQuantProblem(w, http.StatusNotFound, "Quant signal is unavailable")
		return
	}
	var task struct {
		ID int64
	}
	if err := q.db.WithContext(r.Context()).Table("workflow_human_tasks tasks").Select("tasks.id").
		Joins("JOIN workflow_node_runs signal_run ON signal_run.batch_id = tasks.batch_id").Where(
		"signal_run.operation_key = ? AND tasks.workflow_id = ? AND tasks.task_type = 'paper_signal' AND tasks.business_key = ? AND tasks.status = 'pending'",
		signal.OperationKey, signal.WorkflowID, signal.BusinessKey,
	).Order("tasks.created_at DESC, tasks.id DESC").First(&task).Error; err != nil {
		writeQuantProblem(w, http.StatusConflict, "Quant signal has no pending approval task")
		return
	}
	if err := resultScope.HumanTasks.Decide(r.Context(), task.ID, action, resultScope.UserID); err != nil {
		writeQuantProblem(w, http.StatusConflict, "Quant signal decision is no longer available")
		return
	}
	writeQuantOK(w, map[string]any{"signalId": signal.ID, "taskId": task.ID, "decision": action})
}

func (q *quantRuntime) handleQuantPaperExport(w http.ResponseWriter, r *http.Request, routeScope sdk.RouteScope) {
	scope, filters, ok := quantPaperScope(routeScope)
	if !ok || !quantQueryKeys(r) {
		writeQuantProblem(w, http.StatusBadRequest, "invalid Quant Paper export scope")
		return
	}
	signals, err := q.listQuantSignals(r, scope.WorkflowID, scope.SignalNodeInstanceID, filters, 200)
	if err != nil {
		writeQuantProblem(w, http.StatusInternalServerError, err.Error())
		return
	}
	accounts, err := q.listQuantPaperAccounts(r, scope.WorkflowID, scope.PaperNodeInstanceID, 20)
	if err != nil {
		writeQuantProblem(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="paper-results.json"`)
	writeQuantJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": 1, "exportedAt": time.Now().UTC().Format(time.RFC3339Nano),
		"signals": signals, "accounts": accounts,
	})
}

func quantPaperScope(routeScope sdk.RouteScope) (quantPaperResultScope, quantPaperResultFilters, bool) {
	scope, ok := routeScope.(sdk.ResultScope)
	if !ok || scope.PluginID != quantPluginID || scope.PageKey != "paper" {
		return quantPaperResultScope{}, quantPaperResultFilters{}, false
	}
	var fixed quantPaperResultScope
	var filters quantPaperResultFilters
	if json.Unmarshal(scope.Scope, &fixed) != nil || json.Unmarshal(scope.Filters, &filters) != nil ||
		fixed.WorkflowID <= 0 || strings.TrimSpace(fixed.SignalNodeInstanceID) == "" || strings.TrimSpace(fixed.PaperNodeInstanceID) == "" {
		return quantPaperResultScope{}, quantPaperResultFilters{}, false
	}
	return fixed, filters, true
}

func (q *quantRuntime) listQuantSignals(r *http.Request, workflowID int64, nodeID string, filters quantPaperResultFilters, limit int) ([]map[string]any, error) {
	query := q.db.WithContext(r.Context()).Order("created_at DESC, id DESC").Limit(limit)
	if workflowID > 0 {
		query = query.Where("workflow_id = ? AND node_instance_id = ?", workflowID, nodeID)
	}
	query = applyQuantSignalFilters(query, filters)
	var signals []quantSignal
	if err := query.Find(&signals).Error; err != nil {
		return nil, errors.New("list Quant signals failed")
	}
	items := make([]map[string]any, len(signals))
	for index := range signals {
		items[index] = quantSignalView(signals[index])
		if signals[index].Status == "pending" {
			var task struct {
				ID     int64
				Status string
			}
			if err := q.db.WithContext(r.Context()).Table("workflow_human_tasks tasks").Select("tasks.id, tasks.status").
				Joins("JOIN workflow_node_runs signal_run ON signal_run.batch_id = tasks.batch_id").Where(
				"signal_run.operation_key = ? AND tasks.workflow_id = ? AND tasks.task_type = 'paper_signal' AND tasks.business_key = ?",
				signals[index].OperationKey, signals[index].WorkflowID, signals[index].BusinessKey,
			).Order("tasks.created_at DESC, tasks.id DESC").First(&task).Error; err == nil {
				switch task.Status {
				case "pending":
					items[index]["taskId"] = task.ID
				case "approved":
					items[index]["status"] = "approved"
				case "rejected":
					items[index]["status"], items[index]["rejectionReason"] = "rejected", "human_rejected"
				case "expired":
					items[index]["status"], items[index]["rejectionReason"] = "rejected", "task_expired"
				case "superseded":
					items[index]["status"] = "superseded"
				}
			}
		}
	}
	return items, nil
}

func applyQuantSignalFilters(query *gorm.DB, filters quantPaperResultFilters) *gorm.DB {
	if filters.Market != "" {
		query = query.Where("market = ?", strings.ToLower(filters.Market))
	}
	if filters.Instrument != "" {
		query = query.Where("instrument = ?", strings.ToUpper(filters.Instrument))
	}
	if filters.Status != "" {
		query = query.Where("status = ?", strings.ToLower(filters.Status))
	}
	return query
}

func (q *quantRuntime) listQuantPaperAccounts(r *http.Request, workflowID int64, nodeID string, limit int) ([]map[string]any, error) {
	query := q.db.WithContext(r.Context()).Order("created_at DESC, id DESC").Limit(limit)
	if workflowID > 0 {
		query = query.Where("workflow_id = ? AND node_instance_id = ?", workflowID, nodeID)
	}
	var accounts []quantPaperAccount
	if err := query.Find(&accounts).Error; err != nil {
		return nil, errors.New("list Quant Paper accounts failed")
	}
	items := make([]map[string]any, len(accounts))
	for index := range accounts {
		var positions []quantPaperPosition
		if err := q.db.WithContext(r.Context()).Where("account_id = ?", accounts[index].ID).Order("market, instrument").Find(&positions).Error; err != nil {
			return nil, errors.New("list Quant Paper positions failed")
		}
		positionViews := make([]map[string]any, len(positions))
		for positionIndex := range positions {
			positionViews[positionIndex] = map[string]any{
				"market": positions[positionIndex].Market, "instrument": positions[positionIndex].Instrument,
				"quantity": positions[positionIndex].Quantity.String(), "averagePrice": positions[positionIndex].AveragePrice.String(),
				"lastPrice": positions[positionIndex].LastPrice.String(), "updatedAt": positions[positionIndex].UpdatedAt.UTC().Format(time.RFC3339Nano),
			}
		}
		items[index] = map[string]any{
			"id": accounts[index].ID, "status": accounts[index].Status,
			"initialBalance": accounts[index].InitialBalance.String(), "cashBalance": accounts[index].CashBalance.String(),
			"equity": accounts[index].Equity.String(), "peakEquity": accounts[index].PeakEquity.String(),
			"dayStartEquity": accounts[index].DayStartEquity.String(), "updatedAt": accounts[index].UpdatedAt.UTC().Format(time.RFC3339Nano),
			"positions": positionViews,
		}
	}
	return items, nil
}

func quantSignalView(signal quantSignal) map[string]any {
	view := map[string]any{
		"id": signal.ID, "strategyId": signal.StrategyID, "strategyVersion": signal.StrategyVersion,
		"market": signal.Market, "instrument": signal.Instrument, "target": signal.Target.String(),
		"evaluatedAt": signal.EvaluatedAt.UTC().Format(time.RFC3339Nano), "status": signal.Status,
		"createdAt": signal.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if signal.RejectionReason != nil {
		view["rejectionReason"] = *signal.RejectionReason
	}
	if signal.DecidedAt != nil {
		view["decidedAt"] = signal.DecidedAt.UTC().Format(time.RFC3339Nano)
	}
	if signal.ExecutedAt != nil {
		view["executedAt"] = signal.ExecutedAt.UTC().Format(time.RFC3339Nano)
	}
	return view
}

func (q *quantRuntime) rebuildQuantPaperAccount(ctx context.Context, accountID int64) error {
	return q.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var account quantPaperAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, accountID).Error; err != nil {
			return errors.New("Quant Paper account is unavailable")
		}
		var rows []struct {
			Market, Instrument   string
			QuantityDelta, Price decimal.Decimal
			FilledAt             time.Time
			EquityAfter          decimal.Decimal
		}
		if err := tx.Table("plugin_quant.paper_fills fills").Select(
			"orders.market, orders.instrument, fills.quantity_delta, fills.price, fills.filled_at, orders.equity_after",
		).Joins("JOIN plugin_quant.paper_orders orders ON orders.id = fills.order_id").
			Where("orders.account_id = ?", accountID).Order("fills.filled_at, fills.id").Scan(&rows).Error; err != nil {
			return errors.New("load Quant Paper facts failed")
		}
		positions := map[string]quantPaperPosition{}
		peak := account.InitialBalance
		dayStart := account.InitialBalance
		today := quantUTCDate(time.Now().UTC())
		for _, row := range rows {
			key := row.Market + ":" + row.Instrument
			position := positions[key]
			position.AccountID, position.Market, position.Instrument = accountID, row.Market, row.Instrument
			position.AveragePrice = quantPositionAverage(position.Quantity, position.AveragePrice, row.QuantityDelta, row.Price)
			position.Quantity = position.Quantity.Add(row.QuantityDelta)
			position.LastPrice, position.UpdatedAt = row.Price, row.FilledAt.UTC()
			positions[key] = position
			peak = decimal.Max(peak, row.EquityAfter)
			if row.FilledAt.Before(today) {
				dayStart = row.EquityAfter
			}
		}
		var ledgerTotal decimal.Decimal
		if err := tx.Model(&quantPaperLedgerEntry{}).Where("account_id = ?", accountID).
			Select("COALESCE(SUM(amount), 0)").Scan(&ledgerTotal).Error; err != nil {
			return errors.New("sum Quant Paper ledger failed")
		}
		cash := account.InitialBalance.Add(ledgerTotal)
		equity := cash
		for _, position := range positions {
			equity = equity.Add(position.Quantity.Mul(position.LastPrice))
		}
		if err := tx.Where("account_id = ?", accountID).Delete(&quantPaperPosition{}).Error; err != nil {
			return errors.New("clear Quant Paper projections failed")
		}
		for _, position := range positions {
			if err := tx.Create(&position).Error; err != nil {
				return errors.New("rebuild Quant Paper position failed")
			}
		}
		if err := tx.Model(&account).Updates(map[string]any{
			"cash_balance": cash, "equity": equity, "peak_equity": peak,
			"day_start_equity": dayStart, "day_start_date": today, "updated_at": time.Now().UTC(),
		}).Error; err != nil {
			return errors.New("rebuild Quant Paper account failed")
		}
		return nil
	})
}
