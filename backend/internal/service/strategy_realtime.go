package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/marketdata"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrStrategyInstanceMissing        = errors.New("strategy instance was not found")
	ErrStrategySignalMissing          = errors.New("strategy signal was not found")
	ErrStrategySignalConflict         = errors.New("strategy signal state does not allow this operation")
	ErrStrategySignalReauthentication = errors.New("valid reauthentication token is required")
)

type StrategyInstanceCreatePayload struct {
	StrategyVersionID string                     `json:"strategyVersionId"`
	InstrumentID      string                     `json:"instrumentId"`
	Interval          string                     `json:"interval"`
	TradingAccountID  string                     `json:"tradingAccountId"`
	AllocationUSDT    string                     `json:"allocationUsdt"`
	StopLossRatio     string                     `json:"stopLossRatio"`
	Name              string                     `json:"name"`
	Mode              string                     `json:"mode"`
	Environment       string                     `json:"environment"`
	Parameters        map[string]json.RawMessage `json:"parameters"`
}

type StrategyInstanceView struct {
	ID                   string          `json:"id"`
	OwnerUserID          int64           `json:"ownerUserId"`
	StrategyVersionID    string          `json:"strategyVersionId"`
	TradingAccountID     *string         `json:"tradingAccountId"`
	AllocationUSDT       *string         `json:"allocationUsdt"`
	StopLossRatio        *string         `json:"stopLossRatio"`
	Name                 string          `json:"name"`
	Mode                 string          `json:"mode"`
	Environment          string          `json:"environment"`
	Parameters           json.RawMessage `json:"parameters"`
	IsEnabled            bool            `json:"isEnabled"`
	CreatedAt            string          `json:"createdAt"`
	UpdatedAt            string          `json:"updatedAt"`
	Market               string          `json:"market"`
	InstrumentID         string          `json:"instrumentId"`
	Symbol               string          `json:"symbol"`
	Interval             string          `json:"interval"`
	StrategyName         string          `json:"strategyName"`
	StrategyVersion      int             `json:"strategyVersion"`
	WorkflowDefinitionID int64           `json:"workflowDefinitionId"`
	WorkflowNodeID       string          `json:"workflowNodeId"`
}

type StrategySignalView struct {
	ID                 string  `json:"id"`
	StrategyInstanceID string  `json:"strategyInstanceId"`
	StrategyVersionID  string  `json:"strategyVersionId"`
	InstrumentID       string  `json:"instrumentId"`
	Interval           string  `json:"interval"`
	CandleOpenTime     string  `json:"candleOpenTime"`
	CandleCloseTime    string  `json:"candleCloseTime"`
	Target             string  `json:"target"`
	PreviousTarget     string  `json:"previousTarget"`
	TargetChange       string  `json:"targetChange"`
	Action             string  `json:"action"`
	Mode               string  `json:"mode"`
	Environment        string  `json:"environment"`
	Status             string  `json:"status"`
	ExpiresAt          *string `json:"expiresAt,omitempty"`
	DecidedAt          *string `json:"decidedAt,omitempty"`
	CreatedAt          string  `json:"createdAt"`
}

type StrategySignalQuery struct {
	Page               CursorPage
	InstrumentID       string
	StrategyInstanceID string
	Interval           string
	StartTime          string
	EndTime            string
}

type strategyInstanceListRow struct {
	db.StrategyInstance
	Symbol                string
	StrategyName          string
	StrategyVersionNumber int
}

type strategySignalListRow struct {
	db.StrategySignal
	PreviousTarget *decimal.Decimal `gorm:"column:previous_target"`
}

type validatedStrategyInstance struct {
	Name, Mode, Environment, ParametersJSON string
	TradingAccountID                        *uuid.UUID
	AllocationUSDT                          *decimal.Decimal
	StopLossRatio                           *decimal.Decimal
}

func validateStrategyInstancePayload(
	payload StrategyInstanceCreatePayload, version db.StrategyVersion, market string,
	spotLiveManualEnabled, usdmLiveManualEnabled, spotLiveAutoEnabled, usdmLiveAutoEnabled bool,
) (validatedStrategyInstance, error) {
	name := strings.TrimSpace(payload.Name)
	if name == "" || len(name) > 120 {
		return validatedStrategyInstance{}, invalidStrategy("name must be between 1 and 120 bytes")
	}
	mode := strings.TrimSpace(payload.Mode)
	if mode == "" {
		mode = "signal_only"
	}
	if mode != "signal_only" && mode != "manual" && mode != "auto" {
		return validatedStrategyInstance{}, invalidStrategy("mode must be signal_only, manual, or auto")
	}
	environment := strings.TrimSpace(payload.Environment)
	if environment == "" {
		environment = "paper"
	}
	if environment != "paper" && environment != "testnet" && environment != "live" {
		return validatedStrategyInstance{}, invalidStrategy("environment must be paper, testnet, or live")
	}
	liveManualEnabled := (market == string(marketdata.MarketTypeSpot) && spotLiveManualEnabled) ||
		(market == string(marketdata.MarketTypeUSDM) && usdmLiveManualEnabled)
	liveAutoEnabled := (market == string(marketdata.MarketTypeSpot) && spotLiveAutoEnabled) ||
		(market == string(marketdata.MarketTypeUSDM) && usdmLiveAutoEnabled)
	if environment == "live" && mode == "auto" &&
		!liveAutoEnabled {
		return validatedStrategyInstance{}, invalidStrategy("Live auto trading is not enabled for this market")
	}
	if environment == "live" && mode != "signal_only" && !liveManualEnabled {
		return validatedStrategyInstance{}, invalidStrategy("Live manual trading is not enabled for this market")
	}
	parameters := payload.Parameters
	if parameters == nil {
		parameters = map[string]json.RawMessage{}
	}
	validatedParameters, err := validateParameters(parameters, version.ParameterSchemaJSON)
	if err != nil {
		return validatedStrategyInstance{}, err
	}
	var tradingAccountID *uuid.UUID
	var allocationUSDT *decimal.Decimal
	var stopLossRatio *decimal.Decimal
	if mode != "signal_only" && (environment == "paper" || environment == "testnet" ||
		(environment == "live" && liveManualEnabled)) {
		accountID, err := requiredStrategyUUID(payload.TradingAccountID, "tradingAccountId")
		if err != nil {
			return validatedStrategyInstance{}, err
		}
		allocation, err := parseDecimalField(payload.AllocationUSDT, "allocationUsdt", false)
		if err != nil || !allocation.IsPositive() {
			return validatedStrategyInstance{}, invalidStrategy("allocationUsdt must be a positive decimal string")
		}
		tradingAccountID = &accountID
		allocationUSDT = &allocation
		stopLossValue := strings.TrimSpace(payload.StopLossRatio)
		if isPrivateTradingEnvironment(environment) && stopLossValue != "" {
			ratio, err := parseDecimalField(stopLossValue, "stopLossRatio", false)
			if err != nil || !ratio.IsPositive() || !ratio.LessThan(decimal.NewFromInt(1)) {
				return validatedStrategyInstance{}, invalidStrategy("stopLossRatio must be a decimal string greater than 0 and less than 1")
			}
			stopLossRatio = &ratio
		} else if isPrivateTradingEnvironment(environment) {
			return validatedStrategyInstance{}, invalidStrategy("stopLossRatio is required for executable private instances")
		} else if stopLossValue != "" {
			return validatedStrategyInstance{}, invalidStrategy("stopLossRatio is only available for executable private instances")
		}
	} else if strings.TrimSpace(payload.TradingAccountID) != "" || strings.TrimSpace(payload.AllocationUSDT) != "" {
		return validatedStrategyInstance{}, invalidStrategy("trading account binding is not available for this strategy environment")
	} else if strings.TrimSpace(payload.StopLossRatio) != "" {
		return validatedStrategyInstance{}, invalidStrategy("stopLossRatio is only available for executable strategy instances")
	}
	return validatedStrategyInstance{
		Name: name, Mode: mode, Environment: environment, ParametersJSON: string(validatedParameters),
		TradingAccountID: tradingAccountID, AllocationUSDT: allocationUSDT, StopLossRatio: stopLossRatio,
	}, nil
}

func (a *App) ListStrategyInstances(ctx context.Context, userID int64, page CursorPage) (CursorResult[StrategyInstanceView], error) {
	if userID <= 0 {
		return CursorResult[StrategyInstanceView]{}, invalidStrategy("owner is required")
	}
	if err := validateCursorLimit(page); err != nil {
		return CursorResult[StrategyInstanceView]{}, err
	}
	after, err := parseOptionalUUIDv7(page.After, "cursor")
	if err != nil {
		return CursorResult[StrategyInstanceView]{}, invalidStrategy(err.Error())
	}
	query := a.dbWithContext(ctx).Table("strategy_instances AS instance").Where("instance.owner_user_id = ? AND instance.archived_at IS NULL", userID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return CursorResult[StrategyInstanceView]{}, err
	}
	if after != uuid.Nil {
		query = query.Where("instance.id < ?", after)
	}
	var rows []strategyInstanceListRow
	if err := query.Select(`
instance.*, instrument.native_symbol AS symbol,
version.name AS strategy_name, version.version_number AS strategy_version_number
`).Joins("JOIN strategy_versions AS version ON version.id = instance.strategy_version_id").
		Joins("JOIN market_instruments AS instrument ON instrument.id = instance.instrument_id").
		Order("instance.id DESC").Limit(page.Limit + 1).Scan(&rows).Error; err != nil {
		return CursorResult[StrategyInstanceView]{}, err
	}
	hasMore := len(rows) > page.Limit
	if hasMore {
		rows = rows[:page.Limit]
	}
	items := make([]StrategyInstanceView, 0, len(rows))
	for i := range rows {
		items = append(items, serializeStrategyInstanceList(rows[i]))
	}
	lastKey := ""
	if len(rows) > 0 {
		lastKey = rows[len(rows)-1].ID.String()
	}
	return typedCursorResult(items, page, lastKey, hasMore, total), nil
}

func (a *App) ListStrategySignals(ctx context.Context, userID int64, request StrategySignalQuery) (CursorResult[StrategySignalView], error) {
	if userID <= 0 {
		return CursorResult[StrategySignalView]{}, invalidStrategy("owner is required")
	}
	if err := validateCursorLimit(request.Page); err != nil {
		return CursorResult[StrategySignalView]{}, err
	}
	after, err := parseOptionalUUIDv7(request.Page.After, "cursor")
	if err != nil {
		return CursorResult[StrategySignalView]{}, invalidStrategy(err.Error())
	}
	instrumentID, err := parseOptionalUUIDv7(request.InstrumentID, "instrumentId")
	if err != nil {
		return CursorResult[StrategySignalView]{}, invalidStrategy(err.Error())
	}
	instanceID, err := parseOptionalUUIDv7(request.StrategyInstanceID, "strategyInstanceId")
	if err != nil {
		return CursorResult[StrategySignalView]{}, invalidStrategy(err.Error())
	}
	interval := strings.TrimSpace(request.Interval)
	if interval != "" {
		if _, ok := marketdata.CandleIntervalDuration(marketdata.CandleInterval(interval)); !ok {
			return CursorResult[StrategySignalView]{}, invalidStrategy("interval is not supported")
		}
	}
	startTime, err := parseOptionalUTCTime(request.StartTime, "startTime")
	if err != nil {
		return CursorResult[StrategySignalView]{}, invalidStrategy(err.Error())
	}
	endTime, err := parseOptionalUTCTime(request.EndTime, "endTime")
	if err != nil {
		return CursorResult[StrategySignalView]{}, invalidStrategy(err.Error())
	}
	if startTime != nil && endTime != nil && !startTime.Before(*endTime) {
		return CursorResult[StrategySignalView]{}, invalidStrategy("startTime must be before endTime")
	}
	query := a.dbWithContext(ctx).Table("strategy_signals AS signal").Where("signal.owner_user_id = ?", userID)
	if instrumentID != uuid.Nil {
		query = query.Where("signal.instrument_id = ?", instrumentID)
	}
	if instanceID != uuid.Nil {
		query = query.Where("signal.strategy_instance_id = ?", instanceID)
	}
	if interval != "" {
		query = query.Where("signal.interval_code = ?", interval)
	}
	if startTime != nil {
		query = query.Where("signal.candle_open_time >= ?", *startTime)
	}
	if endTime != nil {
		query = query.Where("signal.candle_open_time < ?", *endTime)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return CursorResult[StrategySignalView]{}, err
	}
	if after != uuid.Nil {
		query = query.Where("signal.id < ?", after)
	}
	var rows []strategySignalListRow
	if err := query.Select(`
signal.*,
(SELECT previous.target FROM strategy_signals AS previous
 WHERE previous.strategy_instance_id = signal.strategy_instance_id
   AND previous.candle_open_time < signal.candle_open_time
 ORDER BY previous.candle_open_time DESC LIMIT 1) AS previous_target
`).Order("signal.id DESC").Limit(request.Page.Limit + 1).Scan(&rows).Error; err != nil {
		return CursorResult[StrategySignalView]{}, err
	}
	hasMore := len(rows) > request.Page.Limit
	if hasMore {
		rows = rows[:request.Page.Limit]
	}
	items := make([]StrategySignalView, 0, len(rows))
	for i := range rows {
		items = append(items, serializeStrategySignalWithPrevious(rows[i].StrategySignal, rows[i].PreviousTarget))
	}
	lastKey := ""
	if len(rows) > 0 {
		lastKey = rows[len(rows)-1].ID.String()
	}
	return typedCursorResult(items, request.Page, lastKey, hasMore, total), nil
}

// DecideStrategySignal 只记录人工决策；执行仍由独立且默认关闭的边界负责。
func (a *App) DecideStrategySignal(
	ctx context.Context,
	principal *Principal,
	rawID, decision, idempotencyKey, reauthToken string,
) (StrategySignalView, error) {
	if principal == nil || principal.User == nil || principal.User.ID <= 0 {
		return StrategySignalView{}, invalidStrategy("owner is required")
	}
	signalID, err := requiredStrategyUUID(rawID, "signalId")
	if err != nil {
		return StrategySignalView{}, err
	}
	if decision != "approved" && decision != "rejected" {
		return StrategySignalView{}, invalidStrategy("decision must be approved or rejected")
	}
	normalizedKey, err := normalizeIdempotencyKey(idempotencyKey)
	if err != nil {
		return StrategySignalView{}, err
	}
	requestHash, err := canonicalRequestHash(M{"signalId": signalID.String(), "decision": decision})
	if err != nil {
		return StrategySignalView{}, err
	}

	var row db.StrategySignal
	// 过期决策仍返回冲突，但会提交 expired 状态，避免把已失效信号继续暴露为 active。
	var decisionErr error
	err = a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_user_id = ?", signalID, principal.User.ID).
			Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrStrategySignalMissing
			}
			return err
		}
		scope := "strategy-signal:decision:" + signalID.String()
		if row.DecisionIdempotencyRecordID != nil {
			var record db.IdempotencyRecord
			if err := tx.Where("id = ?", *row.DecisionIdempotencyRecordID).Take(&record).Error; err != nil {
				return err
			}
			if row.Status == decision && record.UserID == principal.User.ID && record.Scope == scope &&
				record.KeyHash == hashIdempotencyKey(normalizedKey) && record.RequestHash == requestHash {
				return nil
			}
			return ErrStrategySignalConflict
		}
		if row.Mode != "manual" || row.Status != "active" || row.ExpiresAt == nil {
			return ErrStrategySignalConflict
		}
		var now time.Time
		if err := tx.Raw("SELECT CURRENT_TIMESTAMP").Scan(&now).Error; err != nil {
			return err
		}
		now = now.UTC()
		if !row.ExpiresAt.After(now) {
			result := tx.Model(&db.StrategySignal{}).
				Where("id = ? AND owner_user_id = ? AND status = 'active'", signalID, principal.User.ID).
				Update("status", "expired")
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrStrategySignalConflict
			}
			decisionErr = ErrStrategySignalConflict
			return nil
		}
		record, reused, err := a.reserveIdempotencyRecord(
			tx, principal.User.ID, scope, normalizedKey, requestHash,
		)
		if err != nil {
			return err
		}
		if reused {
			return ErrStrategySignalConflict
		}
		if decision == "approved" && !a.ConsumeReauthToken(reauthToken, principal) {
			return ErrStrategySignalReauthentication
		}

		result := tx.Model(&db.StrategySignal{}).
			Where("id = ? AND owner_user_id = ? AND mode = 'manual' AND status = 'active' AND expires_at > ?", signalID, principal.User.ID, now).
			Updates(map[string]any{
				"status":                         decision,
				"decision_idempotency_record_id": record.ID,
				"decided_by_user_id":             principal.User.ID,
				"decided_at":                     now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrStrategySignalConflict
		}
		if err := tx.Where("id = ?", signalID).Take(&row).Error; err != nil {
			return err
		}
		if decision == "approved" {
			return a.createTradingIntentForSignalWithDB(tx, row, true)
		}
		return nil
	})
	if err != nil {
		return StrategySignalView{}, err
	}
	if decisionErr != nil {
		return StrategySignalView{}, decisionErr
	}
	return serializeStrategySignal(row), nil
}

func serializeStrategyInstance(row db.StrategyInstance) StrategyInstanceView {
	view := StrategyInstanceView{
		ID: row.ID.String(), OwnerUserID: row.OwnerUserID, StrategyVersionID: row.StrategyVersionID.String(),
		Name: row.Name, Mode: row.Mode, Environment: row.Environment,
		Parameters: rawJSON(row.ParametersJSON, `{}`), IsEnabled: row.IsEnabled,
		CreatedAt: formatUTC(row.CreatedAt), UpdatedAt: formatUTC(row.UpdatedAt),
		Market: row.Market, InstrumentID: row.InstrumentID.String(), Interval: row.Interval,
		WorkflowDefinitionID: row.WorkflowDefinitionID, WorkflowNodeID: row.WorkflowNodeID,
	}
	if row.TradingAccountID != nil {
		value := row.TradingAccountID.String()
		view.TradingAccountID = &value
	}
	if row.AllocationUSDT != nil {
		value := row.AllocationUSDT.String()
		view.AllocationUSDT = &value
	}
	if row.StopLossRatio != nil {
		value := row.StopLossRatio.String()
		view.StopLossRatio = &value
	}
	return view
}

func serializeStrategyInstanceList(row strategyInstanceListRow) StrategyInstanceView {
	view := serializeStrategyInstance(row.StrategyInstance)
	view.Symbol = row.Symbol
	view.StrategyName = row.StrategyName
	view.StrategyVersion = row.StrategyVersionNumber
	return view
}

func serializeStrategySignal(row db.StrategySignal) StrategySignalView {
	return serializeStrategySignalWithPrevious(row, nil)
}

func serializeStrategySignalWithPrevious(row db.StrategySignal, previous *decimal.Decimal) StrategySignalView {
	previousTarget := decimal.Zero
	if previous != nil {
		previousTarget = *previous
	}
	change := row.Target.Sub(previousTarget)
	action := "hold"
	if !change.IsZero() && row.Target.IsZero() {
		action = "flat"
	} else if change.IsPositive() {
		action = "buy"
	} else if change.IsNegative() {
		action = "sell"
	}
	view := StrategySignalView{
		ID: row.ID.String(), StrategyInstanceID: row.StrategyInstanceID.String(),
		StrategyVersionID: row.StrategyVersionID.String(), InstrumentID: row.InstrumentID.String(),
		Interval: row.Interval, CandleOpenTime: formatUTC(row.CandleOpenTime),
		CandleCloseTime: formatUTC(row.CandleCloseTime), Target: row.Target.String(),
		PreviousTarget: previousTarget.String(), TargetChange: change.String(), Action: action,
		Mode: row.Mode, Environment: row.Environment, Status: row.Status, CreatedAt: formatUTC(row.CreatedAt),
	}
	if row.ExpiresAt != nil {
		value := formatUTC(*row.ExpiresAt)
		view.ExpiresAt = &value
	}
	if row.DecidedAt != nil {
		value := formatUTC(*row.DecidedAt)
		view.DecidedAt = &value
	}
	return view
}
