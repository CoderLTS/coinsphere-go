package binance

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxPrivateResponseBytes = 1 << 20

type executionProvider struct{ runtime *binanceRuntime }

func (executionProvider) ID() string { return "binance" }

func (p executionProvider) PlaceOrder(ctx context.Context, request sdk.OrderRequest) (sdk.OrderResult, error) {
	if err := validateOrderRequest(request); err != nil {
		return sdk.OrderResult{}, err
	}
	if err := p.runtime.validateOrderRules(ctx, request); err != nil {
		return sdk.OrderResult{}, err
	}
	responseType := "FULL"
	if request.Market == "usdm" {
		responseType = "RESULT"
	}
	values := url.Values{"symbol": {strings.ToUpper(request.Instrument)}, "side": {strings.ToUpper(request.Side)}, "type": {"MARKET"}, "newClientOrderId": {request.ClientOrderID}, "newOrderRespType": {responseType}}
	if request.Quantity.Sign() > 0 {
		values.Set("quantity", request.Quantity.String())
	} else if request.Market == "spot" {
		values.Set("quoteOrderQty", request.QuoteAmount.String())
	} else {
		return sdk.OrderResult{}, errors.New("Binance USD-M market order requires quantity")
	}
	if request.Market == "usdm" && request.PositionEffect == "reduce" {
		values.Set("reduceOnly", "true")
	}
	var payload map[string]any
	if err := p.runtime.privateJSON(ctx, request.Market, http.MethodPost, orderPath(request.Market), values, request.Secrets, request.ProxyID, &payload); err != nil {
		return sdk.OrderResult{}, err
	}
	return parseOrderResult(request.Market, payload)
}

func (p executionProvider) GetOrder(ctx context.Context, request sdk.OrderQuery) (sdk.OrderResult, error) {
	if err := validateOrderQuery(request); err != nil {
		return sdk.OrderResult{}, err
	}
	values := url.Values{"symbol": {strings.ToUpper(request.Instrument)}}
	if request.OrderID != "" {
		values.Set("orderId", request.OrderID)
	} else {
		values.Set("origClientOrderId", request.ClientOrderID)
	}
	var payload map[string]any
	if err := p.runtime.privateJSON(ctx, request.Market, http.MethodGet, orderPath(request.Market), values, request.Secrets, request.ProxyID, &payload); err != nil {
		return sdk.OrderResult{}, err
	}
	return parseOrderResult(request.Market, payload)
}

func (p executionProvider) CancelOrder(ctx context.Context, request sdk.CancelOrderRequest) error {
	if err := validateOrderQuery(request); err != nil {
		return err
	}
	values := url.Values{"symbol": {strings.ToUpper(request.Instrument)}}
	if request.OrderID != "" {
		values.Set("orderId", request.OrderID)
	} else {
		values.Set("origClientOrderId", request.ClientOrderID)
	}
	return p.runtime.privateJSON(ctx, request.Market, http.MethodDelete, orderPath(request.Market), values, request.Secrets, request.ProxyID, &map[string]any{})
}

func registerExecution(registrar sdk.Registrar, runtime *binanceRuntime) error {
	if err := registrar.ExecutionProvider(executionProvider{runtime: runtime}); err != nil {
		return err
	}
	if err := registrar.Action(withNodeMeta(sdk.NodeDescriptor{
		Type: "official.binance.live_execute", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"liveTradingEnabled":{"type":"boolean","title":"Enable live trading","default":false},"accountConfirmed":{"type":"boolean","title":"Account manually confirmed","default":false},"proxyId":{"type":"integer","title":"Proxy","minimum":0,"default":0,"x-coinsphere-proxy":true},"maxOrderNotional":{"type":"string","title":"Max order notional","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"maxInstrumentNotional":{"type":"string","title":"Max instrument notional","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"maxDailyLoss":{"type":"string","title":"Max daily loss","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"maxDailyOrders":{"type":"integer","title":"Max daily orders","minimum":1,"maximum":10000},"maxSlippage":{"type":"string","title":"Max slippage","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"maxQuoteAgeSeconds":{"type":"integer","title":"Max quote age","minimum":1,"maximum":300},"apiKey":{"type":"string","title":"API key","x-coinsphere-secret":true},"apiSecret":{"type":"string","title":"API secret","x-coinsphere-secret":true}},"required":["liveTradingEnabled","accountConfirmed","maxOrderNotional","maxInstrumentNotional","maxDailyLoss","maxDailyOrders","maxSlippage","maxQuoteAgeSeconds","apiKey","apiSecret"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["liveTradingEnabled","accountConfirmed","proxyId","maxOrderNotional","maxInstrumentNotional","maxDailyLoss","maxDailyOrders","maxSlippage","maxQuoteAgeSeconds","apiKey","apiSecret"]}`),
		InputSchema:  orderIntentSchema(), OutputSchema: orderResultSchema(), Pool: sdk.PoolStream, SideEffect: sdk.SideEffectData, State: sdk.StateStateless,
	}, "Binance 真实下单", "通过完整风险门禁提交 Binance 市价单。", "交易", "#b45309", "badge-dollar-sign"), liveExecuteAction{runtime: runtime}); err != nil {
		return err
	}
	return registerPrivateAccountStream(registrar, runtime)
}

type liveExecuteAction struct{ runtime *binanceRuntime }

func (a liveExecuteAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	var config struct {
		LiveTradingEnabled                                                 bool  `json:"liveTradingEnabled"`
		AccountConfirmed                                                   bool  `json:"accountConfirmed"`
		ProxyID                                                            int64 `json:"proxyId"`
		MaxOrderNotional, MaxInstrumentNotional, MaxDailyLoss, MaxSlippage string
		MaxDailyOrders, MaxQuoteAgeSeconds                                 int
	}
	var intent struct{ Venue, Account, Market, Instrument, Side, Quantity, QuoteAmount, PositionEffect, ClientOrderID, ReferencePrice, QuotedAt string }
	if json.Unmarshal(request.Config, &config) != nil || json.Unmarshal(request.Input, &intent) != nil {
		return sdk.ActionResult{}, errors.New("Binance live order configuration is invalid")
	}
	if !config.LiveTradingEnabled || !config.AccountConfirmed {
		return sdk.ActionResult{}, errors.New("Binance live trading is disabled or not manually confirmed")
	}
	if intent.Venue != "binance" || intent.ClientOrderID == "" {
		return sdk.ActionResult{}, errors.New("Binance order intent is invalid")
	}
	quantity, quantityErr := decimal.NewFromString(zeroIfEmpty(intent.Quantity))
	quoteAmount, quoteErr := decimal.NewFromString(zeroIfEmpty(intent.QuoteAmount))
	reference, referenceErr := decimal.NewFromString(intent.ReferencePrice)
	quotedAt, timeErr := time.Parse(time.RFC3339, intent.QuotedAt)
	limits, limitErr := parseRiskLimits(config.MaxOrderNotional, config.MaxInstrumentNotional, config.MaxDailyLoss, config.MaxSlippage, config.MaxDailyOrders, config.MaxQuoteAgeSeconds)
	if quantityErr != nil || quoteErr != nil || referenceErr != nil || timeErr != nil || limitErr != nil {
		return sdk.ActionResult{}, errors.New("Binance order intent or risk limits are invalid")
	}
	intent.Account = strings.TrimSpace(intent.Account)
	intent.Market = strings.ToLower(strings.TrimSpace(intent.Market))
	intent.Instrument = strings.ToUpper(strings.TrimSpace(intent.Instrument))
	intent.Side = strings.ToLower(strings.TrimSpace(intent.Side))
	intent.PositionEffect = strings.ToLower(strings.TrimSpace(intent.PositionEffect))
	if validateOrderRequest(sdk.OrderRequest{
		Account: intent.Account, Market: intent.Market, Instrument: intent.Instrument, Side: intent.Side,
		Quantity: quantity, QuoteAmount: quoteAmount, PositionEffect: intent.PositionEffect, ClientOrderID: intent.ClientOrderID,
	}) != nil {
		return sdk.ActionResult{}, errors.New("Binance order intent is invalid")
	}
	unlock := a.runtime.lockLiveAccount(intent.Account, intent.Market)
	defer unlock()
	var release liveAccountRelease
	releaseErr := a.runtime.db.WithContext(ctx).Where("account = ? AND market = ? AND enabled", intent.Account, intent.Market).First(&release).Error
	if errors.Is(releaseErr, gorm.ErrRecordNotFound) {
		return sdk.ActionResult{}, errors.New("Binance live account has not been manually released")
	}
	if releaseErr != nil {
		return sdk.ActionResult{}, errors.New("load Binance live account release failed")
	}
	if existing, ok, err := a.runtime.orderByClientID(ctx, intent.ClientOrderID); err != nil {
		return sdk.ActionResult{}, err
	} else if ok {
		if !matchesOrderIntent(existing, "live", intent.Account, intent.Market, intent.Instrument, intent.Side, intent.PositionEffect, intent.ClientOrderID, quantity, quoteAmount) {
			return sdk.ActionResult{}, errors.New("Binance clientOrderId belongs to a different order intent")
		}
		if existing.Status == "reconciling" {
			result, queryErr := (executionProvider{runtime: a.runtime}).GetOrder(ctx, sdk.OrderQuery{Account: intent.Account, Market: intent.Market, Instrument: intent.Instrument, ClientOrderID: intent.ClientOrderID, Secrets: request.Secrets, ProxyID: config.ProxyID})
			if queryErr == nil {
				_ = a.runtime.updateReconciledOrder(ctx, existing, result)
				existing, _, _ = a.runtime.orderByClientID(ctx, intent.ClientOrderID)
			}
		}
		return sdk.ActionResult{Output: marshalOrder(existing)}, nil
	}
	quote, err := (marketDataProvider{runtime: a.runtime}).Quote(ctx, sdk.QuoteQuery{Market: intent.Market, Instrument: intent.Instrument, ProxyID: config.ProxyID})
	if err != nil {
		return sdk.ActionResult{}, err
	}
	if err := a.runtime.verifyNoWithdrawalPermission(ctx, intent.Market, request.Secrets, config.ProxyID); err != nil {
		return sdk.ActionResult{}, err
	}
	if intent.Market == "usdm" {
		if err := a.runtime.verifyOneWayMode(ctx, request.Secrets, config.ProxyID); err != nil {
			return sdk.ActionResult{}, err
		}
	}
	accountConfig := privateAccountStreamConfig{Account: intent.Account, Market: intent.Market, ProxyID: config.ProxyID, ReconciliationSeconds: 60}
	if err := a.runtime.reconcileAccountState(ctx, accountConfig, request.Secrets, intent.Instrument); err != nil {
		return sdk.ActionResult{}, errors.New("reconcile Binance account before live order failed")
	}
	if err := a.runtime.checkLiveRisk(ctx, intent.Account, intent.Market, intent.Instrument, intent.Side, intent.PositionEffect, quantity, quoteAmount, reference, quotedAt.UTC(), quote, limits); err != nil {
		request.Logger.Warn("Binance live order rejected", "reason", err.Error())
		return sdk.ActionResult{}, err
	}
	workflowID, workflowErr := strconv.ParseInt(request.Revision.WorkflowID, 10, 64)
	if workflowErr != nil || workflowID <= 0 || strings.TrimSpace(request.NodeInstanceID) == "" {
		return sdk.ActionResult{}, errors.New("Binance live workflow identity is invalid")
	}
	now := time.Now().UTC()
	pending := tradingOrder{WorkflowID: workflowID, NodeInstanceID: request.NodeInstanceID, Account: intent.Account, Market: intent.Market, Instrument: intent.Instrument, ClientOrderID: intent.ClientOrderID, Side: intent.Side, RequestQuantity: quantity, RequestQuoteAmount: quoteAmount, PositionEffect: intent.PositionEffect, Quantity: quantity, Status: "reconciling", Mode: "live", OperationKey: request.OperationKey, CreatedAt: now, UpdatedAt: now}
	created := a.runtime.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "client_order_id"}}, DoNothing: true}).Create(&pending)
	if created.Error != nil {
		return sdk.ActionResult{}, errors.New("persist Binance live order intent failed")
	}
	if created.RowsAffected == 0 {
		existing, _, loadErr := a.runtime.orderByClientID(ctx, intent.ClientOrderID)
		if loadErr != nil {
			return sdk.ActionResult{}, loadErr
		}
		if !matchesOrderIntent(existing, "live", intent.Account, intent.Market, intent.Instrument, intent.Side, intent.PositionEffect, intent.ClientOrderID, quantity, quoteAmount) {
			return sdk.ActionResult{}, errors.New("Binance clientOrderId belongs to a different order intent")
		}
		return sdk.ActionResult{Output: marshalOrder(existing)}, nil
	}
	result, err := (executionProvider{runtime: a.runtime}).PlaceOrder(ctx, sdk.OrderRequest{Account: intent.Account, Market: intent.Market, Instrument: intent.Instrument, Side: intent.Side, Quantity: quantity, QuoteAmount: quoteAmount, PositionEffect: intent.PositionEffect, ClientOrderID: intent.ClientOrderID, Secrets: request.Secrets, ProxyID: config.ProxyID})
	if err != nil {
		return sdk.ActionResult{}, err
	}
	if err := a.runtime.updateReconciledOrder(ctx, pending, result); err != nil {
		return sdk.ActionResult{}, errors.New("persist Binance order failed")
	}
	stored, _, err := a.runtime.orderByClientID(ctx, intent.ClientOrderID)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	if err := a.runtime.reconcileOrderTrades(ctx, stored, accountConfig, request.Secrets); err != nil {
		request.Logger.Warn("Binance live fills await reconciliation", "client_order_id", intent.ClientOrderID)
	}
	request.Logger.Info("Binance live order accepted", "account", intent.Account, "market", intent.Market, "instrument", intent.Instrument, "client_order_id", intent.ClientOrderID, "provider_order_id", stored.ProviderOrderID, "status", stored.Status)
	return sdk.ActionResult{Output: marshalOrder(stored)}, nil
}

type riskLimits struct {
	orderNotional, instrumentNotional, dailyLoss, slippage decimal.Decimal
	dailyOrders, quoteAge                                  int
}

func parseRiskLimits(order, instrument, loss, slippage string, dailyOrders, quoteAge int) (riskLimits, error) {
	a, e1 := decimal.NewFromString(order)
	b, e2 := decimal.NewFromString(instrument)
	c, e3 := decimal.NewFromString(loss)
	d, e4 := decimal.NewFromString(slippage)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || a.Sign() <= 0 || b.Sign() <= 0 || c.Sign() <= 0 || d.Sign() < 0 || d.GreaterThan(decimal.NewFromInt(1)) || dailyOrders < 1 || quoteAge < 1 {
		return riskLimits{}, errors.New("invalid risk limits")
	}
	return riskLimits{a, b, c, d, dailyOrders, quoteAge}, nil
}

func (q *binanceRuntime) checkLiveRisk(ctx context.Context, account, market, instrument, side, positionEffect string, quantity, quoteAmount, reference decimal.Decimal, quotedAt time.Time, quote sdk.Quote, limits riskLimits) error {
	now := time.Now().UTC()
	if account == "" || quotedAt.After(now) || quote.QuotedAt.After(now) || now.Sub(quotedAt) > time.Duration(limits.quoteAge)*time.Second || now.Sub(quote.QuotedAt) > time.Duration(limits.quoteAge)*time.Second {
		return errors.New("Binance quote is stale")
	}
	if reference.Sign() <= 0 || quote.Price.Sub(reference).Abs().Div(reference).GreaterThan(limits.slippage) {
		return errors.New("Binance quote exceeds the slippage limit")
	}
	notional := quoteAmount
	if notional.Sign() == 0 {
		notional = quantity.Mul(quote.Price)
	}
	if notional.Sign() <= 0 || notional.GreaterThan(limits.orderNotional) {
		return errors.New("Binance order exceeds the order notional limit")
	}
	var count int64
	day := time.Now().UTC().Truncate(24 * time.Hour)
	if err := q.db.WithContext(ctx).Model(&tradingOrder{}).Where("account = ? AND mode = 'live' AND created_at >= ?", account, day).Count(&count).Error; err != nil {
		return errors.New("load Binance daily order count failed")
	}
	if count >= int64(limits.dailyOrders) {
		return errors.New("Binance daily order limit reached")
	}
	var position tradingPosition
	positionErr := q.db.WithContext(ctx).Where("account = ? AND mode = 'live' AND market = ? AND instrument = ?", account, market, instrument).First(&position).Error
	if positionErr != nil && !errors.Is(positionErr, gorm.ErrRecordNotFound) {
		return errors.New("load Binance position risk failed")
	}
	delta := quantity
	if delta.Sign() == 0 {
		delta = quoteAmount.Div(quote.Price)
	}
	if side == "sell" {
		delta = delta.Neg()
	}
	nextQuantity := position.Quantity.Add(delta)
	if nextQuantity.Abs().Mul(quote.Price).GreaterThan(limits.instrumentNotional) {
		return errors.New("Binance instrument notional limit exceeded")
	}
	if market == "spot" && nextQuantity.Sign() < 0 {
		return errors.New("Binance Spot position is insufficient")
	}
	if market == "usdm" && positionEffect == "reduce" &&
		(position.Quantity.IsZero() || position.Quantity.Sign() == delta.Sign() || nextQuantity.Sign() != 0 && nextQuantity.Sign() != position.Quantity.Sign()) {
		return errors.New("Binance reduce order would increase or reverse the position")
	}
	asset := "USD"
	if market == "spot" {
		var row binanceInstrument
		if err := q.db.WithContext(ctx).Where("market = ? AND symbol = ?", market, instrument).First(&row).Error; err != nil {
			return errors.New("load Binance instrument risk rules failed")
		}
		asset = row.QuoteAsset
	}
	var first, latest accountSnapshot
	firstErr := q.db.WithContext(ctx).Where("account = ? AND market = ? AND asset = ? AND captured_at >= ?", account, market, asset, day).Order("captured_at").First(&first).Error
	latestErr := q.db.WithContext(ctx).Where("account = ? AND market = ? AND asset = ? AND captured_at >= ?", account, market, asset, day).Order("captured_at DESC").First(&latest).Error
	if firstErr != nil || latestErr != nil {
		return errors.New("Binance daily loss evidence is unavailable")
	}
	if first.Equity.Sub(latest.Equity).GreaterThanOrEqual(limits.dailyLoss) {
		return errors.New("Binance daily loss limit reached")
	}
	return nil
}

func (q *binanceRuntime) verifyNoWithdrawalPermission(ctx context.Context, market string, secrets sdk.SecretReader, proxyID int64) error {
	var payload struct {
		EnableWithdrawals bool `json:"enableWithdrawals"`
	}
	if err := q.privateJSON(ctx, market, http.MethodGet, "/sapi/v1/account/apiRestrictions", url.Values{}, secrets, proxyID, &payload); err != nil {
		return errors.New("verify Binance API key permissions failed")
	}
	if payload.EnableWithdrawals {
		return errors.New("Binance API key with withdrawal permission is prohibited")
	}
	return nil
}

func (q *binanceRuntime) verifyOneWayMode(ctx context.Context, secrets sdk.SecretReader, proxyID int64) error {
	var payload struct {
		DualSidePosition bool `json:"dualSidePosition"`
	}
	if err := q.privateJSON(ctx, "usdm", http.MethodGet, "/fapi/v1/positionSide/dual", url.Values{}, secrets, proxyID, &payload); err != nil {
		return errors.New("verify Binance USD-M position mode failed")
	}
	if payload.DualSidePosition {
		return errors.New("Binance USD-M hedge mode is unsupported")
	}
	return nil
}

func (q *binanceRuntime) validateOrderRules(ctx context.Context, request sdk.OrderRequest) error {
	if request.Quantity.Sign() <= 0 {
		return nil
	}
	var instrument binanceInstrument
	if err := q.db.WithContext(ctx).Where("market = ? AND symbol = ?", request.Market, strings.ToUpper(request.Instrument)).First(&instrument).Error; err != nil {
		return errors.New("Binance instrument rules are unavailable")
	}
	if request.Quantity.LessThan(instrument.MinQuantity) || !request.Quantity.Mod(instrument.QuantityStep).IsZero() {
		return errors.New("Binance order quantity violates instrument rules")
	}
	return nil
}

func (q *binanceRuntime) privateJSON(ctx context.Context, market, method, path string, values url.Values, secrets sdk.SecretReader, proxyID int64, destination any) error {
	if secrets == nil {
		return errors.New("Binance credentials are unavailable")
	}
	apiKey, err := secrets.Read(ctx, "apiKey")
	if err != nil || len(apiKey) == 0 || len(apiKey) > 512 {
		return errors.New("Binance API key is unavailable")
	}
	secret, err := secrets.Read(ctx, "apiSecret")
	if err != nil || len(secret) == 0 || len(secret) > 512 {
		return errors.New("Binance API secret is unavailable")
	}
	values.Set("timestamp", strconv.FormatInt(time.Now().UTC().UnixMilli(), 10))
	values.Set("recvWindow", "5000")
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(values.Encode()))
	values.Set("signature", hex.EncodeToString(mac.Sum(nil)))
	base := "https://api.binance.com"
	if market == "usdm" && path != "/sapi/v1/account/apiRestrictions" {
		base = "https://fapi.binance.com"
	}
	var body io.Reader
	target := base + path
	if method == http.MethodGet || method == http.MethodDelete {
		target += "?" + values.Encode()
	} else {
		body = bytes.NewBufferString(values.Encode())
	}
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, method, target, body)
	if err != nil {
		return errors.New("create Binance private request failed")
	}
	req.Header.Set("X-MBX-APIKEY", string(apiKey))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	proxy, err := q.outboundProxyURL(callCtx, proxyID)
	if err != nil {
		return err
	}
	response, err := q.client.DoPrivateProxied(req, proxy)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxPrivateResponseBytes+1))
	if err != nil || len(raw) > maxPrivateResponseBytes {
		return errors.New("Binance private response exceeds limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Binance private response status %d", response.StatusCode)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(destination) != nil {
		return errors.New("decode Binance private response failed")
	}
	return nil
}

func validateOrderRequest(r sdk.OrderRequest) error {
	if !accountIDPattern.MatchString(r.Account) || (r.Market != "spot" && r.Market != "usdm") ||
		!instrumentPattern.MatchString(strings.ToUpper(r.Instrument)) || (r.Side != "buy" && r.Side != "sell") ||
		!clientOrderIDPattern.MatchString(r.ClientOrderID) || (r.Quantity.Sign() <= 0 && r.QuoteAmount.Sign() <= 0) ||
		r.Quantity.Sign() > 0 && r.QuoteAmount.Sign() > 0 || r.Market == "usdm" && r.QuoteAmount.Sign() > 0 ||
		(r.PositionEffect != "open" && r.PositionEffect != "reduce") {
		return errors.New("Binance order request is invalid")
	}
	return nil
}

func validateOrderQuery(r sdk.OrderQuery) error {
	if !accountIDPattern.MatchString(r.Account) || (r.Market != "spot" && r.Market != "usdm") ||
		!instrumentPattern.MatchString(strings.ToUpper(r.Instrument)) ||
		(r.OrderID == "" && r.ClientOrderID == "") || (r.OrderID != "" && r.ClientOrderID != "") {
		return errors.New("Binance order query is invalid")
	}
	return nil
}
func orderPath(market string) string {
	if market == "usdm" {
		return "/fapi/v1/order"
	}
	return "/api/v3/order"
}
func parseOrderResult(market string, payload map[string]any) (sdk.OrderResult, error) {
	text := func(key string) string {
		value := payload[key]
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
	}
	quantity, quantityErr := decimal.NewFromString(zeroIfEmpty(text("origQty")))
	executed, executedErr := decimal.NewFromString(zeroIfEmpty(text("executedQty")))
	average, averageErr := decimal.NewFromString(zeroIfEmpty(text("avgPrice")))
	if average.Sign() == 0 {
		quote, quoteErr := decimal.NewFromString(zeroIfEmpty(text("cummulativeQuoteQty")))
		if quoteErr != nil {
			return sdk.OrderResult{}, errors.New("Binance order response is invalid")
		}
		if executed.Sign() > 0 {
			average = quote.Div(executed)
		}
	}
	result := sdk.OrderResult{ProviderOrderID: text("orderId"), ClientOrderID: text("clientOrderId"), Status: strings.ToLower(text("status")), Market: market, Instrument: strings.ToUpper(text("symbol")), Side: strings.ToLower(text("side")), Quantity: quantity, Executed: executed, AveragePrice: average, UpdatedAt: time.Now().UTC()}
	if quantityErr != nil || executedErr != nil || averageErr != nil || result.ProviderOrderID == "" ||
		!clientOrderIDPattern.MatchString(result.ClientOrderID) || !instrumentPattern.MatchString(result.Instrument) ||
		(result.Side != "buy" && result.Side != "sell") || !validOrderStatus(result.Status) || quantity.Sign() < 0 ||
		executed.Sign() < 0 || average.Sign() < 0 || quantity.Sign() > 0 && executed.GreaterThan(quantity) {
		return sdk.OrderResult{}, errors.New("Binance order response is invalid")
	}
	return result, nil
}

func validOrderStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "new", "partially_filled", "filled", "canceled", "pending_cancel", "rejected", "expired", "expired_in_match":
		return true
	default:
		return false
	}
}
func (q *binanceRuntime) orderByClientID(ctx context.Context, id string) (tradingOrder, bool, error) {
	var row tradingOrder
	err := q.db.WithContext(ctx).Where("client_order_id = ?", id).First(&row).Error
	if err == nil {
		return row, true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, false, nil
	}
	return row, false, errors.New("load Binance order failed")
}

func matchesOrderIntent(row tradingOrder, mode, account, market, instrument, side, positionEffect, clientOrderID string, quantity, quoteAmount decimal.Decimal) bool {
	return row.Mode == mode && row.Account == account && row.Market == market && row.Instrument == instrument &&
		row.Side == side && row.PositionEffect == positionEffect && row.ClientOrderID == clientOrderID &&
		row.RequestQuantity.Equal(quantity) && row.RequestQuoteAmount.Equal(quoteAmount)
}
func marshalOrder(row tradingOrder) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"orderId": row.ID, "providerOrderId": row.ProviderOrderID, "clientOrderId": row.ClientOrderID, "status": row.Status, "market": row.Market, "instrument": row.Instrument, "side": row.Side, "quantity": row.Quantity.String(), "executed": row.Executed.String(), "averagePrice": row.AveragePrice.String(), "updatedAt": row.UpdatedAt.UTC().Format(time.RFC3339Nano)})
	return raw
}
func zeroIfEmpty(value string) string {
	if strings.TrimSpace(value) == "" {
		return "0"
	}
	return value
}
func orderIntentSchema() json.RawMessage {
	return json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"venue":{"type":"string","const":"binance"},"account":{"type":"string","pattern":"^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$"},"market":{"type":"string","enum":["spot","usdm"]},"instrument":{"type":"string","pattern":"^[A-Z0-9]{2,32}$"},"side":{"type":"string","enum":["buy","sell"]},"quantity":{"type":"string","x-coinsphere-decimal":true},"quoteAmount":{"type":"string","x-coinsphere-decimal":true},"positionEffect":{"type":"string","enum":["open","reduce"]},"clientOrderId":{"type":"string","pattern":"^[A-Za-z0-9._:/-]{1,36}$"},"referencePrice":{"type":"string","x-coinsphere-decimal":true},"quotedAt":{"type":"string","format":"date-time"}},"required":["venue","account","market","instrument","side","quantity","quoteAmount","positionEffect","clientOrderId","referencePrice","quotedAt"],"additionalProperties":false}`)
}
func orderResultSchema() json.RawMessage {
	return json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"orderId":{"type":"integer"},"providerOrderId":{"type":"string"},"clientOrderId":{"type":"string"},"status":{"type":"string"},"market":{"type":"string"},"instrument":{"type":"string"},"side":{"type":"string"},"quantity":{"type":"string","x-coinsphere-decimal":true},"executed":{"type":"string","x-coinsphere-decimal":true},"averagePrice":{"type":"string","x-coinsphere-decimal":true},"updatedAt":{"type":"string","format":"date-time"}},"required":["orderId","providerOrderId","clientOrderId","status","market","instrument","side","quantity","executed","averagePrice","updatedAt"],"additionalProperties":false}`)
}

var _ sdk.ExecutionProvider = executionProvider{}
var _ sdk.ActionHandler = liveExecuteAction{}
