package quant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type quantSignalAction struct{ runtime *quantRuntime }
type quantPaperAction struct{ runtime *quantRuntime }

type quantPublicQuote struct {
	Price     decimal.Decimal
	Retrieved time.Time
}

type quantPaperConfig struct {
	quantSeriesConfig
	DecisionMode          string
	InitialBalance        decimal.Decimal
	FeeRate               decimal.Decimal
	MaxTotalNotional      decimal.Decimal
	MaxInstrumentNotional decimal.Decimal
	MaxOperationNotional  decimal.Decimal
	MaxDailyLoss          decimal.Decimal
	MaxDrawdown           decimal.Decimal
	MaxQuoteAge           time.Duration
}

type quantPaperResult struct {
	AccountID int64
	SignalID  int64
	OrderID   int64
	Executed  bool
	Status    string
	Reason    string
}

func (q *quantRuntime) registerPaper(registrar sdk.Registrar) error {
	if err := registrar.Action(sdk.NodeDescriptor{
		Type: "official.quant.signal", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: quantSeriesConfigSchema,
		UISchema:     json.RawMessage(`{"ui:order":["market","instrument","interval"]}`),
		InputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"strategyId":{"type":"string","minLength":1},"strategyVersion":{"type":"string","minLength":1},"target":{"type":"string","pattern":"^-?[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"evaluatedAt":{"type":"string","format":"date-time"},"businessKey":{"type":"string","minLength":1,"maxLength":256}},"required":["strategyId","strategyVersion","target","evaluatedAt","businessKey"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"signalId":{"type":"integer"},"businessKey":{"type":"string"},"target":{"type":"string","x-coinsphere-decimal":true},"status":{"type":"string","enum":["pending","superseded","approved","rejected","executed"]}},"required":["signalId","businessKey","target","status"],"additionalProperties":false}`),
		Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
	}, quantSignalAction{runtime: q}); err != nil {
		return err
	}
	return registrar.Action(sdk.NodeDescriptor{
		Type: "official.quant.paper_execute", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"decisionMode":{"type":"string","title":"Decision mode","enum":["human","auto"],"default":"human"},"market":{"type":"string","title":"Market","enum":["spot","usdm"]},"instrument":{"type":"string","title":"Instrument","pattern":"^[A-Z0-9]{2,32}$"},"interval":{"type":"string","title":"Interval","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]},"initialBalance":{"type":"string","title":"Initial balance","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"feeRate":{"type":"string","title":"Fee rate","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"maxTotalNotional":{"type":"string","title":"Max total notional","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"maxInstrumentNotional":{"type":"string","title":"Max instrument notional","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"maxOperationNotional":{"type":"string","title":"Max operation notional","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"maxDailyLoss":{"type":"string","title":"Max daily loss","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"maxDrawdown":{"type":"string","title":"Max drawdown ratio","pattern":"^(?:0(?:\\.[0-9]+)?|1(?:\\.0+)?)$","x-coinsphere-decimal":true},"maxQuoteAgeSeconds":{"type":"integer","title":"Max quote age","minimum":1,"maximum":60,"default":10}},"required":["decisionMode","market","instrument","interval","initialBalance","feeRate","maxTotalNotional","maxInstrumentNotional","maxOperationNotional","maxDailyLoss","maxDrawdown","maxQuoteAgeSeconds"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["decisionMode","market","instrument","interval","initialBalance","feeRate","maxTotalNotional","maxInstrumentNotional","maxOperationNotional","maxDailyLoss","maxDrawdown","maxQuoteAgeSeconds"]}`),
		InputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"signalId":{"type":"integer","minimum":1},"decisionTaskId":{"type":"integer","minimum":0},"decisionStatus":{"type":"string","enum":["approved","rejected","expired","superseded"]}},"required":["signalId","decisionTaskId","decisionStatus"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"accountId":{"type":"integer"},"signalId":{"type":"integer"},"orderId":{"type":"integer"},"executed":{"type":"boolean"},"status":{"type":"string"},"reason":{"type":"string"}},"required":["accountId","signalId","orderId","executed","status","reason"],"additionalProperties":false}`),
		Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectPaper, State: sdk.StatePersistent,
	}, quantPaperAction{runtime: q})
}

func (a quantSignalAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	config, err := parseQuantSeriesConfig(request.Config)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	var input struct {
		StrategyID, StrategyVersion, Target, EvaluatedAt, BusinessKey string
	}
	if json.Unmarshal(request.Input, &input) != nil {
		return sdk.ActionResult{}, errors.New("quant signal input is invalid")
	}
	target, targetErr := decimal.NewFromString(input.Target)
	evaluatedAt, timeErr := parseQuantUTCTime(input.EvaluatedAt)
	workflowID, workflowErr := quantInt64(request.Revision.WorkflowID)
	revisionID, revisionErr := quantInt64(request.Revision.RevisionID)
	input.StrategyID, input.StrategyVersion, input.BusinessKey = strings.TrimSpace(input.StrategyID), strings.TrimSpace(input.StrategyVersion), strings.TrimSpace(input.BusinessKey)
	if targetErr != nil || timeErr != nil || workflowErr != nil || revisionErr != nil ||
		target.LessThan(decimal.NewFromInt(-1)) || target.GreaterThan(quantOne) ||
		input.StrategyID == "" || input.StrategyVersion == "" || input.BusinessKey == "" || len(input.BusinessKey) > 256 {
		return sdk.ActionResult{}, errors.New("quant signal identity or Decimal target is invalid")
	}
	if existing, ok, err := a.runtime.loadQuantSignalByOperation(ctx, request.OperationKey); err != nil {
		return sdk.ActionResult{}, err
	} else if ok {
		return quantSignalResult(existing), nil
	}
	now := time.Now().UTC()
	row := quantSignal{
		OperationKey: request.OperationKey, WorkflowID: workflowID, RevisionID: revisionID,
		NodeInstanceID: request.NodeInstanceID, StrategyID: input.StrategyID, StrategyVersion: input.StrategyVersion,
		Market: config.Market, Instrument: config.Instrument, BusinessKey: input.BusinessKey,
		Target: target, EvaluatedAt: evaluatedAt, Status: "pending", CreatedAt: now,
	}
	err = a.runtime.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		identity := fmt.Sprintf("%d:%s:%s", workflowID, request.NodeInstanceID, input.BusinessKey)
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, identity).Error; err != nil {
			return errors.New("lock Quant signal business identity failed")
		}
		var superseded []quantSignal
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			`workflow_id = ? AND node_instance_id = ? AND business_key = ? AND status = 'pending'
			AND NOT EXISTS (
				SELECT 1 FROM workflow_run_nodes signal_run
				JOIN workflow_human_tasks task ON task.run_id = signal_run.run_id
				WHERE signal_run.operation_key = plugin_quant.signals.operation_key
				AND task.status IN ('approved', 'rejected')
			)`,
			workflowID, request.NodeInstanceID, input.BusinessKey,
		).Find(&superseded).Error; err != nil {
			return errors.New("load replaced Quant signals failed")
		}
		if err := tx.Model(&quantSignal{}).Where("id IN ?", quantSignalIDs(superseded)).Updates(map[string]any{
			"status": "superseded", "decided_at": now,
		}).Error; err != nil {
			return errors.New("replace pending Quant signals failed")
		}
		if err := tx.Create(&row).Error; err != nil {
			return errors.New("persist Quant signal failed")
		}
		if len(superseded) > 0 {
			if err := tx.Model(&quantSignal{}).Where("id IN ?", quantSignalIDs(superseded)).Update("superseded_by", row.ID).Error; err != nil {
				return errors.New("link replaced Quant signals failed")
			}
		}
		return nil
	})
	if err != nil {
		if existing, ok, loadErr := a.runtime.loadQuantSignalByOperation(ctx, request.OperationKey); loadErr == nil && ok {
			return quantSignalResult(existing), nil
		}
		return sdk.ActionResult{}, err
	}
	return quantSignalResult(row), nil
}

func (a quantPaperAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	config, err := parseQuantPaperConfig(request.Config)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	var input struct {
		SignalID       int64  `json:"signalId"`
		DecisionTaskID int64  `json:"decisionTaskId"`
		DecisionStatus string `json:"decisionStatus"`
	}
	if json.Unmarshal(request.Input, &input) != nil || input.SignalID <= 0 ||
		input.DecisionStatus != "approved" && input.DecisionStatus != "rejected" &&
			input.DecisionStatus != "expired" && input.DecisionStatus != "superseded" {
		return sdk.ActionResult{}, errors.New("quant Paper decision input is invalid")
	}
	workflowID, err := quantInt64(request.Revision.WorkflowID)
	if err != nil {
		return sdk.ActionResult{}, errors.New("quant Paper workflow identity is invalid")
	}
	if existing, ok, err := a.runtime.loadQuantPaperOrderByOperation(ctx, request.OperationKey); err != nil {
		return sdk.ActionResult{}, err
	} else if ok {
		return quantPaperActionResult(quantPaperResult{AccountID: existing.AccountID, SignalID: existing.SignalID, OrderID: existing.ID, Executed: true, Status: "executed"}), nil
	}
	if existing, ok, err := a.runtime.loadQuantSignalByPaperOperation(ctx, request.OperationKey); err != nil {
		return sdk.ActionResult{}, err
	} else if ok {
		result := quantPaperResult{SignalID: existing.ID, Status: existing.Status}
		if existing.RejectionReason != nil {
			result.Reason = *existing.RejectionReason
		}
		if existing.Status == "executed" {
			result.Reason = "no_position_change"
		}
		return quantPaperActionResult(result), nil
	}
	var quote quantPublicQuote
	if input.DecisionStatus == "approved" {
		quote, err = a.runtime.quote(ctx, config.quantSeriesConfig)
		if err != nil {
			return sdk.ActionResult{}, err
		}
	}
	result := quantPaperResult{SignalID: input.SignalID, Status: input.DecisionStatus}
	err = a.runtime.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return a.runtime.executeQuantPaper(tx, request, config, input.SignalID, input.DecisionTaskID, input.DecisionStatus, workflowID, quote, &result)
	})
	if err != nil {
		return sdk.ActionResult{}, err
	}
	return quantPaperActionResult(result), nil
}

func (q *quantRuntime) executeQuantPaper(tx *gorm.DB, request sdk.ActionRequest, config quantPaperConfig, signalID, taskID int64, decision string, workflowID int64, quote quantPublicQuote, result *quantPaperResult) error {
	now := time.Now().UTC()
	identity := fmt.Sprintf("%d:%s", workflowID, request.NodeInstanceID)
	if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, identity).Error; err != nil {
		return errors.New("lock Quant Paper account identity failed")
	}
	var signal quantSignal
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&signal, signalID).Error; err != nil ||
		signal.WorkflowID != workflowID || signal.Market != config.Market || signal.Instrument != config.Instrument {
		return errors.New("quant Paper signal scope is invalid")
	}
	if signal.Status == "executed" {
		var order quantPaperOrder
		if err := tx.Where("signal_id = ?", signal.ID).First(&order).Error; err == nil {
			*result = quantPaperResult{AccountID: order.AccountID, SignalID: signal.ID, OrderID: order.ID, Executed: true, Status: "executed"}
			return nil
		}
		*result = quantPaperResult{SignalID: signal.ID, Executed: false, Status: "executed", Reason: "no_position_change"}
		return nil
	}
	if signal.Status == "superseded" && (decision == "superseded" || decision == "expired") {
		if err := verifyQuantPaperDecision(tx, config.DecisionMode, signal, taskID, decision); err != nil {
			return err
		}
		*result = quantPaperResult{SignalID: signal.ID, Executed: false, Status: "superseded", Reason: "superseded"}
		return nil
	}
	if signal.Status != "pending" && signal.Status != "approved" {
		return errors.New("quant Paper signal is no longer executable")
	}
	if err := verifyQuantPaperDecision(tx, config.DecisionMode, signal, taskID, decision); err != nil {
		return err
	}
	if decision != "approved" {
		reason := "human_rejected"
		if decision == "expired" {
			reason = "task_expired"
		}
		return rejectQuantSignal(tx, &signal, request.OperationKey, taskID, reason, now, result)
	}
	if quote.Price.Sign() <= 0 || quote.Retrieved.IsZero() || now.Sub(quote.Retrieved) < 0 || now.Sub(quote.Retrieved) > config.MaxQuoteAge {
		return rejectQuantSignal(tx, &signal, request.OperationKey, taskID, "stale_quote", now, result)
	}
	var instrument quantInstrument
	if err := tx.Where("market = ? AND symbol = ? AND status = 'TRADING'", config.Market, config.Instrument).First(&instrument).Error; err != nil {
		return rejectQuantSignal(tx, &signal, request.OperationKey, taskID, "instrument_unavailable", now, result)
	}
	account, exists, err := loadQuantPaperAccountForUpdate(tx, workflowID, request.NodeInstanceID)
	if err != nil {
		return err
	}
	if !exists {
		date := quantUTCDate(now)
		account = quantPaperAccount{
			WorkflowID: workflowID, NodeInstanceID: request.NodeInstanceID, Status: "active",
			InitialBalance: config.InitialBalance, CashBalance: config.InitialBalance,
			Equity: config.InitialBalance, PeakEquity: config.InitialBalance,
			DayStartEquity: config.InitialBalance, DayStartDate: date, CreatedAt: now, UpdatedAt: now,
		}
	}
	if account.Status != "active" {
		return rejectQuantSignal(tx, &signal, request.OperationKey, taskID, "account_paused", now, result)
	}
	var positions []quantPaperPosition
	if exists {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ?", account.ID).Find(&positions).Error; err != nil {
			return errors.New("load Quant Paper positions failed")
		}
	}
	currentIndex := -1
	currentQuantity := decimal.Zero
	for index := range positions {
		if positions[index].Market == config.Market && positions[index].Instrument == config.Instrument {
			currentIndex, currentQuantity = index, positions[index].Quantity
			positions[index].LastPrice = quote.Price
		}
	}
	currentEquity := account.CashBalance
	for _, position := range positions {
		currentEquity = currentEquity.Add(position.Quantity.Mul(position.LastPrice))
	}
	desiredQuantity := quantRoundToStep(currentEquity.Mul(signal.Target).Div(quote.Price), instrument.QuantityStep)
	delta := desiredQuantity.Sub(currentQuantity)
	if delta.IsZero() {
		if !exists {
			if err := tx.Create(&account).Error; err != nil {
				return errors.New("create Quant Paper account failed")
			}
		}
		updates := map[string]any{"status": "executed", "paper_operation_key": request.OperationKey, "decision_task_id": quantNullableTaskID(taskID), "decided_at": now, "executed_at": now}
		if err := tx.Model(&signal).Updates(updates).Error; err != nil {
			return errors.New("finish no-change Quant signal failed")
		}
		*result = quantPaperResult{AccountID: account.ID, SignalID: signal.ID, Executed: false, Status: "executed", Reason: "no_position_change"}
		return nil
	}
	if delta.Abs().LessThan(instrument.MinQuantity) {
		return rejectQuantSignal(tx, &signal, request.OperationKey, taskID, "minimum_quantity", now, result)
	}
	notional := delta.Abs().Mul(quote.Price)
	fee := notional.Mul(config.FeeRate)
	cashAfter := account.CashBalance.Sub(delta.Mul(quote.Price)).Sub(fee)
	if currentIndex < 0 {
		positions = append(positions, quantPaperPosition{Market: config.Market, Instrument: config.Instrument, LastPrice: quote.Price})
		currentIndex = len(positions) - 1
	}
	positions[currentIndex].AveragePrice = quantPositionAverage(
		currentQuantity, positions[currentIndex].AveragePrice, delta, quote.Price,
	)
	positions[currentIndex].Quantity = desiredQuantity
	totalNotional, equityAfter := decimal.Zero, cashAfter
	for _, position := range positions {
		totalNotional = totalNotional.Add(position.Quantity.Abs().Mul(position.LastPrice))
		equityAfter = equityAfter.Add(position.Quantity.Mul(position.LastPrice))
	}
	if !account.DayStartDate.Equal(quantUTCDate(now)) {
		account.DayStartDate, account.DayStartEquity = quantUTCDate(now), currentEquity
	}
	peak := decimal.Max(account.PeakEquity, currentEquity)
	dailyLoss := decimal.Max(account.DayStartEquity.Sub(equityAfter), decimal.Zero)
	drawdown := decimal.Zero
	if peak.Sign() > 0 {
		drawdown = decimal.Max(peak.Sub(equityAfter).Div(peak), decimal.Zero)
	}
	riskReason := ""
	switch {
	case notional.GreaterThan(config.MaxOperationNotional):
		riskReason = "operation_notional"
	case desiredQuantity.Abs().Mul(quote.Price).GreaterThan(config.MaxInstrumentNotional):
		riskReason = "instrument_notional"
	case totalNotional.GreaterThan(config.MaxTotalNotional):
		riskReason = "total_notional"
	case dailyLoss.GreaterThan(config.MaxDailyLoss):
		riskReason = "daily_loss"
	case drawdown.GreaterThan(config.MaxDrawdown):
		riskReason = "drawdown"
	case equityAfter.Sign() < 0:
		riskReason = "negative_equity"
	}
	if riskReason != "" {
		return rejectQuantSignal(tx, &signal, request.OperationKey, taskID, riskReason, now, result)
	}
	if !exists {
		if err := tx.Create(&account).Error; err != nil {
			return errors.New("create Quant Paper account failed")
		}
	}
	side := "buy"
	if delta.Sign() < 0 {
		side = "sell"
	}
	order := quantPaperOrder{
		AccountID: account.ID, SignalID: signal.ID, OperationKey: request.OperationKey,
		Market: config.Market, Instrument: config.Instrument, Side: side, Quantity: delta.Abs(),
		QuotePrice: quote.Price, Notional: notional, Status: "filled", QuotedAt: quote.Retrieved,
		CashAfter: cashAfter, EquityAfter: equityAfter, CreatedAt: now,
	}
	if err := tx.Create(&order).Error; err != nil {
		return errors.New("create Quant Paper order failed")
	}
	fill := quantPaperFill{
		OrderID: order.ID, OperationKey: quantDerivedOperationKey(request.OperationKey, "fill"),
		QuantityDelta: delta, Price: quote.Price, Notional: notional, FilledAt: now,
	}
	if err := tx.Create(&fill).Error; err != nil {
		return errors.New("create Quant Paper fill failed")
	}
	if err := tx.Create(&quantPaperFee{
		FillID: fill.ID, OperationKey: quantDerivedOperationKey(request.OperationKey, "fee"), Amount: fee, CreatedAt: now,
	}).Error; err != nil {
		return errors.New("create Quant Paper fee failed")
	}
	entries := []quantPaperLedgerEntry{
		{AccountID: account.ID, OperationKey: request.OperationKey, EntryType: "trade_cash", Amount: delta.Mul(quote.Price).Neg(), OccurredAt: now},
		{AccountID: account.ID, OperationKey: request.OperationKey, EntryType: "fee", Amount: fee.Neg(), OccurredAt: now},
	}
	if err := tx.Create(&entries).Error; err != nil {
		return errors.New("create Quant Paper ledger entries failed")
	}
	position := positions[currentIndex]
	position.AccountID, position.UpdatedAt = account.ID, now
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "account_id"}, {Name: "market"}, {Name: "instrument"}},
		DoUpdates: clause.AssignmentColumns([]string{"quantity", "average_price", "last_price", "updated_at"}),
	}).Create(&position).Error; err != nil {
		return errors.New("project Quant Paper position failed")
	}
	peak = decimal.Max(peak, equityAfter)
	if err := tx.Model(&account).Updates(map[string]any{
		"cash_balance": cashAfter, "equity": equityAfter, "peak_equity": peak,
		"day_start_equity": account.DayStartEquity, "day_start_date": account.DayStartDate, "updated_at": now,
	}).Error; err != nil {
		return errors.New("project Quant Paper account failed")
	}
	if err := tx.Model(&signal).Updates(map[string]any{
		"status": "executed", "paper_operation_key": request.OperationKey,
		"decision_task_id": quantNullableTaskID(taskID), "decided_at": now, "executed_at": now,
	}).Error; err != nil {
		return errors.New("finish Quant Paper signal failed")
	}
	*result = quantPaperResult{AccountID: account.ID, SignalID: signal.ID, OrderID: order.ID, Executed: true, Status: "executed"}
	return nil
}

func parseQuantPaperConfig(raw json.RawMessage) (quantPaperConfig, error) {
	var payload struct {
		DecisionMode, Market, Instrument, Interval, InitialBalance, FeeRate string
		MaxTotalNotional, MaxInstrumentNotional, MaxOperationNotional       string
		MaxDailyLoss, MaxDrawdown                                           string
		MaxQuoteAgeSeconds                                                  int
	}
	if json.Unmarshal(raw, &payload) != nil {
		return quantPaperConfig{}, errors.New("quant Paper configuration is invalid")
	}
	series, err := parseQuantSeriesConfig(mustMarshal(map[string]any{"market": payload.Market, "instrument": payload.Instrument, "interval": payload.Interval}))
	if err != nil || payload.DecisionMode != "human" && payload.DecisionMode != "auto" || payload.MaxQuoteAgeSeconds < 1 || payload.MaxQuoteAgeSeconds > 60 {
		return quantPaperConfig{}, errors.New("quant Paper mode or market configuration is invalid")
	}
	values := []*decimal.Decimal{}
	for _, rawValue := range []string{payload.InitialBalance, payload.FeeRate, payload.MaxTotalNotional, payload.MaxInstrumentNotional, payload.MaxOperationNotional, payload.MaxDailyLoss, payload.MaxDrawdown} {
		value, parseErr := decimal.NewFromString(rawValue)
		if parseErr != nil {
			return quantPaperConfig{}, errors.New("quant Paper limits must be Decimal strings")
		}
		copy := value
		values = append(values, &copy)
	}
	config := quantPaperConfig{
		quantSeriesConfig: series, DecisionMode: payload.DecisionMode,
		InitialBalance: *values[0], FeeRate: *values[1], MaxTotalNotional: *values[2],
		MaxInstrumentNotional: *values[3], MaxOperationNotional: *values[4],
		MaxDailyLoss: *values[5], MaxDrawdown: *values[6],
		MaxQuoteAge: time.Duration(payload.MaxQuoteAgeSeconds) * time.Second,
	}
	if config.InitialBalance.Sign() <= 0 || config.FeeRate.Sign() < 0 || config.FeeRate.GreaterThan(quantOne) ||
		config.MaxTotalNotional.Sign() <= 0 || config.MaxInstrumentNotional.Sign() <= 0 ||
		config.MaxOperationNotional.Sign() <= 0 || config.MaxDailyLoss.Sign() < 0 ||
		config.MaxDrawdown.Sign() < 0 || config.MaxDrawdown.GreaterThan(quantOne) {
		return quantPaperConfig{}, errors.New("quant Paper limits are outside the allowed range")
	}
	return config, nil
}

func (q *quantRuntime) fetchQuantPublicQuote(ctx context.Context, config quantSeriesConfig) (quantPublicQuote, error) {
	base, path := "https://data-api.binance.vision", "/api/v3/ticker/price"
	if config.Market == "usdm" {
		base, path = "https://fapi.binance.com", "/fapi/v1/ticker/price"
	}
	var payload struct {
		Price string `json:"price"`
	}
	if err := q.getQuantJSON(ctx, base+path+"?"+url.Values{"symbol": {config.Instrument}}.Encode(), 0, &payload); err != nil {
		return quantPublicQuote{}, err
	}
	price, err := decimal.NewFromString(payload.Price)
	if err != nil || price.Sign() <= 0 {
		return quantPublicQuote{}, errors.New("binance public quote is invalid")
	}
	return quantPublicQuote{Price: price, Retrieved: time.Now().UTC()}, nil
}

func verifyQuantPaperDecision(tx *gorm.DB, mode string, signal quantSignal, taskID int64, decision string) error {
	if mode == "auto" {
		if taskID != 0 || decision != "approved" {
			return errors.New("automatic Quant Paper execution requires an automatic approval")
		}
		return nil
	}
	if taskID <= 0 {
		return errors.New("human Quant Paper execution requires a durable task")
	}
	var task struct {
		WorkflowID  int64
		BusinessKey string
		TaskType    string
		Status      string
	}
	if err := tx.Table("workflow_human_tasks").Where("id = ?", taskID).First(&task).Error; err != nil ||
		task.WorkflowID != signal.WorkflowID || task.BusinessKey != signal.BusinessKey ||
		task.TaskType != "paper_signal" || task.Status != decision {
		return errors.New("quant Paper human task is stale or outside the signal scope")
	}
	return nil
}

func rejectQuantSignal(tx *gorm.DB, signal *quantSignal, operationKey string, taskID int64, reason string, now time.Time, result *quantPaperResult) error {
	if err := tx.Model(signal).Updates(map[string]any{
		"status": "rejected", "paper_operation_key": operationKey, "decision_task_id": quantNullableTaskID(taskID),
		"rejection_reason": reason, "decided_at": now,
	}).Error; err != nil {
		return errors.New("reject Quant Paper signal failed")
	}
	*result = quantPaperResult{SignalID: signal.ID, Executed: false, Status: "rejected", Reason: reason}
	return nil
}

func loadQuantPaperAccountForUpdate(tx *gorm.DB, workflowID int64, nodeID string) (quantPaperAccount, bool, error) {
	var account quantPaperAccount
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("workflow_id = ? AND node_instance_id = ?", workflowID, nodeID).First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return quantPaperAccount{}, false, nil
	}
	if err != nil {
		return quantPaperAccount{}, false, errors.New("load Quant Paper account failed")
	}
	return account, true, nil
}

func (q *quantRuntime) loadQuantSignalByOperation(ctx context.Context, operationKey string) (quantSignal, bool, error) {
	var signal quantSignal
	if err := q.db.WithContext(ctx).Where("operation_key = ?", operationKey).First(&signal).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return quantSignal{}, false, nil
		}
		return quantSignal{}, false, errors.New("load Quant signal failed")
	}
	return signal, true, nil
}

func (q *quantRuntime) loadQuantPaperOrderByOperation(ctx context.Context, operationKey string) (quantPaperOrder, bool, error) {
	var order quantPaperOrder
	if err := q.db.WithContext(ctx).Where("operation_key = ?", operationKey).First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return quantPaperOrder{}, false, nil
		}
		return quantPaperOrder{}, false, errors.New("load Quant Paper order failed")
	}
	return order, true, nil
}

func (q *quantRuntime) loadQuantSignalByPaperOperation(ctx context.Context, operationKey string) (quantSignal, bool, error) {
	var signal quantSignal
	if err := q.db.WithContext(ctx).Where("paper_operation_key = ?", operationKey).First(&signal).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return quantSignal{}, false, nil
		}
		return quantSignal{}, false, errors.New("load Quant Paper outcome failed")
	}
	return signal, true, nil
}

func quantSignalResult(signal quantSignal) sdk.ActionResult {
	return sdk.ActionResult{Output: mustMarshal(map[string]any{
		"signalId": signal.ID, "businessKey": signal.BusinessKey, "target": signal.Target.String(), "status": signal.Status,
	})}
}

func quantPaperActionResult(result quantPaperResult) sdk.ActionResult {
	return sdk.ActionResult{Output: mustMarshal(map[string]any{
		"accountId": result.AccountID, "signalId": result.SignalID, "orderId": result.OrderID,
		"executed": result.Executed, "status": result.Status, "reason": result.Reason,
	})}
}

func quantSignalIDs(signals []quantSignal) []int64 {
	ids := make([]int64, len(signals))
	for index := range signals {
		ids[index] = signals[index].ID
	}
	return ids
}

func quantRoundToStep(value, step decimal.Decimal) decimal.Decimal {
	return value.Div(step).Truncate(0).Mul(step)
}

func quantPositionAverage(quantity, average, delta, price decimal.Decimal) decimal.Decimal {
	next := quantity.Add(delta)
	if next.IsZero() {
		return decimal.Zero
	}
	if quantity.IsZero() || quantity.Sign() != next.Sign() {
		return price
	}
	if quantity.Sign() != delta.Sign() {
		return average
	}
	return quantity.Abs().Mul(average).Add(delta.Abs().Mul(price)).Div(next.Abs())
}

func quantNullableTaskID(taskID int64) any {
	if taskID <= 0 {
		return nil
	}
	return taskID
}

func quantDerivedOperationKey(operationKey, suffix string) string {
	digest := sha256.Sum256([]byte(operationKey + ":" + suffix))
	return hex.EncodeToString(digest[:])
}

func quantUTCDate(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func quantPathInt64(value string) (int64, error) {
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number <= 0 {
		return 0, errors.New("positive integer path is required")
	}
	return number, nil
}

var _ sdk.ActionHandler = quantSignalAction{}
var _ sdk.ActionHandler = quantPaperAction{}
