package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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
	ErrInvalidTradingRequest         = errors.New("invalid trading request")
	ErrTradingAccountMissing         = errors.New("trading account was not found")
	ErrTradingAccountConflict        = errors.New("trading account state does not allow this operation")
	ErrTradingReauthentication       = errors.New("valid reauthentication token is required")
	ErrTradingExecutionUnavailable   = errors.New("trading execution is not available for this strategy signal")
	ErrTradingCredentialsMissing     = errors.New("testnet trading credentials are not configured")
	ErrTradingCredentialsUnverified  = errors.New("testnet trading credentials have not been verified")
	ErrTradingReconciliationRequired = errors.New("testnet account initial reconciliation has not matched")
)

type TradingRiskPayload struct {
	InstrumentIDs      []string `json:"instrumentIds"`
	MaxTotalNotional   string   `json:"maxTotalNotional"`
	MaxSymbolNotional  string   `json:"maxSymbolNotional"`
	MaxOrderNotional   string   `json:"maxOrderNotional"`
	MaxDailyLoss       string   `json:"maxDailyLoss"`
	MaxDrawdown        string   `json:"maxDrawdown"`
	MaxQuoteAgeSeconds int      `json:"maxQuoteAgeSeconds"`
	Leverage           *int     `json:"leverage"`
}

type TradingAccountCreatePayload struct {
	Name           string             `json:"name"`
	Market         string             `json:"market"`
	Environment    string             `json:"environment"`
	InitialBalance string             `json:"initialBalance"`
	PaperFeeRate   string             `json:"paperFeeRate"`
	Risk           TradingRiskPayload `json:"risk"`
}

type TradingRiskView struct {
	InstrumentIDs      []string `json:"instrumentIds"`
	MaxTotalNotional   *string  `json:"maxTotalNotional"`
	MaxSymbolNotional  *string  `json:"maxSymbolNotional"`
	MaxOrderNotional   *string  `json:"maxOrderNotional"`
	MaxDailyLoss       *string  `json:"maxDailyLoss"`
	MaxDrawdown        *string  `json:"maxDrawdown"`
	MaxQuoteAgeSeconds *int     `json:"maxQuoteAgeSeconds"`
	Leverage           *int     `json:"leverage"`
	Complete           bool     `json:"complete"`
}

type TradingAccountView struct {
	ID                     string                    `json:"id"`
	Name                   string                    `json:"name"`
	Market                 string                    `json:"market"`
	Environment            string                    `json:"environment"`
	Status                 string                    `json:"status"`
	PauseReason            string                    `json:"pauseReason"`
	AutomationEnabled      bool                      `json:"automationEnabled"`
	AutomationAuthorized   bool                      `json:"automationAuthorized"`
	AutomationAuthorizedAt *string                   `json:"automationAuthorizedAt"`
	CredentialsConfigured  bool                      `json:"credentialsConfigured"`
	CredentialStatus       string                    `json:"credentialStatus"`
	CredentialVerification string                    `json:"credentialVerificationStatus"`
	CredentialsUpdatedAt   *string                   `json:"credentialsUpdatedAt"`
	Reconciliation         TestnetReconciliationView `json:"reconciliation"`
	InitialBalance         string                    `json:"initialBalance"`
	PaperFeeRate           string                    `json:"paperFeeRate"`
	Risk                   TradingRiskView           `json:"risk"`
	CreatedAt              string                    `json:"createdAt"`
	UpdatedAt              string                    `json:"updatedAt"`
}

type TestnetReconciliationView struct {
	Status          string  `json:"status"`
	ErrorCode       string  `json:"errorCode,omitempty"`
	BalanceCount    int     `json:"balanceCount"`
	PositionCount   int     `json:"positionCount"`
	OpenOrderCount  int     `json:"openOrderCount"`
	LastAttemptedAt *string `json:"lastAttemptedAt,omitempty"`
	LastObservedAt  *string `json:"lastObservedAt,omitempty"`
}

type TestnetBalanceView struct {
	AccountID        string `json:"accountId"`
	Asset            string `json:"asset"`
	TotalBalance     string `json:"totalBalance"`
	AvailableBalance string `json:"availableBalance"`
	ObservedAt       string `json:"observedAt"`
}

type TestnetPositionView struct {
	AccountID     string `json:"accountId"`
	NativeSymbol  string `json:"symbol"`
	PositionSide  string `json:"positionSide"`
	Quantity      string `json:"quantity"`
	EntryPrice    string `json:"entryPrice"`
	UnrealizedPnl string `json:"unrealizedPnl"`
	ObservedAt    string `json:"observedAt"`
}

type TestnetOpenOrderView struct {
	AccountID        string `json:"accountId"`
	NativeSymbol     string `json:"symbol"`
	ExchangeOrderID  string `json:"exchangeOrderId"`
	ClientOrderID    string `json:"clientOrderId"`
	Side             string `json:"side"`
	OrderType        string `json:"orderType"`
	Status           string `json:"status"`
	Price            string `json:"price"`
	OriginalQuantity string `json:"originalQuantity"`
	ExecutedQuantity string `json:"executedQuantity"`
	StopPrice        string `json:"stopPrice"`
	ObservedAt       string `json:"observedAt"`
}

type TestnetOrderView struct {
	ID                      string  `json:"id"`
	AccountID               string  `json:"accountId"`
	IntentID                string  `json:"intentId"`
	InstrumentID            string  `json:"instrumentId"`
	Symbol                  string  `json:"symbol"`
	ClientOrderID           string  `json:"clientOrderId"`
	ExchangeOrderID         *string `json:"exchangeOrderId"`
	Side                    string  `json:"side"`
	Quantity                string  `json:"quantity"`
	FilledQuantity          string  `json:"filledQuantity"`
	CumulativeQuoteQuantity string  `json:"cumulativeQuoteQuantity"`
	AveragePrice            string  `json:"averagePrice"`
	Purpose                 string  `json:"purpose"`
	OrderType               string  `json:"orderType"`
	StopPrice               string  `json:"stopPrice"`
	ClosePosition           bool    `json:"closePosition"`
	ReduceOnly              bool    `json:"reduceOnly"`
	WorkingType             string  `json:"workingType"`
	ReplacesOrderID         *string `json:"replacesOrderId"`
	Status                  string  `json:"status"`
	LastErrorCode           string  `json:"lastErrorCode"`
	SubmitAttemptCount      int     `json:"submitAttemptCount"`
	QueryAttemptCount       int     `json:"queryAttemptCount"`
	SubmittedAt             string  `json:"submittedAt"`
	LastQueriedAt           *string `json:"lastQueriedAt"`
	ObservedAt              *string `json:"observedAt"`
	CreatedAt               string  `json:"createdAt"`
	UpdatedAt               string  `json:"updatedAt"`
}

// TradingCredentialPayload 只在写入边界接收明文；响应永远不包含这两个字段。
type TradingCredentialPayload struct {
	APIKey                string `json:"apiKey"`
	APISecret             string `json:"apiSecret"`
	WithdrawalDisabled    bool   `json:"withdrawalDisabled"`
	IPWhitelistConfigured bool   `json:"ipWhitelistConfigured"`
}

// TradingCredentialView 是凭据的非敏感状态投影。
type TradingCredentialView struct {
	AccountID          string  `json:"accountId"`
	Configured         bool    `json:"configured"`
	Status             string  `json:"status"`
	VerificationStatus string  `json:"verificationStatus"`
	VerificationError  string  `json:"verificationErrorCode,omitempty"`
	UpdatedAt          string  `json:"updatedAt"`
	LastVerifiedAt     *string `json:"lastVerifiedAt,omitempty"`
}

type TradingControlView struct {
	EmergencyStopped bool    `json:"emergencyStopped"`
	StopReason       string  `json:"stopReason"`
	StoppedAt        string  `json:"stoppedAt"`
	StoppedByUserID  *int64  `json:"stoppedByUserId"`
	ReleasedAt       *string `json:"releasedAt"`
	ReleasedByUserID *int64  `json:"releasedByUserId"`
	UpdatedAt        string  `json:"updatedAt"`
}

type TradingIntentView struct {
	ID                 string  `json:"id"`
	AccountID          string  `json:"accountId"`
	StrategySignalID   string  `json:"strategySignalId"`
	StrategyInstanceID string  `json:"strategyInstanceId"`
	InstrumentID       string  `json:"instrumentId"`
	Symbol             string  `json:"symbol"`
	Market             string  `json:"market"`
	Mode               string  `json:"mode"`
	Target             string  `json:"target"`
	Status             string  `json:"status"`
	BlockReason        string  `json:"blockReason"`
	ClientOrderID      string  `json:"clientOrderId"`
	CompletedAt        *string `json:"completedAt"`
	CreatedAt          string  `json:"createdAt"`
}

type PaperOrderView struct {
	ID             string `json:"id"`
	AccountID      string `json:"accountId"`
	IntentID       string `json:"intentId"`
	InstrumentID   string `json:"instrumentId"`
	Symbol         string `json:"symbol"`
	ClientOrderID  string `json:"clientOrderId"`
	Side           string `json:"side"`
	Quantity       string `json:"quantity"`
	FilledQuantity string `json:"filledQuantity"`
	AveragePrice   string `json:"averagePrice"`
	Status         string `json:"status"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

type PaperPositionView struct {
	AccountID               string  `json:"accountId"`
	InstrumentID            string  `json:"instrumentId"`
	Symbol                  string  `json:"symbol"`
	OwnerStrategyInstanceID *string `json:"ownerStrategyInstanceId"`
	Quantity                string  `json:"quantity"`
	AverageEntryPrice       string  `json:"averageEntryPrice"`
	LastPrice               string  `json:"lastPrice"`
	RealizedPnl             string  `json:"realizedPnl"`
	UnrealizedPnl           string  `json:"unrealizedPnl"`
	UpdatedAt               string  `json:"updatedAt"`
}

type PaperBalanceView struct {
	AccountID      string `json:"accountId"`
	CashBalance    string `json:"cashBalance"`
	Equity         string `json:"equity"`
	PeakEquity     string `json:"peakEquity"`
	DayStartDate   string `json:"dayStartDate"`
	DayStartEquity string `json:"dayStartEquity"`
	RealizedPnl    string `json:"realizedPnl"`
	UnrealizedPnl  string `json:"unrealizedPnl"`
	Fees           string `json:"fees"`
	Funding        string `json:"funding"`
	UpdatedAt      string `json:"updatedAt"`
}

type TradingOverviewView struct {
	Control           TradingControlView     `json:"control"`
	Accounts          []TradingAccountView   `json:"accounts"`
	Intents           []TradingIntentView    `json:"intents"`
	Orders            []PaperOrderView       `json:"orders"`
	Positions         []PaperPositionView    `json:"positions"`
	Balances          []PaperBalanceView     `json:"balances"`
	TestnetBalances   []TestnetBalanceView   `json:"testnetBalances"`
	TestnetPositions  []TestnetPositionView  `json:"testnetPositions"`
	TestnetOpenOrders []TestnetOpenOrderView `json:"testnetOpenOrders"`
	TestnetOrders     []TestnetOrderView     `json:"testnetOrders"`
}

type validatedTradingRisk struct {
	InstrumentIDs      []uuid.UUID
	MaxTotalNotional   *decimal.Decimal
	MaxSymbolNotional  *decimal.Decimal
	MaxOrderNotional   *decimal.Decimal
	MaxDailyLoss       *decimal.Decimal
	MaxDrawdown        *decimal.Decimal
	MaxQuoteAgeSeconds *int
	Leverage           *int
}

func (a *App) CreateTradingAccount(
	ctx context.Context, userID int64, payload TradingAccountCreatePayload, idempotencyKey string,
) (TradingAccountView, error) {
	if userID <= 0 {
		return TradingAccountView{}, invalidTrading("owner is required")
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" || len(name) > 120 {
		return TradingAccountView{}, invalidTrading("name must be between 1 and 120 bytes")
	}
	market := strings.TrimSpace(payload.Market)
	if market != string(marketdata.MarketTypeSpot) && market != string(marketdata.MarketTypeUSDM) {
		return TradingAccountView{}, invalidTrading("market must be spot or usd_m")
	}
	environment := strings.TrimSpace(payload.Environment)
	if environment == "" {
		environment = "paper"
	}
	if environment != "paper" && environment != "testnet" {
		return TradingAccountView{}, invalidTrading("environment must be paper or testnet")
	}
	initial, err := parseTradingDecimal(payload.InitialBalance, "initialBalance", false)
	if err != nil || !initial.IsPositive() {
		return TradingAccountView{}, invalidTrading("initialBalance must be a positive decimal string")
	}
	feeRate, err := parseTradingDecimal(payload.PaperFeeRate, "paperFeeRate", false)
	if err != nil || feeRate.IsNegative() || feeRate.GreaterThan(decimal.RequireFromString("0.01")) {
		return TradingAccountView{}, invalidTrading("paperFeeRate must be between 0 and 0.01")
	}
	risk, err := validateTradingRisk(payload.Risk, market)
	if err != nil {
		return TradingAccountView{}, err
	}
	requestHash, err := canonicalRequestHash(payload)
	if err != nil {
		return TradingAccountView{}, err
	}

	var row db.TradingAccount
	err = a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, reused, err := a.reserveIdempotencyRecord(tx, userID, "trading-account:create", idempotencyKey, requestHash)
		if err != nil {
			return err
		}
		if reused {
			if err := tx.Where("creation_idempotency_record_id = ? AND owner_user_id = ?", record.ID, userID).Take(&row).Error; err != nil {
				return err
			}
			return nil
		}
		if err := validateTradingInstruments(tx, risk.InstrumentIDs, market); err != nil {
			return err
		}
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		pauseReason := "configuration_required"
		if environment == "testnet" {
			pauseReason = "credentials_required"
		}
		row = db.TradingAccount{
			ID: id, OwnerUserID: userID, Name: name, Market: market, Environment: environment,
			Status: "paused", PauseReason: pauseReason, InitialBalance: &initial,
			PaperFeeRate: &feeRate, MaxTotalNotional: risk.MaxTotalNotional,
			MaxSymbolNotional: risk.MaxSymbolNotional, MaxOrderNotional: risk.MaxOrderNotional,
			MaxDailyLoss: risk.MaxDailyLoss, MaxDrawdown: risk.MaxDrawdown,
			MaxQuoteAgeSeconds: risk.MaxQuoteAgeSeconds, Leverage: risk.Leverage,
			CreationIdempotencyRecordID: &record.ID, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if err := replaceTradingInstrumentWhitelist(tx, row.ID, risk.InstrumentIDs, now); err != nil {
			return err
		}
		if environment == "paper" {
			balance := db.PaperBalance{
				AccountID: row.ID, CashBalance: initial, Equity: initial, PeakEquity: initial,
				DayStartDate: utcDay(now), DayStartEquity: initial, UpdatedAt: now,
			}
			return tx.Create(&balance).Error
		}
		return nil
	})
	if err != nil {
		return TradingAccountView{}, err
	}
	return a.loadTradingAccountView(a.dbWithContext(ctx), row)
}

func (a *App) UpdateTradingRisk(
	ctx context.Context, principal *Principal, rawID string, payload TradingRiskPayload,
	idempotencyKey, reauthToken string,
) (TradingAccountView, error) {
	accountID, err := requiredTradingUUID(rawID, "accountId")
	if err != nil {
		return TradingAccountView{}, err
	}
	if principal == nil || principal.User == nil {
		return TradingAccountView{}, invalidTrading("owner is required")
	}
	requestHash, err := canonicalRequestHash(payload)
	if err != nil {
		return TradingAccountView{}, err
	}
	var row db.TradingAccount
	err = a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_user_id = ?", accountID, principal.User.ID).Take(&row).Error; err != nil {
			return tradingAccountLookupError(err)
		}
		_, reused, err := a.reserveIdempotencyRecord(
			tx, principal.User.ID, "trading-account:risk:"+accountID.String(), idempotencyKey, requestHash,
		)
		if err != nil || reused {
			return err
		}
		risk, err := validateTradingRisk(payload, row.Market)
		if err != nil {
			return err
		}
		if err := validateTradingInstruments(tx, risk.InstrumentIDs, row.Market); err != nil {
			return err
		}
		if !a.ConsumeReauthToken(reauthToken, principal) {
			return ErrTradingReauthentication
		}
		now := time.Now().UTC()
		updates := map[string]any{
			"max_total_notional": risk.MaxTotalNotional, "max_symbol_notional": risk.MaxSymbolNotional,
			"max_order_notional": risk.MaxOrderNotional, "max_daily_loss": risk.MaxDailyLoss,
			"max_drawdown": risk.MaxDrawdown, "max_quote_age_seconds": risk.MaxQuoteAgeSeconds,
			"leverage": risk.Leverage, "status": "paused", "pause_reason": "risk_configuration_changed",
			"automation_enabled": false, "updated_at": now,
		}
		if err := tx.Model(&row).Updates(updates).Error; err != nil {
			return err
		}
		if err := replaceTradingInstrumentWhitelist(tx, row.ID, risk.InstrumentIDs, now); err != nil {
			return err
		}
		if row.Environment == "testnet" {
			if err := clearTestnetReconciliation(tx, row.ID); err != nil {
				return err
			}
		}
		if err := disableAutoInstances(tx, &row.ID, now); err != nil {
			return err
		}
		return tx.Where("id = ?", row.ID).Take(&row).Error
	})
	if err != nil {
		return TradingAccountView{}, err
	}
	return a.loadTradingAccountView(a.dbWithContext(ctx), row)
}

func (a *App) SetTradingAutomation(
	ctx context.Context, principal *Principal, rawID string, enabled bool,
	idempotencyKey, reauthToken string,
) (TradingAccountView, error) {
	accountID, err := requiredTradingUUID(rawID, "accountId")
	if err != nil {
		return TradingAccountView{}, err
	}
	if principal == nil || principal.User == nil {
		return TradingAccountView{}, invalidTrading("owner is required")
	}
	requestHash, _ := canonicalRequestHash(M{"enabled": enabled})
	var row db.TradingAccount
	err = a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_user_id = ?", accountID, principal.User.ID).Take(&row).Error; err != nil {
			return tradingAccountLookupError(err)
		}
		_, reused, err := a.reserveIdempotencyRecord(
			tx, principal.User.ID, "trading-account:automation:"+accountID.String(), idempotencyKey, requestHash,
		)
		if err != nil || reused {
			return err
		}
		now := time.Now().UTC()
		if enabled {
			if !a.ConsumeReauthToken(reauthToken, principal) {
				return ErrTradingReauthentication
			}
			if row.Environment == "testnet" {
				if err := testnetAccountReadinessError(tx, row.ID); err != nil {
					return err
				}
			}
			complete, err := tradingRiskComplete(tx, row)
			if err != nil {
				return err
			}
			control, err := loadTradingControl(tx, clause.Locking{Strength: "SHARE"})
			if err != nil {
				return err
			}
			if !complete || row.Status != "active" || row.AutomationAuthorizedAt == nil || control.EmergencyStopped {
				return ErrTradingAccountConflict
			}
		} else if err := disableAutoInstances(tx, &row.ID, now); err != nil {
			return err
		}
		if err := tx.Model(&row).Updates(map[string]any{"automation_enabled": enabled, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", row.ID).Take(&row).Error
	})
	if err != nil {
		return TradingAccountView{}, err
	}
	return a.loadTradingAccountView(a.dbWithContext(ctx), row)
}

func (a *App) ResumeTradingAccount(
	ctx context.Context, principal *Principal, rawID, idempotencyKey, reauthToken string,
) (TradingAccountView, error) {
	accountID, err := requiredTradingUUID(rawID, "accountId")
	if err != nil {
		return TradingAccountView{}, err
	}
	if principal == nil || principal.User == nil {
		return TradingAccountView{}, invalidTrading("owner is required")
	}
	requestHash, _ := canonicalRequestHash(M{"status": "active"})
	var row db.TradingAccount
	err = a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_user_id = ?", accountID, principal.User.ID).Take(&row).Error; err != nil {
			return tradingAccountLookupError(err)
		}
		_, reused, err := a.reserveIdempotencyRecord(
			tx, principal.User.ID, "trading-account:resume:"+accountID.String(), idempotencyKey, requestHash,
		)
		if err != nil || reused {
			return err
		}
		if !a.ConsumeReauthToken(reauthToken, principal) {
			return ErrTradingReauthentication
		}
		complete, err := tradingRiskComplete(tx, row)
		if err != nil {
			return err
		}
		control, err := loadTradingControl(tx, clause.Locking{Strength: "SHARE"})
		if err != nil {
			return err
		}
		if !complete || control.EmergencyStopped {
			return ErrTradingAccountConflict
		}
		if row.Environment == "paper" {
			var balance db.PaperBalance
			if err := tx.Where("account_id = ?", row.ID).Take(&balance).Error; err != nil {
				return err
			}
			if currentRiskBreached(row, balance) {
				return ErrTradingAccountConflict
			}
		} else {
			if err := testnetAccountReadinessError(tx, row.ID); err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		if err := tx.Model(&row).Updates(map[string]any{
			"status": "active", "pause_reason": "", "automation_enabled": false, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", row.ID).Take(&row).Error
	})
	if err != nil {
		return TradingAccountView{}, err
	}
	return a.loadTradingAccountView(a.dbWithContext(ctx), row)
}

func (a *App) SetTradingAuthorization(
	ctx context.Context, principal *Principal, rawID string, authorized bool,
	idempotencyKey, reauthToken string,
) (TradingAccountView, error) {
	if principal == nil || principal.User == nil || !principal.HasRole("R_SUPER") {
		return TradingAccountView{}, ErrPermission
	}
	accountID, err := requiredTradingUUID(rawID, "accountId")
	if err != nil {
		return TradingAccountView{}, err
	}
	requestHash, _ := canonicalRequestHash(M{"authorized": authorized})
	var row db.TradingAccount
	err = a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", accountID).Take(&row).Error; err != nil {
			return tradingAccountLookupError(err)
		}
		_, reused, err := a.reserveIdempotencyRecord(
			tx, principal.User.ID, "trading-account:authorize:"+accountID.String(), idempotencyKey, requestHash,
		)
		if err != nil || reused {
			return err
		}
		if !a.ConsumeReauthToken(reauthToken, principal) {
			return ErrTradingReauthentication
		}
		now := time.Now().UTC()
		updates := map[string]any{"updated_at": now}
		if authorized {
			updates["automation_authorized_at"] = now
			updates["automation_authorized_by_user_id"] = principal.User.ID
		} else {
			updates["automation_authorized_at"] = nil
			updates["automation_authorized_by_user_id"] = nil
			updates["automation_enabled"] = false
			if err := disableAutoInstances(tx, &row.ID, now); err != nil {
				return err
			}
		}
		if err := tx.Model(&row).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", row.ID).Take(&row).Error
	})
	if err != nil {
		return TradingAccountView{}, err
	}
	return a.loadTradingAccountView(a.dbWithContext(ctx), row)
}

func (a *App) ActivateTradingEmergencyStop(
	ctx context.Context, principal *Principal, reason, idempotencyKey string,
) (TradingControlView, error) {
	if principal == nil || principal.User == nil {
		return TradingControlView{}, invalidTrading("owner is required")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 255 {
		return TradingControlView{}, invalidTrading("reason must be between 1 and 255 bytes")
	}
	requestHash, _ := canonicalRequestHash(M{"reason": reason})
	var control db.TradingControl
	err := a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, reused, err := a.reserveIdempotencyRecord(
			tx, principal.User.ID, "trading-control:emergency-stop", idempotencyKey, requestHash,
		)
		if err != nil || reused {
			if err != nil {
				return err
			}
			control, err = loadTradingControl(tx, clause.Locking{})
			return err
		}
		control, err = loadTradingControl(tx, clause.Locking{Strength: "UPDATE"})
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := tx.Model(&control).Updates(map[string]any{
			"emergency_stopped": true, "stop_reason": reason, "stopped_at": now,
			"stopped_by_user_id": principal.User.ID, "released_at": nil,
			"released_by_user_id": nil, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&db.TradingAccount{}).Where("status <> ? OR automation_enabled", "paused").Updates(map[string]any{
			"status": "paused", "pause_reason": "global_emergency_stop",
			"automation_enabled": false, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		if err := disableAutoInstances(tx, nil, now); err != nil {
			return err
		}
		return tx.Where("id = 1").Take(&control).Error
	})
	if err != nil {
		return TradingControlView{}, err
	}
	return serializeTradingControl(control), nil
}

func (a *App) ReleaseTradingEmergencyStop(
	ctx context.Context, principal *Principal, idempotencyKey, reauthToken string,
) (TradingControlView, error) {
	if principal == nil || principal.User == nil || !principal.HasRole("R_SUPER") {
		return TradingControlView{}, ErrPermission
	}
	requestHash, _ := canonicalRequestHash(M{"emergencyStopped": false})
	var control db.TradingControl
	err := a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, reused, err := a.reserveIdempotencyRecord(
			tx, principal.User.ID, "trading-control:release", idempotencyKey, requestHash,
		)
		if err != nil || reused {
			if err != nil {
				return err
			}
			control, err = loadTradingControl(tx, clause.Locking{})
			return err
		}
		if !a.ConsumeReauthToken(reauthToken, principal) {
			return ErrTradingReauthentication
		}
		control, err = loadTradingControl(tx, clause.Locking{Strength: "UPDATE"})
		if err != nil {
			return err
		}
		if !control.EmergencyStopped {
			return nil
		}
		now := time.Now().UTC()
		if err := tx.Model(&control).Updates(map[string]any{
			"emergency_stopped": false, "stop_reason": "", "released_at": now,
			"released_by_user_id": principal.User.ID, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		return tx.Where("id = 1").Take(&control).Error
	})
	if err != nil {
		return TradingControlView{}, err
	}
	return serializeTradingControl(control), nil
}

func (a *App) GetTradingOverview(ctx context.Context, userID int64) (TradingOverviewView, error) {
	if userID <= 0 {
		return TradingOverviewView{}, invalidTrading("owner is required")
	}
	database := a.dbWithContext(ctx)
	control, err := loadTradingControl(database, clause.Locking{})
	if err != nil {
		return TradingOverviewView{}, err
	}
	result := TradingOverviewView{
		Control: serializeTradingControl(control), Accounts: []TradingAccountView{}, Intents: []TradingIntentView{},
		Orders: []PaperOrderView{}, Positions: []PaperPositionView{}, Balances: []PaperBalanceView{},
		TestnetBalances: []TestnetBalanceView{}, TestnetPositions: []TestnetPositionView{},
		TestnetOpenOrders: []TestnetOpenOrderView{}, TestnetOrders: []TestnetOrderView{},
	}
	var accounts []db.TradingAccount
	if err := database.Where("owner_user_id = ?", userID).Order("id DESC").Find(&accounts).Error; err != nil {
		return TradingOverviewView{}, err
	}
	for _, account := range accounts {
		view, err := a.loadTradingAccountView(database, account)
		if err != nil {
			return TradingOverviewView{}, err
		}
		result.Accounts = append(result.Accounts, view)
	}
	var intents []db.TradingIntent
	if err := database.Where("owner_user_id = ?", userID).Order("id DESC").Limit(50).Find(&intents).Error; err != nil {
		return TradingOverviewView{}, err
	}
	var orders []db.PaperOrder
	if err := database.Joins("JOIN trading_accounts ON trading_accounts.id = paper_orders.account_id").
		Where("trading_accounts.owner_user_id = ?", userID).Order("paper_orders.id DESC").Limit(50).Find(&orders).Error; err != nil {
		return TradingOverviewView{}, err
	}
	var positions []db.PaperPosition
	if err := database.Joins("JOIN trading_accounts ON trading_accounts.id = paper_positions.account_id").
		Where("trading_accounts.owner_user_id = ?", userID).Order("paper_positions.account_id, paper_positions.instrument_id").Find(&positions).Error; err != nil {
		return TradingOverviewView{}, err
	}
	var balances []db.PaperBalance
	if err := database.Joins("JOIN trading_accounts ON trading_accounts.id = paper_balances.account_id").
		Where("trading_accounts.owner_user_id = ?", userID).Order("paper_balances.account_id").Find(&balances).Error; err != nil {
		return TradingOverviewView{}, err
	}
	var testnetBalances []db.TestnetBalance
	if err := database.Joins("JOIN trading_accounts ON trading_accounts.id = testnet_balances.account_id").
		Where("trading_accounts.owner_user_id = ?", userID).
		Order("testnet_balances.account_id, testnet_balances.asset").Find(&testnetBalances).Error; err != nil {
		return TradingOverviewView{}, err
	}
	var testnetPositions []db.TestnetPosition
	if err := database.Joins("JOIN trading_accounts ON trading_accounts.id = testnet_positions.account_id").
		Where("trading_accounts.owner_user_id = ?", userID).
		Order("testnet_positions.account_id, testnet_positions.native_symbol, testnet_positions.position_side").Find(&testnetPositions).Error; err != nil {
		return TradingOverviewView{}, err
	}
	var testnetOrders []db.TestnetOpenOrder
	if err := database.Joins("JOIN trading_accounts ON trading_accounts.id = testnet_open_orders.account_id").
		Where("trading_accounts.owner_user_id = ?", userID).
		Order("testnet_open_orders.account_id, testnet_open_orders.native_symbol, testnet_open_orders.exchange_order_id").Find(&testnetOrders).Error; err != nil {
		return TradingOverviewView{}, err
	}
	var managedTestnetOrders []db.TestnetOrder
	if err := database.Joins("JOIN trading_accounts ON trading_accounts.id = testnet_orders.account_id").
		Where("trading_accounts.owner_user_id = ?", userID).
		Order("testnet_orders.created_at DESC, testnet_orders.id DESC").Limit(50).
		Find(&managedTestnetOrders).Error; err != nil {
		return TradingOverviewView{}, err
	}
	symbols, err := loadTradingSymbols(database, intents, orders, positions, managedTestnetOrders)
	if err != nil {
		return TradingOverviewView{}, err
	}
	for _, row := range intents {
		result.Intents = append(result.Intents, serializeTradingIntent(row, symbols[row.InstrumentID]))
	}
	for _, row := range orders {
		result.Orders = append(result.Orders, serializePaperOrder(row, symbols[row.InstrumentID]))
	}
	for _, row := range positions {
		result.Positions = append(result.Positions, serializePaperPosition(row, symbols[row.InstrumentID]))
	}
	for _, row := range balances {
		result.Balances = append(result.Balances, serializePaperBalance(row))
	}
	for _, row := range testnetBalances {
		result.TestnetBalances = append(result.TestnetBalances, serializeTestnetBalance(row))
	}
	for _, row := range testnetPositions {
		result.TestnetPositions = append(result.TestnetPositions, serializeTestnetPosition(row))
	}
	for _, row := range testnetOrders {
		result.TestnetOpenOrders = append(result.TestnetOpenOrders, serializeTestnetOpenOrder(row))
	}
	for _, row := range managedTestnetOrders {
		result.TestnetOrders = append(result.TestnetOrders, serializeTestnetOrder(row, symbols[row.InstrumentID]))
	}
	return result, nil
}

func (a *App) createTradingIntentForSignalWithDB(database *gorm.DB, signal db.StrategySignal, strict bool) error {
	if signal.Mode != "manual" && signal.Mode != "auto" {
		return nil
	}
	if signal.Environment != "paper" && signal.Environment != "testnet" {
		if strict {
			return ErrTradingExecutionUnavailable
		}
		return nil
	}
	var instance db.StrategyInstance
	if err := database.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", signal.StrategyInstanceID).Take(&instance).Error; err != nil {
		return err
	}
	if instance.OwnerUserID != signal.OwnerUserID || instance.TradingAccountID == nil || instance.AllocationUSDT == nil {
		if strict {
			return ErrTradingExecutionUnavailable
		}
		return nil
	}
	if signal.Mode == "manual" && signal.Status != "approved" {
		return nil
	}
	if signal.Mode == "auto" && (signal.Status != "active" || !instance.IsEnabled) {
		return nil
	}
	var account db.TradingAccount
	if err := database.Where("id = ? AND owner_user_id = ? AND environment = ?", *instance.TradingAccountID, signal.OwnerUserID, signal.Environment).
		Take(&account).Error; err != nil {
		if strict || !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return nil
	}
	var instrument db.MarketInstrument
	if err := database.Select("id", "market_type").Where("id = ?", signal.InstrumentID).Take(&instrument).Error; err != nil {
		return err
	}
	if instrument.Market != account.Market {
		if strict {
			return ErrTradingExecutionUnavailable
		}
		return nil
	}
	intentID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	clientOrderID := tradingClientOrderID(intentID)
	now := time.Now().UTC()
	intent := db.TradingIntent{
		ID: intentID, AccountID: account.ID, StrategySignalID: signal.ID,
		StrategyInstanceID: signal.StrategyInstanceID, OwnerUserID: signal.OwnerUserID,
		InstrumentID: signal.InstrumentID, Market: account.Market, Mode: signal.Mode,
		Environment: signal.Environment, Target: signal.Target, Status: "pending",
		ClientOrderID: clientOrderID, CreatedAt: now, UpdatedAt: now,
	}
	return database.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "strategy_signal_id"}}, DoNothing: true,
	}).Create(&intent).Error
}

func (a *App) prepareAutoTradingIntent(ctx context.Context, event *domainEvent) error {
	if event == nil || event.EventType != "strategy.signal.created" {
		return nil
	}
	signalID, err := requiredTradingUUID(event.AggregateID, "signalId")
	if err != nil {
		return nil
	}
	return a.dbWithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var signal db.StrategySignal
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id = ?", signalID).Take(&signal).Error; err != nil {
			return err
		}
		if signal.Mode != "auto" {
			return nil
		}
		return a.createTradingIntentForSignalWithDB(tx, signal, false)
	})
}

func validateTradingRisk(payload TradingRiskPayload, market string) (validatedTradingRisk, error) {
	if len(payload.InstrumentIDs) > 100 {
		return validatedTradingRisk{}, invalidTrading("instrumentIds must contain at most 100 entries")
	}
	seen := map[uuid.UUID]bool{}
	instrumentIDs := make([]uuid.UUID, 0, len(payload.InstrumentIDs))
	for _, raw := range payload.InstrumentIDs {
		id, err := requiredTradingUUID(raw, "instrumentIds")
		if err != nil {
			return validatedTradingRisk{}, err
		}
		if !seen[id] {
			seen[id] = true
			instrumentIDs = append(instrumentIDs, id)
		}
	}
	maxTotal, err := parseOptionalTradingLimit(payload.MaxTotalNotional, "maxTotalNotional")
	if err != nil {
		return validatedTradingRisk{}, err
	}
	maxSymbol, err := parseOptionalTradingLimit(payload.MaxSymbolNotional, "maxSymbolNotional")
	if err != nil {
		return validatedTradingRisk{}, err
	}
	maxOrder, err := parseOptionalTradingLimit(payload.MaxOrderNotional, "maxOrderNotional")
	if err != nil {
		return validatedTradingRisk{}, err
	}
	maxDailyLoss, err := parseOptionalTradingLimit(payload.MaxDailyLoss, "maxDailyLoss")
	if err != nil {
		return validatedTradingRisk{}, err
	}
	maxDrawdown, err := parseOptionalTradingLimit(payload.MaxDrawdown, "maxDrawdown")
	if err != nil {
		return validatedTradingRisk{}, err
	}
	var maxQuoteAge *int
	if payload.MaxQuoteAgeSeconds != 0 {
		if payload.MaxQuoteAgeSeconds < 1 || payload.MaxQuoteAgeSeconds > 300 {
			return validatedTradingRisk{}, invalidTrading("maxQuoteAgeSeconds must be between 1 and 300")
		}
		value := payload.MaxQuoteAgeSeconds
		maxQuoteAge = &value
	}
	if market == string(marketdata.MarketTypeSpot) && payload.Leverage != nil {
		return validatedTradingRisk{}, invalidTrading("leverage must be omitted for spot")
	}
	if payload.Leverage != nil && (*payload.Leverage < 1 || *payload.Leverage > 5) {
		return validatedTradingRisk{}, invalidTrading("leverage must be between 1 and 5")
	}
	return validatedTradingRisk{
		InstrumentIDs: instrumentIDs, MaxTotalNotional: maxTotal, MaxSymbolNotional: maxSymbol,
		MaxOrderNotional: maxOrder, MaxDailyLoss: maxDailyLoss, MaxDrawdown: maxDrawdown,
		MaxQuoteAgeSeconds: maxQuoteAge, Leverage: payload.Leverage,
	}, nil
}

func validateTradingInstruments(database *gorm.DB, ids []uuid.UUID, market string) error {
	if len(ids) == 0 {
		return nil
	}
	var count int64
	if err := database.Model(&db.MarketInstrument{}).Where(
		"id IN ? AND venue = ? AND market_type = ? AND status = ?", ids, marketdata.VenueBinance, market, "trading",
	).Count(&count).Error; err != nil {
		return err
	}
	if count != int64(len(ids)) {
		return invalidTrading("instrumentIds must contain trading Binance instruments from the account market")
	}
	return nil
}

func replaceTradingInstrumentWhitelist(database *gorm.DB, accountID uuid.UUID, ids []uuid.UUID, now time.Time) error {
	if err := database.Where("account_id = ?", accountID).Delete(&db.TradingAccountInstrument{}).Error; err != nil {
		return err
	}
	for _, id := range ids {
		if err := database.Create(&db.TradingAccountInstrument{AccountID: accountID, InstrumentID: id, CreatedAt: now}).Error; err != nil {
			return err
		}
	}
	return nil
}

func tradingRiskComplete(database *gorm.DB, account db.TradingAccount) (bool, error) {
	var whitelistCount int64
	if err := database.Model(&db.TradingAccountInstrument{}).Where("account_id = ?", account.ID).Count(&whitelistCount).Error; err != nil {
		return false, err
	}
	complete := whitelistCount > 0 && account.MaxTotalNotional != nil && account.MaxSymbolNotional != nil &&
		account.MaxOrderNotional != nil && account.MaxDailyLoss != nil && account.MaxDrawdown != nil &&
		account.MaxQuoteAgeSeconds != nil
	if account.Market == string(marketdata.MarketTypeUSDM) {
		complete = complete && account.Leverage != nil
	}
	return complete, nil
}

func (a *App) validateStrategyInstanceExecutionReady(database *gorm.DB, instance db.StrategyInstance) error {
	if instance.Mode == "signal_only" {
		return nil
	}
	if (instance.Environment != "paper" && instance.Environment != "testnet") ||
		instance.TradingAccountID == nil || instance.AllocationUSDT == nil {
		return ErrTradingExecutionUnavailable
	}
	var version db.StrategyVersion
	if err := database.Select("id", "market_type", "instrument_id").Where("id = ?", instance.StrategyVersionID).Take(&version).Error; err != nil {
		return err
	}
	var account db.TradingAccount
	if err := database.Where(
		"id = ? AND owner_user_id = ? AND market_type = ? AND environment = ?",
		*instance.TradingAccountID, instance.OwnerUserID, version.Market, instance.Environment,
	).Take(&account).Error; err != nil {
		return tradingAccountLookupError(err)
	}
	complete, err := tradingRiskComplete(database, account)
	if err != nil {
		return err
	}
	var whitelisted int64
	if err := database.Model(&db.TradingAccountInstrument{}).Where(
		"account_id = ? AND instrument_id = ?", account.ID, version.InstrumentID,
	).Count(&whitelisted).Error; err != nil {
		return err
	}
	control, err := loadTradingControl(database, clause.Locking{})
	if err != nil {
		return err
	}
	if !complete || whitelisted != 1 || account.Status != "active" || control.EmergencyStopped ||
		account.MaxSymbolNotional == nil || account.MaxTotalNotional == nil ||
		instance.AllocationUSDT.GreaterThan(*account.MaxSymbolNotional) ||
		instance.AllocationUSDT.GreaterThan(*account.MaxTotalNotional) {
		return ErrTradingAccountConflict
	}
	if instance.Environment == "testnet" {
		if !validTestnetStopLossRatio(instance.StopLossRatio) {
			return ErrTradingExecutionUnavailable
		}
		if err := testnetAccountReadinessError(database, account.ID); err != nil {
			return err
		}
	}
	if instance.Mode == "auto" && (!account.AutomationEnabled || account.AutomationAuthorizedAt == nil) {
		return ErrTradingAccountConflict
	}
	return nil
}

func currentRiskBreached(account db.TradingAccount, balance db.PaperBalance) bool {
	if account.MaxDailyLoss == nil || account.MaxDrawdown == nil {
		return true
	}
	dailyLoss := balance.DayStartEquity.Sub(balance.Equity)
	drawdown := balance.PeakEquity.Sub(balance.Equity)
	return dailyLoss.GreaterThanOrEqual(*account.MaxDailyLoss) || drawdown.GreaterThanOrEqual(*account.MaxDrawdown)
}

func disableAutoInstances(database *gorm.DB, accountID *uuid.UUID, now time.Time) error {
	query := database.Model(&db.StrategyInstance{}).Where("mode = 'auto' AND is_enabled")
	if accountID != nil {
		query = query.Where("trading_account_id = ?", *accountID)
	}
	return query.Updates(map[string]any{"is_enabled": false, "updated_at": now}).Error
}

func loadTradingControl(database *gorm.DB, locking clause.Locking) (db.TradingControl, error) {
	var control db.TradingControl
	query := database
	if locking.Strength != "" {
		query = query.Clauses(locking)
	}
	err := query.Where("id = 1").Take(&control).Error
	return control, err
}

func (a *App) loadTradingAccountView(database *gorm.DB, row db.TradingAccount) (TradingAccountView, error) {
	var whitelist []db.TradingAccountInstrument
	if err := database.Where("account_id = ?", row.ID).Order("instrument_id").Find(&whitelist).Error; err != nil {
		return TradingAccountView{}, err
	}
	ids := make([]string, 0, len(whitelist))
	for _, item := range whitelist {
		ids = append(ids, item.InstrumentID.String())
	}
	complete, err := tradingRiskComplete(database, row)
	if err != nil {
		return TradingAccountView{}, err
	}
	view := TradingAccountView{
		ID: row.ID.String(), Name: row.Name, Market: row.Market, Environment: row.Environment,
		Status: row.Status, PauseReason: row.PauseReason, AutomationEnabled: row.AutomationEnabled,
		AutomationAuthorized: row.AutomationAuthorizedAt != nil, InitialBalance: row.InitialBalance.String(),
		PaperFeeRate: row.PaperFeeRate.String(), CreatedAt: formatUTC(row.CreatedAt), UpdatedAt: formatUTC(row.UpdatedAt),
		Reconciliation: TestnetReconciliationView{Status: "not_applicable"},
		Risk: TradingRiskView{
			InstrumentIDs: ids, MaxTotalNotional: decimalText(row.MaxTotalNotional),
			MaxSymbolNotional: decimalText(row.MaxSymbolNotional), MaxOrderNotional: decimalText(row.MaxOrderNotional),
			MaxDailyLoss: decimalText(row.MaxDailyLoss), MaxDrawdown: decimalText(row.MaxDrawdown),
			MaxQuoteAgeSeconds: row.MaxQuoteAgeSeconds, Leverage: row.Leverage, Complete: complete,
		},
	}
	if row.AutomationAuthorizedAt != nil {
		value := formatUTC(*row.AutomationAuthorizedAt)
		view.AutomationAuthorizedAt = &value
	}
	if row.Environment == "testnet" {
		view.Reconciliation.Status = "pending"
		credential, err := loadTradingCredential(database, row.ID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return TradingAccountView{}, err
		}
		if err == nil {
			view.CredentialsConfigured = credential.Status == "configured" &&
				credential.APIKeyCiphertext != "" && credential.APISecretCiphertext != ""
			view.CredentialStatus = credential.Status
			view.CredentialVerification = credential.VerificationStatus
			value := formatUTC(credential.UpdatedAt)
			view.CredentialsUpdatedAt = &value
		}
		var reconciliation db.TestnetReconciliation
		if err := database.Where("account_id = ?", row.ID).Take(&reconciliation).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return TradingAccountView{}, err
		} else if err == nil {
			view.Reconciliation = serializeTestnetReconciliation(reconciliation)
		}
	}
	return view, nil
}

func serializeTradingControl(row db.TradingControl) TradingControlView {
	view := TradingControlView{
		EmergencyStopped: row.EmergencyStopped, StopReason: row.StopReason,
		StoppedAt: formatUTC(row.StoppedAt), StoppedByUserID: row.StoppedByUserID,
		ReleasedByUserID: row.ReleasedByUserID, UpdatedAt: formatUTC(row.UpdatedAt),
	}
	if row.ReleasedAt != nil {
		value := formatUTC(*row.ReleasedAt)
		view.ReleasedAt = &value
	}
	return view
}

func serializeTradingIntent(row db.TradingIntent, symbol string) TradingIntentView {
	view := TradingIntentView{
		ID: row.ID.String(), AccountID: row.AccountID.String(), StrategySignalID: row.StrategySignalID.String(),
		StrategyInstanceID: row.StrategyInstanceID.String(), InstrumentID: row.InstrumentID.String(), Symbol: symbol,
		Market: row.Market, Mode: row.Mode, Target: row.Target.String(), Status: row.Status,
		BlockReason: row.BlockReason, ClientOrderID: row.ClientOrderID, CreatedAt: formatUTC(row.CreatedAt),
	}
	if row.CompletedAt != nil {
		value := formatUTC(*row.CompletedAt)
		view.CompletedAt = &value
	}
	return view
}

func serializePaperOrder(row db.PaperOrder, symbol string) PaperOrderView {
	return PaperOrderView{
		ID: row.ID.String(), AccountID: row.AccountID.String(), IntentID: row.IntentID.String(),
		InstrumentID: row.InstrumentID.String(), Symbol: symbol, ClientOrderID: row.ClientOrderID,
		Side: row.Side, Quantity: row.Quantity.String(), FilledQuantity: row.FilledQuantity.String(),
		AveragePrice: row.AveragePrice.String(), Status: row.Status,
		CreatedAt: formatUTC(row.CreatedAt), UpdatedAt: formatUTC(row.UpdatedAt),
	}
}

func serializePaperPosition(row db.PaperPosition, symbol string) PaperPositionView {
	view := PaperPositionView{
		AccountID: row.AccountID.String(), InstrumentID: row.InstrumentID.String(), Symbol: symbol,
		Quantity: row.Quantity.String(), AverageEntryPrice: row.AverageEntryPrice.String(),
		LastPrice: row.LastPrice.String(), RealizedPnl: row.RealizedPnl.String(),
		UnrealizedPnl: row.UnrealizedPnl.String(), UpdatedAt: formatUTC(row.UpdatedAt),
	}
	if row.OwnerStrategyInstanceID != nil {
		value := row.OwnerStrategyInstanceID.String()
		view.OwnerStrategyInstanceID = &value
	}
	return view
}

func serializePaperBalance(row db.PaperBalance) PaperBalanceView {
	return PaperBalanceView{
		AccountID: row.AccountID.String(), CashBalance: row.CashBalance.String(), Equity: row.Equity.String(),
		PeakEquity: row.PeakEquity.String(), DayStartDate: row.DayStartDate.UTC().Format("2006-01-02"),
		DayStartEquity: row.DayStartEquity.String(), RealizedPnl: row.RealizedPnl.String(),
		UnrealizedPnl: row.UnrealizedPnl.String(), Fees: row.Fees.String(), Funding: row.Funding.String(),
		UpdatedAt: formatUTC(row.UpdatedAt),
	}
}

func serializeTestnetReconciliation(row db.TestnetReconciliation) TestnetReconciliationView {
	view := TestnetReconciliationView{
		Status: row.Status, ErrorCode: row.ErrorCode,
		BalanceCount: row.BalanceCount, PositionCount: row.PositionCount, OpenOrderCount: row.OpenOrderCount,
	}
	lastAttemptedAt := formatUTC(row.LastAttemptedAt)
	view.LastAttemptedAt = &lastAttemptedAt
	if row.LastObservedAt != nil {
		lastObservedAt := formatUTC(*row.LastObservedAt)
		view.LastObservedAt = &lastObservedAt
	}
	return view
}

func serializeTestnetBalance(row db.TestnetBalance) TestnetBalanceView {
	return TestnetBalanceView{
		AccountID: row.AccountID.String(), Asset: row.Asset,
		TotalBalance: row.TotalBalance.String(), AvailableBalance: row.AvailableBalance.String(),
		ObservedAt: formatUTC(row.ObservedAt),
	}
}

func serializeTestnetPosition(row db.TestnetPosition) TestnetPositionView {
	return TestnetPositionView{
		AccountID: row.AccountID.String(), NativeSymbol: row.NativeSymbol, PositionSide: row.PositionSide,
		Quantity: row.Quantity.String(), EntryPrice: row.EntryPrice.String(),
		UnrealizedPnl: row.UnrealizedPnL.String(), ObservedAt: formatUTC(row.ObservedAt),
	}
}

func serializeTestnetOpenOrder(row db.TestnetOpenOrder) TestnetOpenOrderView {
	return TestnetOpenOrderView{
		AccountID: row.AccountID.String(), NativeSymbol: row.NativeSymbol,
		ExchangeOrderID: strconv.FormatInt(row.ExchangeOrderID, 10), ClientOrderID: row.ClientOrderID,
		Side: row.Side, OrderType: row.OrderType, Status: row.Status,
		Price: row.Price.String(), OriginalQuantity: row.OriginalQuantity.String(),
		ExecutedQuantity: row.ExecutedQuantity.String(), StopPrice: row.StopPrice.String(),
		ObservedAt: formatUTC(row.ObservedAt),
	}
}

func serializeTestnetOrder(row db.TestnetOrder, symbol string) TestnetOrderView {
	view := TestnetOrderView{
		ID: row.ID.String(), AccountID: row.AccountID.String(), IntentID: row.IntentID.String(),
		InstrumentID: row.InstrumentID.String(), Symbol: symbol, ClientOrderID: row.ClientOrderID,
		Side: row.Side, Quantity: row.Quantity.String(),
		FilledQuantity:          row.FilledQuantity.String(),
		CumulativeQuoteQuantity: row.CumulativeQuoteQuantity.String(),
		AveragePrice:            row.AveragePrice.String(), Purpose: row.Purpose, OrderType: row.OrderType,
		StopPrice: row.StopPrice.String(), ClosePosition: row.ClosePosition, ReduceOnly: row.ReduceOnly,
		WorkingType: row.WorkingType, Status: row.Status, LastErrorCode: row.LastErrorCode,
		SubmitAttemptCount: row.SubmitAttemptCount, QueryAttemptCount: row.QueryAttemptCount,
		SubmittedAt: formatUTC(row.SubmittedAt), CreatedAt: formatUTC(row.CreatedAt), UpdatedAt: formatUTC(row.UpdatedAt),
	}
	if row.ExchangeOrderID != nil {
		exchangeOrderID := strconv.FormatInt(*row.ExchangeOrderID, 10)
		view.ExchangeOrderID = &exchangeOrderID
	}
	if row.ReplacesOrderID != nil {
		replacesOrderID := row.ReplacesOrderID.String()
		view.ReplacesOrderID = &replacesOrderID
	}
	if row.LastQueriedAt != nil {
		lastQueriedAt := formatUTC(*row.LastQueriedAt)
		view.LastQueriedAt = &lastQueriedAt
	}
	if row.ObservedAt != nil {
		observedAt := formatUTC(*row.ObservedAt)
		view.ObservedAt = &observedAt
	}
	return view
}

func loadTradingSymbols(
	database *gorm.DB,
	intents []db.TradingIntent,
	orders []db.PaperOrder,
	positions []db.PaperPosition,
	testnetOrders []db.TestnetOrder,
) (map[uuid.UUID]string, error) {
	ids := map[uuid.UUID]bool{}
	for _, row := range intents {
		ids[row.InstrumentID] = true
	}
	for _, row := range orders {
		ids[row.InstrumentID] = true
	}
	for _, row := range positions {
		ids[row.InstrumentID] = true
	}
	for _, row := range testnetOrders {
		ids[row.InstrumentID] = true
	}
	keys := make([]uuid.UUID, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	result := map[uuid.UUID]string{}
	if len(keys) == 0 {
		return result, nil
	}
	var instruments []db.MarketInstrument
	if err := database.Select("id", "native_symbol").Where("id IN ?", keys).Find(&instruments).Error; err != nil {
		return nil, err
	}
	for _, instrument := range instruments {
		result[instrument.ID] = instrument.NativeSymbol
	}
	return result, nil
}

func parseTradingDecimal(raw, field string, signed bool) (decimal.Decimal, error) {
	raw = strings.TrimSpace(raw)
	negative := strings.HasPrefix(raw, "-")
	if negative {
		if !signed {
			return decimal.Zero, invalidTrading(field + " must be a decimal string")
		}
		raw = strings.TrimPrefix(raw, "-")
	}
	value, err := marketdata.ParseDecimal(raw)
	if err != nil {
		return decimal.Zero, invalidTrading(field + " must be a decimal string")
	}
	if negative {
		value = value.Neg()
	}
	return value, nil
}

func parseOptionalTradingLimit(raw, field string) (*decimal.Decimal, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value, err := parseTradingDecimal(raw, field, false)
	if err != nil || !value.IsPositive() {
		return nil, invalidTrading(field + " must be a positive decimal string")
	}
	return &value, nil
}

func requiredTradingUUID(raw, field string) (uuid.UUID, error) {
	id, err := parseRequiredUUIDv7(raw, field)
	if err != nil {
		return uuid.Nil, invalidTrading(err.Error())
	}
	return id, nil
}

func tradingAccountLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrTradingAccountMissing
	}
	return err
}

func decimalText(value *decimal.Decimal) *string {
	if value == nil {
		return nil
	}
	text := value.String()
	return &text
}

func invalidTrading(detail string) error {
	return fmt.Errorf("%w: %s", ErrInvalidTradingRequest, detail)
}

func utcDay(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func tradingClientOrderID(id uuid.UUID) string {
	return "cs" + strings.ReplaceAll(id.String(), "-", "")[:30]
}
