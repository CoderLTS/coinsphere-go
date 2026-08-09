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
	TradingAccountID  string                     `json:"tradingAccountId"`
	AllocationUSDT    string                     `json:"allocationUsdt"`
	StopLossRatio     string                     `json:"stopLossRatio"`
	Name              string                     `json:"name"`
	Mode              string                     `json:"mode"`
	Environment       string                     `json:"environment"`
	Parameters        map[string]json.RawMessage `json:"parameters"`
}

type StrategyInstanceView struct {
	ID                string          `json:"id"`
	OwnerUserID       int64           `json:"ownerUserId"`
	StrategyVersionID string          `json:"strategyVersionId"`
	TradingAccountID  *string         `json:"tradingAccountId"`
	AllocationUSDT    *string         `json:"allocationUsdt"`
	StopLossRatio     *string         `json:"stopLossRatio"`
	Name              string          `json:"name"`
	Mode              string          `json:"mode"`
	Environment       string          `json:"environment"`
	Parameters        json.RawMessage `json:"parameters"`
	IsEnabled         bool            `json:"isEnabled"`
	CreatedAt         string          `json:"createdAt"`
	UpdatedAt         string          `json:"updatedAt"`
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
	Mode               string  `json:"mode"`
	Environment        string  `json:"environment"`
	Status             string  `json:"status"`
	ExpiresAt          *string `json:"expiresAt,omitempty"`
	DecidedAt          *string `json:"decidedAt,omitempty"`
	CreatedAt          string  `json:"createdAt"`
}

type validatedStrategyInstance struct {
	Name, Mode, Environment, ParametersJSON string
	TradingAccountID                        *uuid.UUID
	AllocationUSDT                          *decimal.Decimal
	StopLossRatio                           *decimal.Decimal
}

func validateStrategyInstancePayload(
	payload StrategyInstanceCreatePayload, version db.StrategyVersion,
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
	if mode != "signal_only" && (environment == "paper" || environment == "testnet") {
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
		if environment == "testnet" && stopLossValue != "" {
			ratio, err := parseDecimalField(stopLossValue, "stopLossRatio", false)
			if err != nil || !ratio.IsPositive() || !ratio.LessThan(decimal.NewFromInt(1)) {
				return validatedStrategyInstance{}, invalidStrategy("stopLossRatio must be a decimal string greater than 0 and less than 1")
			}
			stopLossRatio = &ratio
		} else if environment == "testnet" {
			return validatedStrategyInstance{}, invalidStrategy("stopLossRatio is required for executable testnet instances")
		} else if stopLossValue != "" {
			return validatedStrategyInstance{}, invalidStrategy("stopLossRatio is only available for executable testnet instances")
		}
	} else if strings.TrimSpace(payload.TradingAccountID) != "" || strings.TrimSpace(payload.AllocationUSDT) != "" {
		return validatedStrategyInstance{}, invalidStrategy("trading account binding is only available for manual or auto paper or testnet instances")
	} else if strings.TrimSpace(payload.StopLossRatio) != "" {
		return validatedStrategyInstance{}, invalidStrategy("stopLossRatio is only available for executable strategy instances")
	}
	return validatedStrategyInstance{
		Name: name, Mode: mode, Environment: environment, ParametersJSON: string(validatedParameters),
		TradingAccountID: tradingAccountID, AllocationUSDT: allocationUSDT, StopLossRatio: stopLossRatio,
	}, nil
}

func (a *App) CreateStrategyInstance(ctx context.Context, userID int64, payload StrategyInstanceCreatePayload) (StrategyInstanceView, error) {
	if userID <= 0 {
		return StrategyInstanceView{}, invalidStrategy("owner is required")
	}
	versionID, err := requiredStrategyUUID(payload.StrategyVersionID, "strategyVersionId")
	if err != nil {
		return StrategyInstanceView{}, err
	}
	var version db.StrategyVersion
	if err := a.dbWithContext(ctx).Where("id = ? AND status = 'published'", versionID).Take(&version).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return StrategyInstanceView{}, ErrStrategyVersionMissing
		}
		return StrategyInstanceView{}, err
	}
	validated, err := validateStrategyInstancePayload(payload, version)
	if err != nil {
		return StrategyInstanceView{}, err
	}
	if validated.TradingAccountID != nil {
		var account db.TradingAccount
		if err := a.dbWithContext(ctx).Where(
			"id = ? AND owner_user_id = ? AND market_type = ? AND environment = ?",
			*validated.TradingAccountID, userID, version.Market, validated.Environment,
		).Take(&account).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return StrategyInstanceView{}, ErrTradingAccountMissing
			}
			return StrategyInstanceView{}, err
		}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return StrategyInstanceView{}, err
	}
	now := time.Now().UTC()
	row := db.StrategyInstance{
		ID: id, OwnerUserID: userID, StrategyVersionID: versionID, Name: validated.Name,
		TradingAccountID: validated.TradingAccountID, AllocationUSDT: validated.AllocationUSDT,
		StopLossRatio: validated.StopLossRatio,
		Mode:          validated.Mode, Environment: validated.Environment,
		ParametersJSON: validated.ParametersJSON,
		IsEnabled:      false, CreatedAt: now, UpdatedAt: now,
	}
	if err := a.dbWithContext(ctx).Create(&row).Error; err != nil {
		return StrategyInstanceView{}, err
	}
	return serializeStrategyInstance(row), nil
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
	query := a.dbWithContext(ctx).Where("owner_user_id = ?", userID)
	var total int64
	if err := query.Model(&db.StrategyInstance{}).Count(&total).Error; err != nil {
		return CursorResult[StrategyInstanceView]{}, err
	}
	if after != uuid.Nil {
		query = query.Where("id < ?", after)
	}
	var rows []db.StrategyInstance
	if err := query.Order("id DESC").Limit(page.Limit + 1).Find(&rows).Error; err != nil {
		return CursorResult[StrategyInstanceView]{}, err
	}
	hasMore := len(rows) > page.Limit
	if hasMore {
		rows = rows[:page.Limit]
	}
	items := make([]StrategyInstanceView, 0, len(rows))
	for i := range rows {
		items = append(items, serializeStrategyInstance(rows[i]))
	}
	lastKey := ""
	if len(rows) > 0 {
		lastKey = rows[len(rows)-1].ID.String()
	}
	return typedCursorResult(items, page, lastKey, hasMore, total), nil
}

func (a *App) SetStrategyInstanceEnabled(ctx context.Context, userID int64, rawID string, enabled bool) (StrategyInstanceView, error) {
	if userID <= 0 {
		return StrategyInstanceView{}, invalidStrategy("owner is required")
	}
	id, err := requiredStrategyUUID(rawID, "instanceId")
	if err != nil {
		return StrategyInstanceView{}, err
	}
	var row db.StrategyInstance
	err = a.dbWithContext(ctx).Where("id = ? AND owner_user_id = ?", id, userID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return StrategyInstanceView{}, ErrStrategyInstanceMissing
	}
	if err != nil {
		return StrategyInstanceView{}, err
	}
	if enabled {
		if err := a.validateStrategyInstanceExecutionReady(a.dbWithContext(ctx), row); err != nil {
			return StrategyInstanceView{}, err
		}
	}
	row.IsEnabled = enabled
	row.UpdatedAt = time.Now().UTC()
	if err := a.dbWithContext(ctx).Save(&row).Error; err != nil {
		return StrategyInstanceView{}, err
	}
	return serializeStrategyInstance(row), nil
}

func (a *App) ListStrategySignals(ctx context.Context, userID int64, page CursorPage) (CursorResult[StrategySignalView], error) {
	if userID <= 0 {
		return CursorResult[StrategySignalView]{}, invalidStrategy("owner is required")
	}
	if err := validateCursorLimit(page); err != nil {
		return CursorResult[StrategySignalView]{}, err
	}
	after, err := parseOptionalUUIDv7(page.After, "cursor")
	if err != nil {
		return CursorResult[StrategySignalView]{}, invalidStrategy(err.Error())
	}
	query := a.dbWithContext(ctx).Where("owner_user_id = ?", userID)
	var total int64
	if err := query.Model(&db.StrategySignal{}).Count(&total).Error; err != nil {
		return CursorResult[StrategySignalView]{}, err
	}
	if after != uuid.Nil {
		query = query.Where("id < ?", after)
	}
	var rows []db.StrategySignal
	if err := query.Order("id DESC").Limit(page.Limit + 1).Find(&rows).Error; err != nil {
		return CursorResult[StrategySignalView]{}, err
	}
	hasMore := len(rows) > page.Limit
	if hasMore {
		rows = rows[:page.Limit]
	}
	items := make([]StrategySignalView, 0, len(rows))
	for i := range rows {
		items = append(items, serializeStrategySignal(rows[i]))
	}
	lastKey := ""
	if len(rows) > 0 {
		lastKey = rows[len(rows)-1].ID.String()
	}
	return typedCursorResult(items, page, lastKey, hasMore, total), nil
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

// EnqueueRealtimeSignals is called only after the market store confirms a first close.
func (a *App) EnqueueRealtimeSignals(ctx context.Context, candle marketdata.Candle) error {
	return a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var instances []db.StrategyInstance
		if err := tx.Raw(`
SELECT instance.*
FROM strategy_instances AS instance
JOIN strategy_versions AS version ON version.id = instance.strategy_version_id
WHERE instance.is_enabled
  AND version.status = 'published'
  AND version.instrument_id = ?
  AND version.interval_code = ?
ORDER BY instance.id
`, candle.InstrumentID, candle.Interval).Scan(&instances).Error; err != nil {
			return err
		}
		for _, instance := range instances {
			taskID, err := uuid.NewV7()
			if err != nil {
				return err
			}
			openTime := formatUTC(candle.OpenTime)
			payload, err := json.Marshal(M{"instanceId": instance.ID.String(), "candleOpenTime": openTime})
			if err != nil {
				return err
			}
			result := tx.Exec(
				"INSERT INTO worker_tasks (id, task_type, payload_json, lane, dedupe_key) VALUES (?, 'strategy.realtime', ?, 'realtime', ?) ON CONFLICT DO NOTHING",
				taskID.String(), string(payload), "strategy.realtime:"+instance.ID.String()+":"+openTime,
			)
			if result.Error != nil {
				return result.Error
			}
		}
		return nil
	})
}

func serializeStrategyInstance(row db.StrategyInstance) StrategyInstanceView {
	view := StrategyInstanceView{
		ID: row.ID.String(), OwnerUserID: row.OwnerUserID, StrategyVersionID: row.StrategyVersionID.String(),
		Name: row.Name, Mode: row.Mode, Environment: row.Environment,
		Parameters: rawJSON(row.ParametersJSON, `{}`), IsEnabled: row.IsEnabled,
		CreatedAt: formatUTC(row.CreatedAt), UpdatedAt: formatUTC(row.UpdatedAt),
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

func serializeStrategySignal(row db.StrategySignal) StrategySignalView {
	view := StrategySignalView{
		ID: row.ID.String(), StrategyInstanceID: row.StrategyInstanceID.String(),
		StrategyVersionID: row.StrategyVersionID.String(), InstrumentID: row.InstrumentID.String(),
		Interval: row.Interval, CandleOpenTime: formatUTC(row.CandleOpenTime),
		CandleCloseTime: formatUTC(row.CandleCloseTime), Target: row.Target.String(),
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
