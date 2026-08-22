package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/marketdata"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	strategyRuntimeVersion   = "python3.12"
	backtestSimulatorVersion = "decimal-bar-v1"
)

var (
	ErrInvalidStrategyRequest    = errors.New("invalid strategy or backtest request")
	ErrStrategyDraftMissing      = errors.New("strategy draft was not found")
	ErrStrategyInstrumentMissing = errors.New("strategy instrument was not found")
	ErrStrategyVersionMissing    = errors.New("published strategy version was not found")
	ErrBacktestMissing           = errors.New("backtest was not found")
	ErrBacktestConflict          = errors.New("backtest state does not allow this operation")

	signedDecimalPattern = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)
)

type StrategyDraftPayload struct {
	Name            string                     `json:"name"`
	SourceCode      string                     `json:"sourceCode"`
	LookbackBars    int                        `json:"lookbackBars"`
	ParameterSchema map[string]json.RawMessage `json:"parameterSchema"`
}

type StrategyDraftView struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	SourceCode      string          `json:"sourceCode"`
	LookbackBars    int             `json:"lookbackBars"`
	ParameterSchema json.RawMessage `json:"parameterSchema"`
	RuntimeVersion  string          `json:"runtimeVersion"`
	CreatedAt       string          `json:"createdAt"`
	UpdatedAt       string          `json:"updatedAt"`
}

type StrategyVersionView struct {
	ID              string          `json:"id"`
	StrategyID      string          `json:"strategyId"`
	VersionNumber   int             `json:"versionNumber"`
	Status          string          `json:"status"`
	Name            string          `json:"name"`
	SourceCode      string          `json:"sourceCode"`
	CodeSHA256      string          `json:"codeSha256"`
	RuntimeVersion  string          `json:"runtimeVersion"`
	LookbackBars    int             `json:"lookbackBars"`
	ParameterSchema json.RawMessage `json:"parameterSchema"`
	PublishedAt     *string         `json:"publishedAt,omitempty"`
	CreatedAt       string          `json:"createdAt"`
}

type BacktestCreatePayload struct {
	StrategyVersionID      string                     `json:"strategyVersionId"`
	InstrumentID           string                     `json:"instrumentId"`
	Interval               string                     `json:"interval"`
	Parameters             map[string]json.RawMessage `json:"parameters"`
	StartTime              string                     `json:"startTime"`
	EndTime                string                     `json:"endTime"`
	AllocationUSDT         string                     `json:"allocationUsdt"`
	InitialEquity          string                     `json:"initialEquity"`
	FeeRate                string                     `json:"feeRate"`
	SlippageRate           string                     `json:"slippageRate"`
	FundingRates           []string                   `json:"fundingRates"`
	StopLossRatio          string                     `json:"stopLossRatio"`
	MaintenanceMarginRatio string                     `json:"maintenanceMarginRatio"`
}

type BacktestView struct {
	ID                     string           `json:"id"`
	OwnerUserID            int64            `json:"ownerUserId"`
	StrategyVersionID      string           `json:"strategyVersionId"`
	InstrumentID           string           `json:"instrumentId"`
	Market                 string           `json:"market"`
	Symbol                 string           `json:"symbol"`
	Interval               string           `json:"interval"`
	SimulatorVersion       string           `json:"simulatorVersion"`
	Status                 string           `json:"status"`
	FailureCategory        *string          `json:"failureCategory,omitempty"`
	Parameters             json.RawMessage  `json:"parameters"`
	StartTime              string           `json:"startTime"`
	EndTime                string           `json:"endTime"`
	AllocationUSDT         string           `json:"allocationUsdt"`
	InitialEquity          string           `json:"initialEquity"`
	FeeRate                string           `json:"feeRate"`
	SlippageRate           string           `json:"slippageRate"`
	FundingRates           json.RawMessage  `json:"fundingRates,omitempty"`
	StopLossRatio          *string          `json:"stopLossRatio,omitempty"`
	MaintenanceMarginRatio *string          `json:"maintenanceMarginRatio,omitempty"`
	Summary                *json.RawMessage `json:"summary,omitempty"`
	InputSHA256            *string          `json:"inputSha256,omitempty"`
	ResultSHA256           *string          `json:"resultSha256,omitempty"`
	ManifestSHA256         *string          `json:"manifestSha256,omitempty"`
	CreatedAt              string           `json:"createdAt"`
}

type backtestTaskState struct {
	Status          string
	FailureCategory *string
}

func (a *App) ListStrategyDrafts(ctx context.Context, page CursorPage) (CursorResult[StrategyDraftView], error) {
	if err := validateCursorLimit(page); err != nil {
		return CursorResult[StrategyDraftView]{}, invalidStrategy(err.Error())
	}
	after, err := parseOptionalUUIDv7(page.After, "cursor")
	if err != nil {
		return CursorResult[StrategyDraftView]{}, invalidStrategy(err.Error())
	}
	query := a.dbWithContext(ctx).Model(&db.StrategyDraft{}).Where("archived_at IS NULL")
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return CursorResult[StrategyDraftView]{}, err
	}
	if after != uuid.Nil {
		query = query.Where("id < ?", after)
	}
	var rows []db.StrategyDraft
	if err := query.Order("id DESC").Limit(page.Limit + 1).Find(&rows).Error; err != nil {
		return CursorResult[StrategyDraftView]{}, err
	}
	hasMore := len(rows) > page.Limit
	if hasMore {
		rows = rows[:page.Limit]
	}
	records := make([]StrategyDraftView, 0, len(rows))
	for i := range rows {
		records = append(records, serializeStrategyDraft(rows[i]))
	}
	lastKey := ""
	if len(rows) > 0 {
		lastKey = rows[len(rows)-1].ID.String()
	}
	return typedCursorResult(records, page, lastKey, hasMore, total), nil
}

func (a *App) CreateStrategyDraft(ctx context.Context, userID int64, payload StrategyDraftPayload) (StrategyDraftView, error) {
	validated, err := a.validateStrategyDraft(payload)
	if err != nil {
		return StrategyDraftView{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return StrategyDraftView{}, err
	}
	now := time.Now().UTC()
	row := db.StrategyDraft{
		ID: id, Name: validated.Name, SourceCode: validated.SourceCode, LookbackBars: validated.LookbackBars,
		ParameterSchemaJSON: validated.ParameterSchemaJSON, RuntimeVersion: strategyRuntimeVersion,
		CreatedByUserID: userID, UpdatedByUserID: userID, CreatedAt: now, UpdatedAt: now,
	}
	if err := a.dbWithContext(ctx).Create(&row).Error; err != nil {
		return StrategyDraftView{}, err
	}
	return serializeStrategyDraft(row), nil
}

func (a *App) GetStrategyDraft(ctx context.Context, rawID string) (StrategyDraftView, error) {
	id, err := requiredStrategyUUID(rawID, "strategyId")
	if err != nil {
		return StrategyDraftView{}, err
	}
	var row db.StrategyDraft
	if err := a.dbWithContext(ctx).Where("id = ? AND archived_at IS NULL", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return StrategyDraftView{}, ErrStrategyDraftMissing
		}
		return StrategyDraftView{}, err
	}
	return serializeStrategyDraft(row), nil
}

func (a *App) ArchiveStrategyDraft(ctx context.Context, rawID string) error {
	id, err := requiredStrategyUUID(rawID, "strategyId")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	result := a.dbWithContext(ctx).Model(&db.StrategyDraft{}).
		Where("id = ? AND archived_at IS NULL", id).
		Updates(map[string]any{"archived_at": now, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrStrategyDraftMissing
	}
	return nil
}

func (a *App) UpdateStrategyDraft(ctx context.Context, userID int64, rawID string, payload StrategyDraftPayload) (StrategyDraftView, error) {
	id, err := requiredStrategyUUID(rawID, "strategyId")
	if err != nil {
		return StrategyDraftView{}, err
	}
	validated, err := a.validateStrategyDraft(payload)
	if err != nil {
		return StrategyDraftView{}, err
	}
	var row db.StrategyDraft
	err = a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND archived_at IS NULL", id).Take(&row).Error; err != nil {
			return err
		}
		row.Name = validated.Name
		row.SourceCode = validated.SourceCode
		row.LookbackBars = validated.LookbackBars
		row.ParameterSchemaJSON = validated.ParameterSchemaJSON
		row.RuntimeVersion = strategyRuntimeVersion
		row.UpdatedByUserID = userID
		row.UpdatedAt = time.Now().UTC()
		return tx.Save(&row).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return StrategyDraftView{}, ErrStrategyDraftMissing
	}
	if err != nil {
		return StrategyDraftView{}, err
	}
	return serializeStrategyDraft(row), nil
}

func (a *App) PublishStrategy(ctx context.Context, userID int64, rawID, idempotencyKey string) (StrategyVersionView, error) {
	strategyID, err := requiredStrategyUUID(rawID, "strategyId")
	if err != nil {
		return StrategyVersionView{}, err
	}
	requestHash, err := canonicalRequestHash(M{"strategyId": strategyID.String()})
	if err != nil {
		return StrategyVersionView{}, err
	}
	var version db.StrategyVersion
	err = a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, reused, err := a.reserveIdempotencyRecord(tx, userID, "strategy:publish:"+strategyID.String(), idempotencyKey, requestHash)
		if err != nil {
			return err
		}
		if reused {
			if err := tx.Where("idempotency_record_id = ?", record.ID).Take(&version).Error; err != nil {
				return err
			}
			return nil
		}

		var draft db.StrategyDraft
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND archived_at IS NULL", strategyID).Take(&draft).Error; err != nil {
			return err
		}
		var latest int
		if err := tx.Model(&db.StrategyVersion{}).Where("strategy_id = ?", strategyID).
			Select("COALESCE(MAX(version_number), 0)").Scan(&latest).Error; err != nil {
			return err
		}
		versionID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		taskID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		payloadJSON, _ := json.Marshal(M{"strategyId": strategyID.String(), "strategyVersionId": versionID.String()})
		if err := tx.Exec(
			"INSERT INTO worker_tasks (id, task_type, payload_json, lane) VALUES (?, 'strategy.publish', ?, 'backtest')",
			taskID.String(), string(payloadJSON),
		).Error; err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(draft.SourceCode))
		version = db.StrategyVersion{
			ID: versionID, StrategyID: strategyID, VersionNumber: latest + 1, Status: "pending",
			WorkerTaskID: taskID.String(), IdempotencyRecordID: record.ID, Name: draft.Name,
			SourceCode: draft.SourceCode, CodeSHA256: hex.EncodeToString(sum[:]), RuntimeVersion: strategyRuntimeVersion,
			LookbackBars: draft.LookbackBars, ParameterSchemaJSON: draft.ParameterSchemaJSON,
			PublishedByUserID: userID, CreatedAt: time.Now().UTC(),
		}
		return tx.Create(&version).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return StrategyVersionView{}, ErrStrategyDraftMissing
	}
	if err != nil {
		return StrategyVersionView{}, err
	}
	return serializeStrategyVersion(version), nil
}

func (a *App) ListPublishedStrategies(ctx context.Context, page CursorPage) (CursorResult[StrategyVersionView], error) {
	if err := validateCursorLimit(page); err != nil {
		return CursorResult[StrategyVersionView]{}, invalidStrategy(err.Error())
	}
	after, err := parseOptionalUUIDv7(page.After, "cursor")
	if err != nil {
		return CursorResult[StrategyVersionView]{}, invalidStrategy(err.Error())
	}
	query := a.dbWithContext(ctx).Model(&db.StrategyVersion{}).Where("status = 'published'")
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return CursorResult[StrategyVersionView]{}, err
	}
	if after != uuid.Nil {
		query = query.Where("id < ?", after)
	}
	var rows []db.StrategyVersion
	if err := query.Order("id DESC").Limit(page.Limit + 1).Find(&rows).Error; err != nil {
		return CursorResult[StrategyVersionView]{}, err
	}
	hasMore := len(rows) > page.Limit
	if hasMore {
		rows = rows[:page.Limit]
	}
	records := make([]StrategyVersionView, 0, len(rows))
	for i := range rows {
		records = append(records, serializeStrategyVersion(rows[i]))
	}
	lastKey := ""
	if len(rows) > 0 {
		lastKey = rows[len(rows)-1].ID.String()
	}
	return typedCursorResult(records, page, lastKey, hasMore, total), nil
}

func (a *App) GetPublishedStrategy(ctx context.Context, rawID string) (StrategyVersionView, error) {
	id, err := requiredStrategyUUID(rawID, "strategyVersionId")
	if err != nil {
		return StrategyVersionView{}, err
	}
	var row db.StrategyVersion
	if err := a.dbWithContext(ctx).Where("id = ? AND status = 'published'", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return StrategyVersionView{}, ErrStrategyVersionMissing
		}
		return StrategyVersionView{}, err
	}
	return serializeStrategyVersion(row), nil
}

func (a *App) CreateBacktest(ctx context.Context, userID int64, idempotencyKey string, payload BacktestCreatePayload) (BacktestView, error) {
	versionID, err := requiredStrategyUUID(payload.StrategyVersionID, "strategyVersionId")
	if err != nil {
		return BacktestView{}, err
	}
	var version db.StrategyVersion
	if err := a.dbWithContext(ctx).Where("id = ? AND status = 'published'", versionID).Take(&version).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return BacktestView{}, ErrStrategyVersionMissing
		}
		return BacktestView{}, err
	}
	instrumentID, err := requiredStrategyUUID(payload.InstrumentID, "instrumentId")
	if err != nil {
		return BacktestView{}, err
	}
	var instrument db.MarketInstrument
	if err := a.dbWithContext(ctx).Where(
		"id = ? AND venue = ? AND quote_asset = 'USDT'", instrumentID, marketdata.VenueBinance,
	).Take(&instrument).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return BacktestView{}, ErrStrategyInstrumentMissing
		}
		return BacktestView{}, err
	}
	validated, err := validateBacktestPayload(payload, version, instrument.Market)
	if err != nil {
		return BacktestView{}, err
	}
	requestHash, err := canonicalRequestHash(validated.Request)
	if err != nil {
		return BacktestView{}, err
	}
	var row db.Backtest
	err = a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, reused, err := a.reserveIdempotencyRecord(tx, userID, "backtest:create", idempotencyKey, requestHash)
		if err != nil {
			return err
		}
		if reused {
			return tx.Where("owner_user_id = ? AND idempotency_record_id = ?", userID, record.ID).Take(&row).Error
		}
		backtestID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		taskID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		taskPayload, _ := json.Marshal(M{"backtestId": backtestID.String()})
		if err := tx.Exec(
			"INSERT INTO worker_tasks (id, task_type, payload_json, lane) VALUES (?, 'strategy.backtest', ?, 'backtest')",
			taskID.String(), string(taskPayload),
		).Error; err != nil {
			return err
		}
		row = db.Backtest{
			ID: backtestID, OwnerUserID: userID, StrategyVersionID: versionID, WorkerTaskID: taskID.String(),
			InstrumentID: instrumentID, Interval: payload.Interval, Market: instrument.Market, Symbol: instrument.NativeSymbol,
			IdempotencyRecordID: record.ID, SimulatorVersion: backtestSimulatorVersion,
			ParametersJSON: validated.ParametersJSON,
			StartTime:      validated.StartTime, EndTime: validated.EndTime,
			AllocationUSDT: validated.AllocationUSDT, InitialEquity: validated.InitialEquity,
			FeeRate: validated.FeeRate, SlippageRate: validated.SlippageRate,
			FundingRatesJSON: validated.FundingRatesJSON, StopLossRatio: validated.StopLossRatio,
			MaintenanceMarginRatio: validated.MaintenanceMarginRatio, CreatedAt: time.Now().UTC(),
		}
		return tx.Create(&row).Error
	})
	if err != nil {
		return BacktestView{}, err
	}
	state, err := loadBacktestTaskState(a.dbWithContext(ctx), row.WorkerTaskID)
	if err != nil {
		return BacktestView{}, err
	}
	return serializeBacktest(row, state), nil
}

func (a *App) ListBacktests(ctx context.Context, userID int64, page CursorPage) (CursorResult[BacktestView], error) {
	if err := validateCursorLimit(page); err != nil {
		return CursorResult[BacktestView]{}, invalidStrategy(err.Error())
	}
	after, err := parseOptionalUUIDv7(page.After, "cursor")
	if err != nil {
		return CursorResult[BacktestView]{}, invalidStrategy(err.Error())
	}
	database := a.dbWithContext(ctx)
	query := database.Table("backtests AS backtest").Where("backtest.owner_user_id = ?", userID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return CursorResult[BacktestView]{}, err
	}
	if after != uuid.Nil {
		query = query.Where("backtest.id < ?", after)
	}
	var rows []db.Backtest
	if err := query.Select("backtest.*, instrument.market_type AS market, instrument.native_symbol AS symbol").
		Joins("JOIN market_instruments AS instrument ON instrument.id = backtest.instrument_id").
		Order("backtest.id DESC").Limit(page.Limit + 1).Scan(&rows).Error; err != nil {
		return CursorResult[BacktestView]{}, err
	}
	hasMore := len(rows) > page.Limit
	if hasMore {
		rows = rows[:page.Limit]
	}
	records := make([]BacktestView, 0, len(rows))
	for i := range rows {
		state, err := loadBacktestTaskState(database, rows[i].WorkerTaskID)
		if err != nil {
			return CursorResult[BacktestView]{}, err
		}
		records = append(records, serializeBacktest(rows[i], state))
	}
	lastKey := ""
	if len(rows) > 0 {
		lastKey = rows[len(rows)-1].ID.String()
	}
	return typedCursorResult(records, page, lastKey, hasMore, total), nil
}

func (a *App) GetBacktest(ctx context.Context, userID int64, rawID string) (BacktestView, error) {
	id, err := requiredStrategyUUID(rawID, "backtestId")
	if err != nil {
		return BacktestView{}, err
	}
	return getBacktestWithDB(a.dbWithContext(ctx), userID, id)
}

func (a *App) CancelBacktest(ctx context.Context, userID int64, rawID string) (BacktestView, error) {
	id, err := requiredStrategyUUID(rawID, "backtestId")
	if err != nil {
		return BacktestView{}, err
	}
	var result BacktestView
	err = a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row db.Backtest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_user_id = ?", id, userID).Take(&row).Error; err != nil {
			return err
		}
		state, err := loadBacktestTaskStateForUpdate(tx, row.WorkerTaskID)
		if err != nil {
			return err
		}
		switch state.Status {
		case "queued":
			err = tx.Exec(
				"UPDATE worker_tasks SET status = 'canceled', cancel_requested_at = CURRENT_TIMESTAMP, finished_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND status = 'queued'",
				row.WorkerTaskID,
			).Error
			state.Status = "canceled"
		case "claimed", "running":
			err = tx.Exec(
				"UPDATE worker_tasks SET status = 'cancelRequested', cancel_requested_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND status IN ('claimed', 'running')",
				row.WorkerTaskID,
			).Error
			state.Status = "cancelRequested"
		case "cancelRequested":
			// Repeated cancellation is idempotent while the Worker owns the request.
		default:
			return ErrBacktestConflict
		}
		if err != nil {
			return err
		}
		if err := loadBacktestBinding(tx, &row); err != nil {
			return err
		}
		result = serializeBacktest(row, state)
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return BacktestView{}, ErrBacktestMissing
	}
	return result, err
}

type validatedStrategyDraft struct {
	Name, SourceCode, ParameterSchemaJSON string
	LookbackBars                          int
}

func (a *App) validateStrategyDraft(payload StrategyDraftPayload) (validatedStrategyDraft, error) {
	name := strings.TrimSpace(payload.Name)
	if name == "" || len(name) > 120 || name != payload.Name {
		return validatedStrategyDraft{}, invalidStrategy("name must be trimmed and between 1 and 120 bytes")
	}
	if strings.TrimSpace(payload.SourceCode) == "" || len(payload.SourceCode) > 65536 {
		return validatedStrategyDraft{}, invalidStrategy("sourceCode must be between 1 and 65536 bytes")
	}
	if payload.LookbackBars < 1 || payload.LookbackBars > 10000 {
		return validatedStrategyDraft{}, invalidStrategy("lookbackBars must be between 1 and 10000")
	}
	schema, err := validateParameterSchema(payload.ParameterSchema)
	if err != nil {
		return validatedStrategyDraft{}, err
	}
	return validatedStrategyDraft{
		Name: name, SourceCode: payload.SourceCode,
		LookbackBars: payload.LookbackBars, ParameterSchemaJSON: string(schema),
	}, nil
}

type validatedBacktest struct {
	Request                               M
	ParametersJSON, FundingRatesJSON      string
	StartTime, EndTime                    time.Time
	AllocationUSDT, InitialEquity         decimal.Decimal
	FeeRate, SlippageRate                 decimal.Decimal
	StopLossRatio, MaintenanceMarginRatio *decimal.Decimal
}

func validateBacktestPayload(payload BacktestCreatePayload, version db.StrategyVersion, market string) (validatedBacktest, error) {
	if _, ok := marketdata.CandleIntervalDuration(marketdata.CandleInterval(payload.Interval)); !ok {
		return validatedBacktest{}, invalidStrategy("interval is not supported")
	}
	start, err := parseStrictUTC(payload.StartTime, "startTime")
	if err != nil {
		return validatedBacktest{}, err
	}
	end, err := parseStrictUTC(payload.EndTime, "endTime")
	if err != nil {
		return validatedBacktest{}, err
	}
	if !start.Before(end) {
		return validatedBacktest{}, invalidStrategy("startTime must be before endTime")
	}
	allocation, err := parseDecimalField(payload.AllocationUSDT, "allocationUsdt", false)
	if err != nil {
		return validatedBacktest{}, err
	}
	initial, err := parseDecimalField(payload.InitialEquity, "initialEquity", false)
	if err != nil {
		return validatedBacktest{}, err
	}
	fee, err := parseDecimalField(payload.FeeRate, "feeRate", false)
	if err != nil {
		return validatedBacktest{}, err
	}
	slippage, err := parseDecimalField(payload.SlippageRate, "slippageRate", false)
	if err != nil {
		return validatedBacktest{}, err
	}
	if !allocation.IsPositive() || !initial.IsPositive() || allocation.GreaterThan(initial) {
		return validatedBacktest{}, invalidStrategy("allocationUsdt and initialEquity must be positive and allocation must not exceed equity")
	}
	if fee.IsNegative() || !fee.LessThan(decimal.NewFromInt(1)) || slippage.IsNegative() || !slippage.LessThan(decimal.NewFromInt(1)) {
		return validatedBacktest{}, invalidStrategy("feeRate and slippageRate must be between zero and one")
	}
	stopLoss, err := parseOptionalRatio(payload.StopLossRatio, "stopLossRatio")
	if err != nil {
		return validatedBacktest{}, err
	}
	margin, err := parseOptionalRatio(payload.MaintenanceMarginRatio, "maintenanceMarginRatio")
	if err != nil {
		return validatedBacktest{}, err
	}
	parameters, err := validateParameters(payload.Parameters, version.ParameterSchemaJSON)
	if err != nil {
		return validatedBacktest{}, err
	}
	if market == string(marketdata.MarketTypeUSDM) && payload.FundingRates == nil {
		return validatedBacktest{}, invalidStrategy("fundingRates are required for usd_m")
	}
	if market == string(marketdata.MarketTypeSpot) && len(payload.FundingRates) > 0 {
		return validatedBacktest{}, invalidStrategy("fundingRates must be omitted for spot")
	}
	for _, value := range payload.FundingRates {
		if _, err := parseDecimalField(value, "fundingRates", true); err != nil {
			return validatedBacktest{}, err
		}
	}
	fundingRates := payload.FundingRates
	if fundingRates == nil {
		fundingRates = []string{}
	}
	fundingJSON, _ := json.Marshal(fundingRates)
	request := M{
		"strategyVersionId": version.ID.String(), "instrumentId": payload.InstrumentID,
		"interval": payload.Interval, "parameters": json.RawMessage(parameters),
		"startTime": formatUTC(start), "endTime": formatUTC(end), "allocationUsdt": allocation.String(),
		"initialEquity": initial.String(), "feeRate": fee.String(), "slippageRate": slippage.String(),
		"fundingRates": fundingRates, "stopLossRatio": payload.StopLossRatio,
		"maintenanceMarginRatio": payload.MaintenanceMarginRatio,
	}
	return validatedBacktest{
		Request: request, ParametersJSON: string(parameters), FundingRatesJSON: string(fundingJSON),
		StartTime: start, EndTime: end, AllocationUSDT: allocation, InitialEquity: initial,
		FeeRate: fee, SlippageRate: slippage, StopLossRatio: stopLoss, MaintenanceMarginRatio: margin,
	}, nil
}

func validateParameterSchema(schema map[string]json.RawMessage) ([]byte, error) {
	if schema == nil || len(schema) > 64 {
		return nil, invalidStrategy("parameterSchema must be an object with at most 64 entries")
	}
	for name, raw := range schema {
		if strings.TrimSpace(name) == "" || len(name) > 64 {
			return nil, invalidStrategy("parameter names must be between 1 and 64 bytes")
		}
		var spec map[string]json.RawMessage
		if err := json.Unmarshal(raw, &spec); err != nil || spec == nil {
			return nil, invalidStrategy("parameter schema entries must be objects")
		}
		for key := range spec {
			if key != "type" && key != "required" && key != "default" && key != "minimum" && key != "maximum" && key != "enum" {
				return nil, invalidStrategy("parameter schema contains an unsupported field")
			}
		}
		var kind string
		if err := json.Unmarshal(spec["type"], &kind); err != nil || (kind != "integer" && kind != "decimal" && kind != "boolean" && kind != "string") {
			return nil, invalidStrategy("parameter type is not supported")
		}
		if rawRequired, ok := spec["required"]; ok {
			var required bool
			if err := json.Unmarshal(rawRequired, &required); err != nil {
				return nil, invalidStrategy("parameter required must be boolean")
			}
		}
		if rawDefault, ok := spec["default"]; ok {
			if err := validateTypedParameter(rawDefault, kind); err != nil {
				return nil, err
			}
		}
		for _, field := range []string{"minimum", "maximum"} {
			if bound, ok := spec[field]; ok {
				if kind != "integer" && kind != "decimal" {
					return nil, invalidStrategy("only numeric parameters support bounds")
				}
				if _, err := decimalParameterValue(bound, kind); err != nil {
					return nil, err
				}
			}
		}
		if enumRaw, ok := spec["enum"]; ok {
			var values []json.RawMessage
			if err := json.Unmarshal(enumRaw, &values); err != nil || len(values) == 0 {
				return nil, invalidStrategy("parameter enum must be a non-empty array")
			}
			for _, value := range values {
				if err := validateTypedParameter(value, kind); err != nil {
					return nil, err
				}
			}
		}
	}
	return json.Marshal(schema)
}

func validateParameters(parameters map[string]json.RawMessage, schemaJSON string) ([]byte, error) {
	if parameters == nil || len(parameters) > 64 {
		return nil, invalidStrategy("parameters must be an object with at most 64 entries")
	}
	var schema map[string]json.RawMessage
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		return nil, invalidStrategy("published parameter schema is invalid")
	}
	for name, raw := range parameters {
		specRaw, ok := schema[name]
		if !ok {
			return nil, invalidStrategy("parameters contain an unknown name")
		}
		var spec map[string]json.RawMessage
		if err := json.Unmarshal(specRaw, &spec); err != nil {
			return nil, invalidStrategy("published parameter schema is invalid")
		}
		var kind string
		if err := json.Unmarshal(spec["type"], &kind); err != nil {
			return nil, invalidStrategy("published parameter schema is invalid")
		}
		if err := validateTypedParameter(raw, kind); err != nil {
			return nil, err
		}
	}
	for name, specRaw := range schema {
		if _, ok := parameters[name]; ok {
			continue
		}
		var spec map[string]json.RawMessage
		if err := json.Unmarshal(specRaw, &spec); err != nil {
			return nil, invalidStrategy("published parameter schema is invalid")
		}
		required := true
		if raw, ok := spec["required"]; ok {
			_ = json.Unmarshal(raw, &required)
		}
		if required {
			if _, hasDefault := spec["default"]; !hasDefault {
				return nil, invalidStrategy("required parameter is missing")
			}
		}
	}
	return json.Marshal(parameters)
}

func validateTypedParameter(raw json.RawMessage, kind string) error {
	value, err := decodeJSONScalar(raw)
	if err != nil {
		return invalidStrategy("parameter values must be JSON scalars")
	}
	switch kind {
	case "integer":
		if _, ok := value.(json.Number); !ok {
			return invalidStrategy("integer parameter has invalid type")
		}
		if _, err := decimalParameterValue(raw, kind); err != nil {
			return err
		}
	case "decimal":
		if _, ok := value.(string); !ok {
			return invalidStrategy("decimal parameters must use strings")
		}
		if _, err := decimalParameterValue(raw, kind); err != nil {
			return err
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return invalidStrategy("boolean parameter has invalid type")
		}
	case "string":
		text, ok := value.(string)
		if !ok || len(text) > 1024 {
			return invalidStrategy("string parameter has invalid type or length")
		}
	default:
		return invalidStrategy("parameter type is not supported")
	}
	return nil
}

func decimalParameterValue(raw json.RawMessage, kind string) (decimal.Decimal, error) {
	value, err := decodeJSONScalar(raw)
	if err != nil {
		return decimal.Zero, invalidStrategy("numeric parameter is invalid")
	}
	var text string
	switch item := value.(type) {
	case json.Number:
		text = item.String()
	case string:
		text = item
	default:
		return decimal.Zero, invalidStrategy("numeric parameter is invalid")
	}
	if kind == "integer" && (strings.ContainsAny(text, ".eE") || !signedDecimalPattern.MatchString(text)) {
		return decimal.Zero, invalidStrategy("integer parameter is invalid")
	}
	if kind == "decimal" && !signedDecimalPattern.MatchString(text) {
		return decimal.Zero, invalidStrategy("decimal parameter is invalid")
	}
	valueDecimal, err := decimal.NewFromString(text)
	if err != nil {
		return decimal.Zero, invalidStrategy("numeric parameter is invalid")
	}
	return valueDecimal, nil
}

func decodeJSONScalar(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("multiple JSON values")
	}
	switch value.(type) {
	case json.Number, string, bool:
		return value, nil
	default:
		return nil, errors.New("not a JSON scalar")
	}
}

func parseDecimalField(raw, field string, signed bool) (decimal.Decimal, error) {
	negative := strings.HasPrefix(raw, "-")
	if negative {
		if !signed {
			return decimal.Zero, invalidStrategy(field + " must be a decimal string")
		}
		raw = strings.TrimPrefix(raw, "-")
	}
	value, err := marketdata.ParseDecimal(raw)
	if err != nil {
		return decimal.Zero, invalidStrategy(field + " must be a decimal string")
	}
	if negative {
		value = value.Neg()
	}
	return value, nil
}

func parseOptionalRatio(raw, field string) (*decimal.Decimal, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := parseDecimalField(raw, field, false)
	if err != nil {
		return nil, err
	}
	if !value.IsPositive() || !value.LessThan(decimal.NewFromInt(1)) {
		return nil, invalidStrategy(field + " must be between zero and one")
	}
	return &value, nil
}

func parseStrictUTC(raw, field string) (time.Time, error) {
	if !strings.HasSuffix(raw, "Z") {
		return time.Time{}, invalidStrategy(field + " must be UTC RFC3339Nano")
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, invalidStrategy(field + " must be UTC RFC3339Nano")
	}
	return value.UTC(), nil
}

func requiredStrategyUUID(raw, field string) (uuid.UUID, error) {
	id, err := parseRequiredUUIDv7(raw, field)
	if err != nil {
		return uuid.Nil, invalidStrategy(err.Error())
	}
	return id, nil
}

func invalidStrategy(detail string) error {
	return fmt.Errorf("%w: %s", ErrInvalidStrategyRequest, detail)
}

func serializeStrategyDraft(row db.StrategyDraft) StrategyDraftView {
	return StrategyDraftView{
		ID: row.ID.String(), Name: row.Name, SourceCode: row.SourceCode, LookbackBars: row.LookbackBars,
		ParameterSchema: rawJSON(row.ParameterSchemaJSON, `{}`), RuntimeVersion: row.RuntimeVersion,
		CreatedAt: formatUTC(row.CreatedAt), UpdatedAt: formatUTC(row.UpdatedAt),
	}
}

func serializeStrategyVersion(row db.StrategyVersion) StrategyVersionView {
	var publishedAt *string
	if row.PublishedAt != nil {
		value := formatUTC(*row.PublishedAt)
		publishedAt = &value
	}
	return StrategyVersionView{
		ID: row.ID.String(), StrategyID: row.StrategyID.String(), VersionNumber: row.VersionNumber,
		Status: row.Status, Name: row.Name, SourceCode: row.SourceCode, CodeSHA256: row.CodeSHA256,
		RuntimeVersion: row.RuntimeVersion, LookbackBars: row.LookbackBars,
		ParameterSchema: rawJSON(row.ParameterSchemaJSON, `{}`), PublishedAt: publishedAt,
		CreatedAt: formatUTC(row.CreatedAt),
	}
}

func serializeBacktest(row db.Backtest, state backtestTaskState) BacktestView {
	view := BacktestView{
		ID: row.ID.String(), OwnerUserID: row.OwnerUserID, StrategyVersionID: row.StrategyVersionID.String(),
		InstrumentID: row.InstrumentID.String(), Market: row.Market, Symbol: row.Symbol, Interval: row.Interval,
		SimulatorVersion: row.SimulatorVersion, Status: state.Status,
		FailureCategory: state.FailureCategory,
		Parameters:      rawJSON(row.ParametersJSON, `{}`), StartTime: formatUTC(row.StartTime), EndTime: formatUTC(row.EndTime),
		AllocationUSDT: row.AllocationUSDT.String(), InitialEquity: row.InitialEquity.String(),
		FeeRate: row.FeeRate.String(), SlippageRate: row.SlippageRate.String(),
		FundingRates: rawJSON(row.FundingRatesJSON, `[]`), InputSHA256: row.InputSHA256,
		ResultSHA256: row.ResultSHA256, ManifestSHA256: row.ManifestSHA256, CreatedAt: formatUTC(row.CreatedAt),
	}
	if row.StopLossRatio != nil {
		value := row.StopLossRatio.String()
		view.StopLossRatio = &value
	}
	if row.MaintenanceMarginRatio != nil {
		value := row.MaintenanceMarginRatio.String()
		view.MaintenanceMarginRatio = &value
	}
	if row.SummaryJSON != nil {
		value := rawJSON(*row.SummaryJSON, `{}`)
		view.Summary = &value
	}
	return view
}

func rawJSON(value, fallback string) json.RawMessage {
	if !json.Valid([]byte(value)) {
		value = fallback
	}
	return json.RawMessage(value)
}

func getBacktestWithDB(database *gorm.DB, userID int64, id uuid.UUID) (BacktestView, error) {
	var row db.Backtest
	if err := database.Table("backtests AS backtest").
		Select("backtest.*, instrument.market_type AS market, instrument.native_symbol AS symbol").
		Joins("JOIN market_instruments AS instrument ON instrument.id = backtest.instrument_id").
		Where("backtest.id = ? AND backtest.owner_user_id = ?", id, userID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return BacktestView{}, ErrBacktestMissing
		}
		return BacktestView{}, err
	}
	state, err := loadBacktestTaskState(database, row.WorkerTaskID)
	if err != nil {
		return BacktestView{}, err
	}
	return serializeBacktest(row, state), nil
}

func loadBacktestBinding(database *gorm.DB, row *db.Backtest) error {
	var instrument db.MarketInstrument
	if err := database.Select("market_type", "native_symbol").Where("id = ?", row.InstrumentID).Take(&instrument).Error; err != nil {
		return err
	}
	row.Market = instrument.Market
	row.Symbol = instrument.NativeSymbol
	return nil
}

func loadBacktestTaskState(database *gorm.DB, taskID string) (backtestTaskState, error) {
	var state backtestTaskState
	err := database.Raw("SELECT status, failure_category FROM worker_tasks WHERE id = ?", taskID).Scan(&state).Error
	return state, err
}

func loadBacktestTaskStateForUpdate(database *gorm.DB, taskID string) (backtestTaskState, error) {
	var state backtestTaskState
	err := database.Raw("SELECT status, failure_category FROM worker_tasks WHERE id = ? FOR UPDATE", taskID).Scan(&state).Error
	if err == nil && state.Status == "" {
		err = gorm.ErrRecordNotFound
	}
	return state, err
}
