package binance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func registerPaper(registrar sdk.Registrar, runtime *binanceRuntime) error {
	return registrar.Action(withNodeMeta(sdk.NodeDescriptor{
		Type: "official.binance.paper_execute", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"initialBalance":{"type":"string","title":"初始余额","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"feeRate":{"type":"string","title":"手续费率","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"maxOrderNotional":{"type":"string","title":"最大订单名义金额","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"maxInstrumentNotional":{"type":"string","title":"单交易对最大名义金额","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true}},"required":["initialBalance","feeRate","maxOrderNotional","maxInstrumentNotional"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["initialBalance","feeRate","maxOrderNotional","maxInstrumentNotional"]}`),
		InputSchema:  orderIntentSchema(), OutputSchema: orderResultSchema(), Pool: sdk.PoolStream, SideEffect: sdk.SideEffectPaper, State: sdk.StatePersistent,
	}, "Paper 执行", "使用 Binance 最新 Quote 模拟成交并记录订单、成交和持仓。", "策略", "#2563eb", "flask-conical"), paperExecuteAction{runtime: runtime})
}

type paperExecuteAction struct{ runtime *binanceRuntime }

func (a paperExecuteAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	var config struct{ InitialBalance, FeeRate, MaxOrderNotional, MaxInstrumentNotional string }
	var intent struct{ Venue, Account, Market, Instrument, Side, Quantity, QuoteAmount, PositionEffect, ClientOrderID string }
	if json.Unmarshal(request.Config, &config) != nil || json.Unmarshal(request.Input, &intent) != nil || intent.Venue != "binance" {
		return sdk.ActionResult{}, errors.New("Binance Paper order is invalid")
	}
	initialBalance, e1 := decimal.NewFromString(config.InitialBalance)
	feeRate, e2 := decimal.NewFromString(config.FeeRate)
	maxOrder, e3 := decimal.NewFromString(config.MaxOrderNotional)
	maxInstrument, e4 := decimal.NewFromString(config.MaxInstrumentNotional)
	quantity, e5 := decimal.NewFromString(zeroIfEmpty(intent.Quantity))
	quoteAmount, e6 := decimal.NewFromString(zeroIfEmpty(intent.QuoteAmount))
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || e6 != nil || initialBalance.Sign() <= 0 || feeRate.Sign() < 0 || maxOrder.Sign() <= 0 || maxInstrument.Sign() <= 0 {
		return sdk.ActionResult{}, errors.New("Binance Paper limits are invalid")
	}
	intent.Market = strings.ToLower(strings.TrimSpace(intent.Market))
	intent.Account = strings.TrimSpace(intent.Account)
	intent.Instrument = strings.ToUpper(strings.TrimSpace(intent.Instrument))
	intent.Side = strings.ToLower(strings.TrimSpace(intent.Side))
	intent.PositionEffect = strings.ToLower(strings.TrimSpace(intent.PositionEffect))
	if !clientOrderIDPattern.MatchString(intent.ClientOrderID) || !accountIDPattern.MatchString(intent.Account) || (quantity.Sign() <= 0 && quoteAmount.Sign() <= 0) || (quantity.Sign() > 0 && quoteAmount.Sign() > 0) ||
		(intent.Market != "spot" && intent.Market != "usdm") || !instrumentPattern.MatchString(intent.Instrument) ||
		(intent.Side != "buy" && intent.Side != "sell") || (intent.PositionEffect != "open" && intent.PositionEffect != "reduce") ||
		intent.Market == "usdm" && quoteAmount.Sign() > 0 {
		return sdk.ActionResult{}, errors.New("Binance Paper order identity is invalid")
	}
	if existing, ok, err := a.runtime.orderByClientID(ctx, intent.ClientOrderID); err != nil {
		return sdk.ActionResult{}, err
	} else if ok {
		if !matchesOrderIntent(existing, "paper", intent.Account, intent.Market, intent.Instrument, intent.Side, intent.PositionEffect, intent.ClientOrderID, quantity, quoteAmount) {
			return sdk.ActionResult{}, errors.New("Binance clientOrderId belongs to a different order intent")
		}
		return sdk.ActionResult{Output: marshalOrder(existing)}, nil
	}
	workflowID, workflowErr := strconv.ParseInt(request.Revision.WorkflowID, 10, 64)
	if workflowErr != nil || workflowID <= 0 || strings.TrimSpace(request.NodeInstanceID) == "" {
		return sdk.ActionResult{}, errors.New("Binance Paper workflow identity is invalid")
	}
	requestedQuantity, requestedQuoteAmount := quantity, quoteAmount
	quote, err := (marketDataProvider{runtime: a.runtime}).Quote(ctx, sdk.QuoteQuery{Market: intent.Market, Instrument: intent.Instrument})
	if err != nil || quote.Price.Sign() <= 0 {
		return sdk.ActionResult{}, errors.New("Binance Paper quote is unavailable")
	}
	if quantity.Sign() <= 0 {
		quantity = quoteAmount.Div(quote.Price)
	}
	if quoteAmount.Sign() == 0 {
		if err := a.runtime.validateOrderRules(ctx, sdk.OrderRequest{Market: intent.Market, Instrument: intent.Instrument, Quantity: quantity}); err != nil {
			return sdk.ActionResult{}, err
		}
	}
	notional := quantity.Mul(quote.Price)
	if notional.Sign() <= 0 || notional.GreaterThan(maxOrder) {
		return sdk.ActionResult{}, errors.New("Binance Paper order exceeds the order notional limit")
	}
	var stored tradingOrder
	err = a.runtime.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, "binance-paper:"+intent.Account).Error; err != nil {
			return errors.New("lock Binance Paper account failed")
		}
		openingKey := paperOpeningOperationKey(intent.Account)
		opening := paperLedgerEntry{Account: intent.Account, OperationKey: openingKey, EntryType: "opening_balance", Amount: initialBalance, OccurredAt: time.Now().UTC()}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "operation_key"}, {Name: "entry_type"}}, DoNothing: true}).Create(&opening).Error; err != nil {
			return errors.New("persist Binance Paper opening balance failed")
		}
		var persistedOpening paperLedgerEntry
		if err := tx.Where("operation_key = ? AND entry_type = 'opening_balance'", openingKey).First(&persistedOpening).Error; err != nil || persistedOpening.Account != intent.Account || !persistedOpening.Amount.Equal(initialBalance) {
			return errors.New("Binance Paper initial balance conflicts with the existing account")
		}
		var position tradingPosition
		positionErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account = ? AND mode = 'paper' AND market = ? AND instrument = ?", intent.Account, intent.Market, strings.ToUpper(intent.Instrument)).First(&position).Error
		if positionErr != nil && !errors.Is(positionErr, gorm.ErrRecordNotFound) {
			return errors.New("load Binance Paper position failed")
		}
		delta := quantity
		if intent.Side == "sell" {
			delta = delta.Neg()
		}
		nextQuantity := position.Quantity.Add(delta)
		if nextQuantity.Abs().Mul(quote.Price).GreaterThan(maxInstrument) {
			return errors.New("Binance Paper instrument notional limit exceeded")
		}
		if intent.Market == "spot" && nextQuantity.Sign() < 0 {
			return errors.New("Binance Paper position is insufficient")
		}
		if intent.Market == "usdm" && intent.PositionEffect == "reduce" &&
			(position.Quantity.IsZero() || position.Quantity.Sign() == delta.Sign() || nextQuantity.Sign() != 0 && nextQuantity.Sign() != position.Quantity.Sign()) {
			return errors.New("Binance Paper reduce order would increase or reverse the position")
		}
		fee := notional.Mul(feeRate)
		var cash decimal.Decimal
		if err := tx.Model(&paperLedgerEntry{}).Where("account = ?", intent.Account).Select("COALESCE(SUM(amount), 0)").Scan(&cash).Error; err != nil {
			return errors.New("load Binance Paper cash balance failed")
		}
		if intent.Market == "spot" && intent.Side == "buy" && cash.LessThan(notional.Add(fee)) ||
			intent.Market == "usdm" && cash.LessThan(fee) {
			return errors.New("Binance Paper cash balance is insufficient")
		}
		now := time.Now().UTC()
		stored = tradingOrder{WorkflowID: workflowID, NodeInstanceID: request.NodeInstanceID, Account: intent.Account, Market: strings.ToLower(intent.Market), Instrument: strings.ToUpper(intent.Instrument), ClientOrderID: intent.ClientOrderID, Side: strings.ToLower(intent.Side), RequestQuantity: requestedQuantity, RequestQuoteAmount: requestedQuoteAmount, PositionEffect: intent.PositionEffect, Quantity: quantity, Executed: quantity, AveragePrice: quote.Price, Notional: notional, Status: "filled", Mode: "paper", OperationKey: request.OperationKey, CreatedAt: now, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "client_order_id"}}, DoNothing: true}).Create(&stored).Error; err != nil {
			return errors.New("persist Binance Paper order failed")
		}
		if stored.ID == 0 {
			if err := tx.Where("client_order_id = ?", intent.ClientOrderID).First(&stored).Error; err != nil {
				return err
			}
			if !matchesOrderIntent(stored, "paper", intent.Account, intent.Market, intent.Instrument, intent.Side, intent.PositionEffect, intent.ClientOrderID, requestedQuantity, requestedQuoteAmount) {
				return errors.New("Binance clientOrderId belongs to a different order intent")
			}
			return nil
		}
		position.Account, position.Mode, position.Market, position.Instrument = intent.Account, "paper", strings.ToLower(intent.Market), strings.ToUpper(intent.Instrument)
		previousQuantity, previousAverage := position.Quantity, position.AveragePrice
		position.Quantity, position.AveragePrice = nextLivePosition(previousQuantity, previousAverage, delta, quote.Price)
		position.UpdatedAt = now
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "account"}, {Name: "mode"}, {Name: "market"}, {Name: "instrument"}}, DoUpdates: clause.AssignmentColumns([]string{"quantity", "average_price", "updated_at"})}).Create(&position).Error; err != nil {
			return errors.New("persist Binance Paper position failed")
		}
		fill := tradingFill{OrderID: stored.ID, ProviderTradeID: request.OperationKey, Quantity: quantity, Price: quote.Price, Fee: fee, FeeAsset: "QUOTE", FilledAt: now}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "provider_trade_id"}}, DoNothing: true}).Create(&fill).Error; err != nil {
			return errors.New("persist Binance Paper fill failed")
		}
		if err := tx.Create(&tradingFee{FillID: fill.ID, Amount: fill.Fee, Asset: fill.FeeAsset, CreatedAt: now}).Error; err != nil {
			return errors.New("persist Binance Paper fee failed")
		}
		cashDelta := decimal.Zero
		if intent.Market == "spot" {
			cashDelta = notional.Neg()
			if intent.Side == "sell" {
				cashDelta = notional
			}
		} else if previousQuantity.Sign() != 0 && previousQuantity.Sign() != delta.Sign() {
			closed := decimal.Min(previousQuantity.Abs(), delta.Abs())
			cashDelta = quote.Price.Sub(previousAverage).Mul(closed)
			if previousQuantity.Sign() < 0 {
				cashDelta = cashDelta.Neg()
			}
		}
		if err := tx.Create(&paperLedgerEntry{Account: intent.Account, OperationKey: request.OperationKey, EntryType: "trade", Amount: cashDelta, OccurredAt: now}).Error; err != nil {
			return errors.New("persist Binance Paper ledger failed")
		}
		return tx.Create(&paperLedgerEntry{Account: intent.Account, OperationKey: request.OperationKey, EntryType: "fee", Amount: fee.Neg(), OccurredAt: now}).Error
	})
	if err != nil {
		return sdk.ActionResult{}, err
	}
	request.Logger.Info("Binance Paper order filled", "account", intent.Account, "market", intent.Market, "instrument", intent.Instrument, "client_order_id", intent.ClientOrderID)
	return sdk.ActionResult{Output: marshalOrder(stored)}, nil
}

func paperOpeningOperationKey(account string) string {
	digest := sha256.Sum256([]byte(account))
	return fmt.Sprintf("opening-%x", digest[:28])
}

var _ sdk.ActionHandler = paperExecuteAction{}
