package binance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/gorilla/websocket"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type privateAccountStreamConfig struct {
	Account               string `json:"account"`
	Market                string `json:"market"`
	ProxyID               int64  `json:"proxyId"`
	ReconciliationSeconds int    `json:"reconciliationSeconds"`
}

type privateOrderUpdate struct {
	ClientOrderID, ProviderOrderID, Instrument, Side, Status string
	Quantity, Executed, AveragePrice                         decimal.Decimal
	LastQuantity, LastPrice, Fee                             decimal.Decimal
	FeeAsset, TradeID                                        string
	UpdatedAt                                                time.Time
}

type privateAccountStream struct{ runtime *binanceRuntime }

func registerPrivateAccountStream(registrar sdk.Registrar, runtime *binanceRuntime) error {
	return registrar.Trigger(withNodeMeta(sdk.NodeDescriptor{
		Type: "official.binance.account_stream", Version: "1.0.0", Kind: sdk.NodeKindTrigger,
		ConfigSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"account":{"type":"string","title":"账户","pattern":"^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$"},"market":{"type":"string","title":"市场类型","enum":["spot","usdm"],"default":"spot"},"proxyId":{"type":"integer","title":"代理","minimum":0,"default":0,"x-coinsphere-proxy":true},"reconciliationSeconds":{"type":"integer","title":"REST 对账周期","minimum":30,"maximum":3600,"default":60},"apiKey":{"type":"string","title":"接口密钥","x-coinsphere-secret":true},"apiSecret":{"type":"string","title":"接口密钥","x-coinsphere-secret":true}},"required":["account","market","reconciliationSeconds","apiKey","apiSecret"],"additionalProperties":false}`),
		UISchema:     json.RawMessage(`{"ui:order":["account","market","proxyId","reconciliationSeconds","apiKey","apiSecret"]}`),
		InputSchema:  emptyObjectSchema,
		OutputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"account":{"type":"string"},"market":{"type":"string"},"instrument":{"type":"string"},"clientOrderId":{"type":"string"},"providerOrderId":{"type":"string"},"side":{"type":"string"},"status":{"type":"string"},"quantity":{"type":"string","x-coinsphere-decimal":true},"executed":{"type":"string","x-coinsphere-decimal":true},"averagePrice":{"type":"string","x-coinsphere-decimal":true},"updatedAt":{"type":"string","format":"date-time"}},"required":["account","market","instrument","clientOrderId","providerOrderId","side","status","quantity","executed","averagePrice","updatedAt"],"additionalProperties":false}`),
		Pool:         sdk.PoolStream, SideEffect: sdk.SideEffectData, State: sdk.StatePersistent,
	}, "Binance 私有账户同步", "通过 User Data Stream 和 REST 恢复并对账订单、成交、持仓与账户快照。", "开始", "#b45309", "refresh-cw"), privateAccountStream{runtime: runtime})
}

func (s privateAccountStream) Run(ctx context.Context, request sdk.TriggerRequest, emitter sdk.Emitter) error {
	config, err := parsePrivateAccountStreamConfig(request.Config)
	if err != nil {
		return err
	}
	if err := s.runtime.verifyNoWithdrawalPermission(ctx, config.Market, request.Secrets, config.ProxyID); err != nil {
		return err
	}
	backoff := time.Second
	for {
		if err := s.runtime.reconcilePrivateAccount(ctx, config, request.Secrets, true); err != nil {
			request.Logger.Warn("Binance REST reconciliation failed", "account", config.Account, "market", config.Market)
		} else if err := s.runtime.runPrivateAccountStream(ctx, config, request.Secrets, emitter, request.Logger); err == nil || ctx.Err() != nil {
			return ctx.Err()
		} else {
			request.Logger.Warn("Binance User Data Stream disconnected", "account", config.Account, "market", config.Market)
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func parsePrivateAccountStreamConfig(raw json.RawMessage) (privateAccountStreamConfig, error) {
	var config privateAccountStreamConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil {
		return config, errors.New("Binance private account configuration is invalid")
	}
	config.Account = strings.TrimSpace(config.Account)
	config.Market = strings.ToLower(strings.TrimSpace(config.Market))
	if !accountIDPattern.MatchString(config.Account) || config.Market != "spot" && config.Market != "usdm" || config.ProxyID < 0 || config.ReconciliationSeconds < 30 || config.ReconciliationSeconds > 3600 {
		return config, errors.New("Binance private account configuration is invalid")
	}
	return config, nil
}

func (q *binanceRuntime) runPrivateAccountStream(ctx context.Context, config privateAccountStreamConfig, secrets sdk.SecretReader, emitter sdk.Emitter, logger interface{ Info(string, ...any) }) error {
	listenKey, err := q.manageListenKey(ctx, config, secrets, http.MethodPost, "")
	if err != nil {
		return err
	}
	streamURL := "wss://stream.binance.com:9443/ws/" + url.PathEscape(listenKey)
	if config.Market == "usdm" {
		streamURL = "wss://fstream.binance.com/ws/" + url.PathEscape(listenKey)
	}
	target, _ := url.Parse(streamURL)
	proxyURL, err := q.outboundProxyURL(ctx, config.ProxyID)
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	if proxyURL == nil {
		if err := q.client.ValidatePrivateWebSocketURL(ctx, target); err != nil {
			return err
		}
		dialer.NetDialContext = q.client.DialContext
	} else {
		if err := q.client.ValidatePrivateProxiedWebSocketURL(target); err != nil {
			return err
		}
		dialer.Proxy = http.ProxyURL(proxyURL)
	}
	connection, response, err := dialer.DialContext(ctx, target.String(), http.Header{"User-Agent": []string{"CoinSphere-Binance/3.0"}})
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		return err
	}
	defer connection.Close()
	connection.SetReadLimit(maxPrivateResponseBytes)
	messages := make(chan json.RawMessage, 16)
	readErrors := make(chan error, 1)
	readCtx, cancelRead := context.WithCancel(ctx)
	defer cancelRead()
	go func() {
		for {
			_, raw, readErr := connection.ReadMessage()
			if readErr != nil {
				select {
				case readErrors <- readErr:
				default:
				}
				return
			}
			select {
			case messages <- json.RawMessage(raw):
			case <-readCtx.Done():
				return
			}
		}
	}()
	keepalive := time.NewTicker(25 * time.Minute)
	reconcile := time.NewTicker(time.Duration(config.ReconciliationSeconds) * time.Second)
	defer keepalive.Stop()
	defer reconcile.Stop()
	logger.Info("Binance User Data Stream connected", "account", config.Account, "market", config.Market)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readErrors:
			return err
		case raw := <-messages:
			if err := q.handlePrivateAccountEvent(ctx, config, raw, emitter); err != nil {
				return err
			}
		case <-keepalive.C:
			if _, err := q.manageListenKey(ctx, config, secrets, http.MethodPut, listenKey); err != nil {
				return err
			}
		case <-reconcile.C:
			if err := q.reconcilePrivateAccount(ctx, config, secrets, false); err != nil {
				return err
			}
		}
	}
}

func (q *binanceRuntime) manageListenKey(ctx context.Context, config privateAccountStreamConfig, secrets sdk.SecretReader, method, listenKey string) (string, error) {
	path := "/api/v3/userDataStream"
	if config.Market == "usdm" {
		path = "/fapi/v1/listenKey"
	}
	values := url.Values{}
	if listenKey != "" {
		values.Set("listenKey", listenKey)
	}
	var payload struct {
		ListenKey string `json:"listenKey"`
	}
	if err := q.privateAPIKeyJSON(ctx, config.Market, method, path, values, secrets, config.ProxyID, &payload); err != nil {
		return "", err
	}
	if method == http.MethodPost {
		listenKey = strings.TrimSpace(payload.ListenKey)
		if listenKey == "" || len(listenKey) > 512 {
			return "", errors.New("Binance listen key response is invalid")
		}
	}
	return listenKey, nil
}

func (q *binanceRuntime) privateAPIKeyJSON(ctx context.Context, market, method, path string, values url.Values, secrets sdk.SecretReader, proxyID int64, destination any) error {
	if secrets == nil {
		return errors.New("Binance credentials are unavailable")
	}
	apiKey, err := secrets.Read(ctx, "apiKey")
	if err != nil || len(apiKey) == 0 || len(apiKey) > 512 {
		return errors.New("Binance API key is unavailable")
	}
	base := "https://api.binance.com"
	if market == "usdm" {
		base = "https://fapi.binance.com"
	}
	target := base + path
	var body io.Reader
	if method == http.MethodGet || method == http.MethodDelete {
		target += "?" + values.Encode()
	} else {
		body = strings.NewReader(values.Encode())
	}
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, method, target, body)
	if err != nil {
		return errors.New("create Binance private stream request failed")
	}
	req.Header.Set("X-MBX-APIKEY", string(apiKey))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	proxyURL, err := q.outboundProxyURL(callCtx, proxyID)
	if err != nil {
		return err
	}
	response, err := q.client.DoPrivateProxied(req, proxyURL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxPrivateResponseBytes+1))
	if err != nil || len(raw) > maxPrivateResponseBytes {
		return errors.New("Binance private stream response exceeds limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Binance private stream response status %d", response.StatusCode)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(destination) != nil {
		return errors.New("decode Binance private stream response failed")
	}
	return nil
}

func (q *binanceRuntime) handlePrivateAccountEvent(ctx context.Context, config privateAccountStreamConfig, raw json.RawMessage, emitter sdk.Emitter) error {
	var envelope struct {
		Event string `json:"e"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return errors.New("Binance User Data Stream payload is invalid")
	}
	var update privateOrderUpdate
	var ok bool
	if config.Market == "spot" && envelope.Event == "executionReport" {
		update, ok = parseSpotOrderEvent(raw)
	} else if config.Market == "usdm" && envelope.Event == "ORDER_TRADE_UPDATE" {
		update, ok = parseUSDMOrderEvent(raw)
	} else {
		return nil
	}
	if !ok {
		return errors.New("Binance order event is invalid")
	}
	owned, err := q.persistPrivateOrderUpdate(ctx, config, update)
	if err != nil || !owned {
		return err
	}
	event := cloudevents.NewEvent()
	event.SetID(fmt.Sprintf("binance:%s:%s:%d:%s", config.Market, update.ClientOrderID, update.UpdatedAt.UnixMilli(), update.Status))
	event.SetSource("urn:coinsphere:plugin:official.binance")
	event.SetType("trade.order.updated")
	event.SetSubject("binance:" + config.Market + ":" + update.Instrument + ":" + update.ClientOrderID)
	event.SetTime(update.UpdatedAt.UTC())
	event.SetExtension("partitionkey", "binance:"+config.Account)
	if err := event.SetData(cloudevents.ApplicationJSON, privateOrderData(config.Account, config.Market, update)); err != nil {
		return errors.New("encode Binance order event failed")
	}
	return emitter.Emit(ctx, event)
}

func parseSpotOrderEvent(raw json.RawMessage) (privateOrderUpdate, bool) {
	var event struct {
		EventTime       int64  `json:"E"`
		Instrument      string `json:"s"`
		ClientOrderID   string `json:"c"`
		Side            string `json:"S"`
		Status          string `json:"X"`
		ProviderOrderID int64  `json:"i"`
		Quantity        string `json:"q"`
		Executed        string `json:"z"`
		QuoteExecuted   string `json:"Z"`
		LastQuantity    string `json:"l"`
		LastPrice       string `json:"L"`
		Fee             string `json:"n"`
		FeeAsset        string `json:"N"`
		TradeID         int64  `json:"t"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return privateOrderUpdate{}, false
	}
	update, ok := makePrivateOrderUpdate(event.ClientOrderID, strconv.FormatInt(event.ProviderOrderID, 10), event.Instrument, event.Side, event.Status, event.Quantity, event.Executed, event.QuoteExecuted, event.LastQuantity, event.LastPrice, event.Fee, event.FeeAsset, strconv.FormatInt(event.TradeID, 10), event.EventTime)
	if ok && update.Executed.Sign() > 0 {
		update.AveragePrice = update.AveragePrice.Div(update.Executed)
	}
	return update, ok
}

func parseUSDMOrderEvent(raw json.RawMessage) (privateOrderUpdate, bool) {
	var wire struct {
		EventTime int64 `json:"E"`
		Order     struct {
			Instrument      string `json:"s"`
			ClientOrderID   string `json:"c"`
			Side            string `json:"S"`
			Status          string `json:"X"`
			ProviderOrderID int64  `json:"i"`
			Quantity        string `json:"q"`
			Executed        string `json:"z"`
			AveragePrice    string `json:"ap"`
			LastQuantity    string `json:"l"`
			LastPrice       string `json:"L"`
			Fee             string `json:"n"`
			FeeAsset        string `json:"N"`
			TradeID         int64  `json:"t"`
		} `json:"o"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return privateOrderUpdate{}, false
	}
	return makePrivateOrderUpdate(wire.Order.ClientOrderID, strconv.FormatInt(wire.Order.ProviderOrderID, 10), wire.Order.Instrument, wire.Order.Side, wire.Order.Status, wire.Order.Quantity, wire.Order.Executed, wire.Order.AveragePrice, wire.Order.LastQuantity, wire.Order.LastPrice, wire.Order.Fee, wire.Order.FeeAsset, strconv.FormatInt(wire.Order.TradeID, 10), wire.EventTime)
}

func makePrivateOrderUpdate(clientID, orderID, instrument, side, status, quantityText, executedText, quoteOrAverageText, lastQuantityText, lastPriceText, feeText, feeAsset, tradeID string, eventMillis int64) (privateOrderUpdate, bool) {
	quantity, e1 := decimal.NewFromString(zeroIfEmpty(quantityText))
	executed, e2 := decimal.NewFromString(zeroIfEmpty(executedText))
	average, e3 := decimal.NewFromString(zeroIfEmpty(quoteOrAverageText))
	lastQuantity, e4 := decimal.NewFromString(zeroIfEmpty(lastQuantityText))
	lastPrice, e5 := decimal.NewFromString(zeroIfEmpty(lastPriceText))
	fee, e6 := decimal.NewFromString(zeroIfEmpty(feeText))
	validSide := side == "BUY" || side == "SELL"
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || e6 != nil ||
		!clientOrderIDPattern.MatchString(clientID) || orderID == "" || !instrumentPattern.MatchString(strings.ToUpper(instrument)) ||
		!validSide || !validOrderStatus(status) || quantity.Sign() < 0 || executed.Sign() < 0 || average.Sign() < 0 ||
		lastQuantity.Sign() < 0 || lastPrice.Sign() < 0 || fee.Sign() < 0 || eventMillis <= 0 ||
		lastQuantity.Sign() > 0 && (lastPrice.Sign() <= 0 || tradeID == "-1") || fee.Sign() > 0 && strings.TrimSpace(feeAsset) == "" {
		return privateOrderUpdate{}, false
	}
	return privateOrderUpdate{ClientOrderID: clientID, ProviderOrderID: orderID, Instrument: strings.ToUpper(instrument), Side: strings.ToLower(side), Status: strings.ToLower(status), Quantity: quantity, Executed: executed, AveragePrice: average, LastQuantity: lastQuantity, LastPrice: lastPrice, Fee: fee, FeeAsset: feeAsset, TradeID: tradeID, UpdatedAt: time.UnixMilli(eventMillis).UTC()}, true
}

func (q *binanceRuntime) persistPrivateOrderUpdate(ctx context.Context, config privateAccountStreamConfig, update privateOrderUpdate) (bool, error) {
	var order tradingOrder
	err := q.db.WithContext(ctx).Where("account = ? AND market = ? AND client_order_id = ? AND mode = 'live'", config.Account, config.Market, update.ClientOrderID).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("load Binance live order failed")
	}
	if order.Instrument != update.Instrument || order.Side != update.Side || order.ProviderOrderID != "" && order.ProviderOrderID != update.ProviderOrderID {
		return false, errors.New("Binance live order event identity does not match")
	}
	if err := q.db.WithContext(ctx).Model(&order).Updates(map[string]any{"provider_order_id": update.ProviderOrderID, "quantity": update.Quantity, "executed": update.Executed, "average_price": update.AveragePrice, "notional": update.Executed.Mul(update.AveragePrice), "status": update.Status, "updated_at": update.UpdatedAt}).Error; err != nil {
		return false, errors.New("update Binance live order failed")
	}
	if update.TradeID != "-1" && update.LastQuantity.Sign() > 0 {
		if err := q.persistLiveFill(ctx, order, liveTradeOperationKey(order.Account, config.Market, update.Instrument, update.TradeID), update.LastQuantity, update.LastPrice, update.Fee, update.FeeAsset, update.UpdatedAt); err != nil {
			return false, err
		}
	}
	slog.Info("Binance live order synchronized", "component", "plugin.binance", "account", config.Account, "market", config.Market, "instrument", update.Instrument, "client_order_id", update.ClientOrderID, "provider_order_id", update.ProviderOrderID, "status", update.Status)
	return true, nil
}

func (q *binanceRuntime) persistLiveFill(ctx context.Context, order tradingOrder, tradeID string, quantity, price, fee decimal.Decimal, feeAsset string, filledAt time.Time) error {
	return q.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, "binance-live-position:"+order.Account+":"+order.Market+":"+order.Instrument).Error; err != nil {
			return errors.New("lock Binance live position failed")
		}
		fill := tradingFill{OrderID: order.ID, ProviderTradeID: tradeID, Quantity: quantity, Price: price, Fee: fee, FeeAsset: feeAsset, FilledAt: filledAt.UTC()}
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "provider_trade_id"}}, DoNothing: true}).Create(&fill)
		if result.Error != nil {
			return errors.New("persist Binance live fill failed")
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if err := tx.Create(&tradingFee{FillID: fill.ID, Amount: fee, Asset: feeAsset, CreatedAt: filledAt.UTC()}).Error; err != nil {
			return errors.New("persist Binance live fee failed")
		}
		var position tradingPosition
		positionErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account = ? AND mode = 'live' AND market = ? AND instrument = ?", order.Account, order.Market, order.Instrument).First(&position).Error
		if positionErr != nil && !errors.Is(positionErr, gorm.ErrRecordNotFound) {
			return errors.New("load Binance live position failed")
		}
		delta := quantity
		if order.Side == "sell" {
			delta = delta.Neg()
		}
		position.Account, position.Mode, position.Market, position.Instrument = order.Account, "live", order.Market, order.Instrument
		position.Quantity, position.AveragePrice = nextLivePosition(position.Quantity, position.AveragePrice, delta, price)
		position.UpdatedAt = filledAt.UTC()
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "account"}, {Name: "mode"}, {Name: "market"}, {Name: "instrument"}}, DoUpdates: clause.AssignmentColumns([]string{"quantity", "average_price", "updated_at"})}).Create(&position).Error
	})
}

func nextLivePosition(quantity, average, delta, price decimal.Decimal) (decimal.Decimal, decimal.Decimal) {
	next := quantity.Add(delta)
	if next.IsZero() {
		return decimal.Zero, decimal.Zero
	}
	if quantity.IsZero() || quantity.Sign() == delta.Sign() {
		return next, quantity.Abs().Mul(average).Add(delta.Abs().Mul(price)).Div(next.Abs())
	}
	if next.Sign() != quantity.Sign() {
		return next, price
	}
	return next, average
}

func (q *binanceRuntime) reconcilePrivateAccount(ctx context.Context, config privateAccountStreamConfig, secrets sdk.SecretReader, includeRecent bool) error {
	var orders []tradingOrder
	query := q.db.WithContext(ctx).Where("account = ? AND market = ? AND mode = 'live' AND status NOT IN ?", config.Account, config.Market, []string{"filled", "canceled", "rejected", "expired"})
	if includeRecent {
		query = q.db.WithContext(ctx).Where("account = ? AND market = ? AND mode = 'live' AND (status NOT IN ? OR updated_at >= ?)", config.Account, config.Market, []string{"filled", "canceled", "rejected", "expired"}, time.Now().UTC().Add(-24*time.Hour))
	}
	if err := query.Order("updated_at DESC").Limit(500).Find(&orders).Error; err != nil {
		return errors.New("load Binance live reconciliation orders failed")
	}
	provider := executionProvider{runtime: q}
	for _, order := range orders {
		result, err := provider.GetOrder(ctx, sdk.OrderQuery{Account: config.Account, Market: config.Market, Instrument: order.Instrument, ClientOrderID: order.ClientOrderID, Secrets: secrets, ProxyID: config.ProxyID})
		if err != nil {
			continue
		}
		if err := q.updateReconciledOrder(ctx, order, result); err != nil {
			return err
		}
		order.ProviderOrderID, order.Status = result.ProviderOrderID, result.Status
		if err := q.reconcileOrderTrades(ctx, order, config, secrets); err != nil {
			return err
		}
	}
	return q.reconcileAccountState(ctx, config, secrets, "")
}

func (q *binanceRuntime) updateReconciledOrder(ctx context.Context, order tradingOrder, result sdk.OrderResult) error {
	if result.ClientOrderID != order.ClientOrderID || result.Market != order.Market || result.Instrument != order.Instrument ||
		result.Side != order.Side || result.ProviderOrderID == "" || order.ProviderOrderID != "" && result.ProviderOrderID != order.ProviderOrderID ||
		!validOrderStatus(result.Status) || result.Quantity.Sign() < 0 || result.Executed.Sign() < 0 || result.AveragePrice.Sign() < 0 ||
		result.Quantity.Sign() > 0 && result.Executed.GreaterThan(result.Quantity) {
		return errors.New("Binance reconciled order identity or amounts are invalid")
	}
	return q.db.WithContext(ctx).Model(&order).Updates(map[string]any{"provider_order_id": result.ProviderOrderID, "quantity": result.Quantity, "executed": result.Executed, "average_price": result.AveragePrice, "notional": result.Executed.Mul(result.AveragePrice), "status": result.Status, "updated_at": result.UpdatedAt.UTC()}).Error
}

func (q *binanceRuntime) reconcileOrderTrades(ctx context.Context, order tradingOrder, config privateAccountStreamConfig, secrets sdk.SecretReader) error {
	if order.ProviderOrderID == "" {
		return nil
	}
	path := "/api/v3/myTrades"
	if config.Market == "usdm" {
		path = "/fapi/v1/userTrades"
	}
	values := url.Values{"symbol": {order.Instrument}, "orderId": {order.ProviderOrderID}, "limit": {"1000"}}
	var trades []struct {
		ID       int64  `json:"id"`
		OrderID  int64  `json:"orderId"`
		Price    string `json:"price"`
		Quantity string `json:"qty"`
		Fee      string `json:"commission"`
		FeeAsset string `json:"commissionAsset"`
		Time     int64  `json:"time"`
		Buyer    bool   `json:"isBuyer"`
		Side     string `json:"side"`
	}
	if err := q.privateJSON(ctx, config.Market, http.MethodGet, path, values, secrets, config.ProxyID, &trades); err != nil {
		return err
	}
	for _, trade := range trades {
		quantity, e1 := decimal.NewFromString(trade.Quantity)
		price, e2 := decimal.NewFromString(trade.Price)
		fee, e3 := decimal.NewFromString(zeroIfEmpty(trade.Fee))
		if e1 != nil || e2 != nil || e3 != nil || quantity.Sign() <= 0 || price.Sign() <= 0 || fee.Sign() < 0 ||
			trade.ID < 0 || trade.Time <= 0 || fee.Sign() > 0 && strings.TrimSpace(trade.FeeAsset) == "" {
			return errors.New("Binance trade reconciliation payload is invalid")
		}
		tradeOrder := order
		if config.Market == "spot" {
			if trade.Buyer {
				tradeOrder.Side = "buy"
			} else {
				tradeOrder.Side = "sell"
			}
		} else {
			tradeOrder.Side = strings.ToLower(trade.Side)
			if tradeOrder.Side != "buy" && tradeOrder.Side != "sell" {
				return errors.New("Binance trade reconciliation side is invalid")
			}
		}
		if err := q.persistLiveFill(ctx, tradeOrder, liveTradeOperationKey(order.Account, config.Market, order.Instrument, strconv.FormatInt(trade.ID, 10)), quantity, price, fee, trade.FeeAsset, time.UnixMilli(trade.Time).UTC()); err != nil {
			return err
		}
	}
	return nil
}

func liveTradeOperationKey(account, market, instrument, tradeID string) string {
	digest := sha256.Sum256([]byte(account + "\x00" + market + "\x00" + instrument + "\x00" + tradeID))
	return fmt.Sprintf("live-%x", digest[:])
}

func (q *binanceRuntime) reconcileAccountState(ctx context.Context, config privateAccountStreamConfig, secrets sdk.SecretReader, targetInstrument string) error {
	now := time.Now().UTC()
	if config.Market == "usdm" {
		var account struct {
			Equity    string `json:"totalMarginBalance"`
			Available string `json:"availableBalance"`
			Positions []struct {
				Instrument string `json:"symbol"`
				Quantity   string `json:"positionAmt"`
				EntryPrice string `json:"entryPrice"`
			} `json:"positions"`
		}
		if err := q.privateJSON(ctx, config.Market, http.MethodGet, "/fapi/v2/account", url.Values{}, secrets, config.ProxyID, &account); err != nil {
			return err
		}
		equity, e1 := decimal.NewFromString(account.Equity)
		available, e2 := decimal.NewFromString(account.Available)
		if e1 != nil || e2 != nil {
			return errors.New("Binance USD-M account payload is invalid")
		}
		for _, item := range account.Positions {
			quantity, qErr := decimal.NewFromString(item.Quantity)
			price, pErr := decimal.NewFromString(item.EntryPrice)
			if qErr != nil || pErr != nil {
				return errors.New("Binance USD-M position payload is invalid")
			}
			position := tradingPosition{Account: config.Account, Mode: "live", Market: config.Market, Instrument: strings.ToUpper(item.Instrument), Quantity: quantity, AveragePrice: price, UpdatedAt: now}
			if err := q.upsertLivePosition(ctx, position); err != nil {
				return err
			}
		}
		return q.db.WithContext(ctx).Create(&accountSnapshot{Account: config.Account, Market: config.Market, Asset: "USD", Equity: equity, Available: available, CapturedAt: now}).Error
	}
	var account struct {
		Balances []struct {
			Asset  string `json:"asset"`
			Free   string `json:"free"`
			Locked string `json:"locked"`
		} `json:"balances"`
	}
	if err := q.privateJSON(ctx, config.Market, http.MethodGet, "/api/v3/account", url.Values{}, secrets, config.ProxyID, &account); err != nil {
		return err
	}
	balances := map[string]decimal.Decimal{}
	for _, item := range account.Balances {
		free, e1 := decimal.NewFromString(item.Free)
		locked, e2 := decimal.NewFromString(item.Locked)
		if e1 != nil || e2 != nil {
			return errors.New("Binance Spot account payload is invalid")
		}
		balances[strings.ToUpper(item.Asset)] = free.Add(locked)
	}
	if targetInstrument != "" {
		var instrument binanceInstrument
		if err := q.db.WithContext(ctx).Where("market = 'spot' AND symbol = ?", strings.ToUpper(targetInstrument)).First(&instrument).Error; err != nil {
			return errors.New("load Binance Spot target instrument failed")
		}
		var position tradingPosition
		positionErr := q.db.WithContext(ctx).Where("account = ? AND mode = 'live' AND market = 'spot' AND instrument = ?", config.Account, instrument.Symbol).First(&position).Error
		if positionErr != nil && !errors.Is(positionErr, gorm.ErrRecordNotFound) {
			return errors.New("load Binance Spot target position failed")
		}
		position.Account, position.Mode, position.Market, position.Instrument = config.Account, "live", "spot", instrument.Symbol
		position.Quantity, position.UpdatedAt = balances[instrument.BaseAsset], now
		if err := q.upsertLivePosition(ctx, position); err != nil {
			return err
		}
	}
	var positions []tradingPosition
	if err := q.db.WithContext(ctx).Where("account = ? AND mode = 'live' AND market = 'spot'", config.Account).Find(&positions).Error; err != nil {
		return errors.New("load Binance Spot positions failed")
	}
	equities := make(map[string]decimal.Decimal, len(balances))
	for asset, balance := range balances {
		if !balance.IsZero() {
			equities[asset] = balance
		}
	}
	for _, position := range positions {
		var instrument binanceInstrument
		if err := q.db.WithContext(ctx).Where("market = 'spot' AND symbol = ?", position.Instrument).First(&instrument).Error; err != nil {
			return errors.New("load Binance Spot position instrument failed")
		}
		position.Quantity = balances[instrument.BaseAsset]
		position.UpdatedAt = now
		if err := q.upsertLivePosition(ctx, position); err != nil {
			return err
		}
		quote, err := (marketDataProvider{runtime: q}).Quote(ctx, sdk.QuoteQuery{Market: "spot", Instrument: position.Instrument, ProxyID: config.ProxyID})
		if err != nil || quote.Price.Sign() <= 0 {
			return errors.New("load Binance Spot position quote failed")
		}
		equities[instrument.QuoteAsset] = equities[instrument.QuoteAsset].Add(position.Quantity.Mul(quote.Price))
	}
	if len(equities) == 0 {
		equities["USDT"] = decimal.Zero
	}
	for asset, equity := range equities {
		if err := q.db.WithContext(ctx).Create(&accountSnapshot{Account: config.Account, Market: config.Market, Asset: asset, Equity: equity, Available: balances[asset], CapturedAt: now}).Error; err != nil {
			return errors.New("persist Binance Spot account snapshot failed")
		}
	}
	return nil
}

func (q *binanceRuntime) upsertLivePosition(ctx context.Context, position tradingPosition) error {
	return q.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "account"}, {Name: "mode"}, {Name: "market"}, {Name: "instrument"}}, DoUpdates: clause.AssignmentColumns([]string{"quantity", "average_price", "updated_at"})}).Create(&position).Error
}

func privateOrderData(account, market string, update privateOrderUpdate) map[string]any {
	return map[string]any{"account": account, "market": market, "instrument": update.Instrument, "clientOrderId": update.ClientOrderID, "providerOrderId": update.ProviderOrderID, "side": update.Side, "status": update.Status, "quantity": update.Quantity.String(), "executed": update.Executed.String(), "averagePrice": update.AveragePrice.String(), "updatedAt": update.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

var _ sdk.TriggerHandler = privateAccountStream{}
