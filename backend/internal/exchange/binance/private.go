package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/internal/marketdata"
	"github.com/shopspring/decimal"
)

const (
	defaultSpotTestnetURL = "https://testnet.binance.vision"
	defaultUSDMTestnetURL = "https://testnet.binancefuture.com"
	defaultRecvWindow     = 5 * time.Second
	defaultResponseLimit  = 1 << 20
)

type PrivateErrorKind string

const (
	PrivateErrorAuthentication PrivateErrorKind = "authentication"
	PrivateErrorPermission     PrivateErrorKind = "permission"
	PrivateErrorRateLimited    PrivateErrorKind = "rate_limited"
	PrivateErrorClockSkew      PrivateErrorKind = "clock_skew"
	PrivateErrorNotFound       PrivateErrorKind = "not_found"
	PrivateErrorRejected       PrivateErrorKind = "rejected"
	PrivateErrorUnavailable    PrivateErrorKind = "unavailable"
	PrivateErrorProtocol       PrivateErrorKind = "protocol"
)

// PrivateError deliberately excludes Binance response text and request data.
type PrivateError struct {
	Kind       PrivateErrorKind
	RetryAfter time.Duration
}

func (err *PrivateError) Error() string {
	if err == nil {
		return "binance private request failed"
	}
	return "binance private request failed: " + string(err.Kind)
}

type PrivateClientConfig struct {
	SpotBaseURL string
	USDMBaseURL string
	HTTPClient  *http.Client
	Now         func() time.Time
}

// PrivateClient signs Testnet requests without retaining account credentials.
type PrivateClient struct {
	baseURLs      map[marketdata.MarketType]url.URL
	http          *http.Client
	now           func() time.Time
	recvWindow    time.Duration
	responseLimit int64
}

type AccountBalance struct {
	Asset     string
	Total     decimal.Decimal
	Available decimal.Decimal
}

type AccountPosition struct {
	Symbol        string
	PositionSide  string
	Quantity      decimal.Decimal
	EntryPrice    decimal.Decimal
	UnrealizedPnL decimal.Decimal
}

type OpenOrder struct {
	Symbol           string
	ExchangeOrderID  int64
	ClientOrderID    string
	Side             string
	OrderType        string
	Status           string
	Price            decimal.Decimal
	OriginalQuantity decimal.Decimal
	ExecutedQuantity decimal.Decimal
	StopPrice        decimal.Decimal
	ClosePosition    bool
	ReduceOnly       bool
	WorkingType      string
}

type OrderResult struct {
	Symbol                  string
	ExchangeOrderID         int64
	ClientOrderID           string
	Side                    string
	OrderType               string
	Status                  string
	OriginalQuantity        decimal.Decimal
	ExecutedQuantity        decimal.Decimal
	CumulativeQuoteQuantity decimal.Decimal
	AveragePrice            decimal.Decimal
	StopPrice               decimal.Decimal
	ClosePosition           bool
	ReduceOnly              bool
	WorkingType             string
	ObservedAt              time.Time
}

type AccountSnapshot struct {
	CanTrade   bool
	Balances   []AccountBalance
	Positions  []AccountPosition
	OpenOrders []OpenOrder
	ObservedAt time.Time
}

func NewPrivateClient(config PrivateClientConfig) (*PrivateClient, error) {
	spotURL := config.SpotBaseURL
	if spotURL == "" {
		spotURL = defaultSpotTestnetURL
	}
	usdmURL := config.USDMBaseURL
	if usdmURL == "" {
		usdmURL = defaultUSDMTestnetURL
	}
	spot, err := privateBaseURL(spotURL, "testnet.binance.vision")
	if err != nil {
		return nil, err
	}
	usdm, err := privateBaseURL(usdmURL, "testnet.binancefuture.com")
	if err != nil {
		return nil, err
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &PrivateClient{
		baseURLs: map[marketdata.MarketType]url.URL{
			marketdata.MarketTypeSpot: spot,
			marketdata.MarketTypeUSDM: usdm,
		},
		http: &clientCopy, now: now, recvWindow: defaultRecvWindow, responseLimit: defaultResponseLimit,
	}, nil
}

// VerifyAccount proves that a signed account request succeeds. It does not infer trading permissions.
func (client *PrivateClient) VerifyAccount(
	ctx context.Context,
	marketType marketdata.MarketType,
	apiKey, apiSecret string,
) error {
	body, err := client.signedGET(ctx, marketType, accountPath(marketType), apiKey, apiSecret)
	if err != nil {
		return err
	}
	var account map[string]json.RawMessage
	if err := json.Unmarshal(body, &account); err != nil || account == nil {
		return &PrivateError{Kind: PrivateErrorProtocol}
	}
	return nil
}

// SnapshotAccount reads authoritative Testnet state without creating or changing orders.
func (client *PrivateClient) SnapshotAccount(
	ctx context.Context,
	marketType marketdata.MarketType,
	apiKey, apiSecret string,
) (AccountSnapshot, error) {
	accountBody, err := client.signedGET(ctx, marketType, accountPath(marketType), apiKey, apiSecret)
	if err != nil {
		return AccountSnapshot{}, err
	}
	openOrdersBody, err := client.signedGET(ctx, marketType, openOrdersPath(marketType), apiKey, apiSecret)
	if err != nil {
		return AccountSnapshot{}, err
	}
	snapshot, err := parseAccountSnapshot(marketType, accountBody, openOrdersBody)
	if err != nil {
		return AccountSnapshot{}, &PrivateError{Kind: PrivateErrorProtocol}
	}
	snapshot.ObservedAt = client.now().UTC()
	return snapshot, nil
}

// QueryOrder resolves a possibly submitted order before any retry decision.
func (client *PrivateClient) QueryOrder(
	ctx context.Context,
	marketType marketdata.MarketType,
	apiKey, apiSecret, symbol, clientOrderID string,
) (OrderResult, error) {
	symbol, clientOrderID, err := privateOrderIdentity(symbol, clientOrderID)
	if err != nil {
		return OrderResult{}, &PrivateError{Kind: PrivateErrorRejected}
	}
	body, err := client.signedRequest(ctx, marketType, http.MethodGet, orderPath(marketType), url.Values{
		"symbol":            {symbol},
		"origClientOrderId": {clientOrderID},
	}, apiKey, apiSecret)
	if err != nil {
		return OrderResult{}, err
	}
	return client.parseOrderResult(body)
}

// PlaceMarketOrder uses a caller-provided deterministic client order ID.
func (client *PrivateClient) PlaceMarketOrder(
	ctx context.Context,
	marketType marketdata.MarketType,
	apiKey, apiSecret, symbol, clientOrderID, side string,
	quantity decimal.Decimal,
	reduceOnly bool,
) (OrderResult, error) {
	symbol, clientOrderID, err := privateOrderIdentity(symbol, clientOrderID)
	if err != nil || (side != "buy" && side != "sell") || !quantity.IsPositive() {
		return OrderResult{}, &PrivateError{Kind: PrivateErrorRejected}
	}
	if _, err := marketdata.ParseDecimal(quantity.String()); err != nil {
		return OrderResult{}, &PrivateError{Kind: PrivateErrorRejected}
	}
	parameters := url.Values{
		"symbol":           {symbol},
		"side":             {strings.ToUpper(side)},
		"type":             {"MARKET"},
		"quantity":         {quantity.String()},
		"newClientOrderId": {clientOrderID},
		"newOrderRespType": {"RESULT"},
	}
	if marketType == marketdata.MarketTypeUSDM && reduceOnly {
		parameters.Set("reduceOnly", "true")
	}
	body, err := client.signedRequest(
		ctx, marketType, http.MethodPost, orderPath(marketType), parameters, apiKey, apiSecret,
	)
	if err != nil {
		return OrderResult{}, err
	}
	return client.parseOrderResult(body)
}

// PlaceProtectiveOrder creates a Spot quantity stop or a USD-M close-position stop.
func (client *PrivateClient) PlaceProtectiveOrder(
	ctx context.Context,
	marketType marketdata.MarketType,
	apiKey, apiSecret, symbol, clientOrderID, side string,
	quantity, stopPrice decimal.Decimal,
) (OrderResult, error) {
	symbol, clientOrderID, err := privateOrderIdentity(symbol, clientOrderID)
	if err != nil || (side != "buy" && side != "sell") || !stopPrice.IsPositive() {
		return OrderResult{}, &PrivateError{Kind: PrivateErrorRejected}
	}
	if _, err := marketdata.ParseDecimal(stopPrice.String()); err != nil {
		return OrderResult{}, &PrivateError{Kind: PrivateErrorRejected}
	}
	parameters := url.Values{
		"symbol":           {symbol},
		"side":             {strings.ToUpper(side)},
		"stopPrice":        {stopPrice.String()},
		"newClientOrderId": {clientOrderID},
		"newOrderRespType": {"RESULT"},
	}
	switch marketType {
	case marketdata.MarketTypeSpot:
		if !quantity.IsPositive() {
			return OrderResult{}, &PrivateError{Kind: PrivateErrorRejected}
		}
		if _, err := marketdata.ParseDecimal(quantity.String()); err != nil {
			return OrderResult{}, &PrivateError{Kind: PrivateErrorRejected}
		}
		parameters.Set("type", "STOP_LOSS")
		parameters.Set("quantity", quantity.String())
	case marketdata.MarketTypeUSDM:
		if !quantity.IsZero() {
			return OrderResult{}, &PrivateError{Kind: PrivateErrorRejected}
		}
		parameters.Set("type", "STOP_MARKET")
		parameters.Set("closePosition", "true")
		parameters.Set("workingType", "MARK_PRICE")
	default:
		return OrderResult{}, &PrivateError{Kind: PrivateErrorRejected}
	}
	body, err := client.signedRequest(
		ctx, marketType, http.MethodPost, orderPath(marketType), parameters, apiKey, apiSecret,
	)
	if err != nil {
		return OrderResult{}, err
	}
	return client.parseOrderResult(body)
}

// CancelOrder cancels one deterministic order by its original client order ID.
func (client *PrivateClient) CancelOrder(
	ctx context.Context,
	marketType marketdata.MarketType,
	apiKey, apiSecret, symbol, clientOrderID string,
) (OrderResult, error) {
	symbol, clientOrderID, err := privateOrderIdentity(symbol, clientOrderID)
	if err != nil {
		return OrderResult{}, &PrivateError{Kind: PrivateErrorRejected}
	}
	body, err := client.signedRequest(ctx, marketType, http.MethodDelete, orderPath(marketType), url.Values{
		"symbol":            {symbol},
		"origClientOrderId": {clientOrderID},
	}, apiKey, apiSecret)
	if err != nil {
		return OrderResult{}, err
	}
	return client.parseOrderResult(body)
}

func (client *PrivateClient) signedGET(
	ctx context.Context,
	marketType marketdata.MarketType,
	requestPath, apiKey, apiSecret string,
) ([]byte, error) {
	return client.signedRequest(ctx, marketType, http.MethodGet, requestPath, nil, apiKey, apiSecret)
}

func (client *PrivateClient) signedRequest(
	ctx context.Context,
	marketType marketdata.MarketType,
	method, requestPath string,
	parameters url.Values,
	apiKey, apiSecret string,
) ([]byte, error) {
	if client == nil || ctx == nil {
		return nil, &PrivateError{Kind: PrivateErrorRejected}
	}
	baseURL, ok := client.baseURLs[marketType]
	if !ok || apiKey == "" || apiSecret == "" {
		return nil, &PrivateError{Kind: PrivateErrorAuthentication}
	}
	endpoint := baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + requestPath
	endpoint.RawPath = ""
	query := make(url.Values, len(parameters)+3)
	for key, values := range parameters {
		query[key] = append([]string(nil), values...)
	}
	query.Set("recvWindow", strconv.FormatInt(client.recvWindow.Milliseconds(), 10))
	query.Set("timestamp", strconv.FormatInt(client.now().UTC().UnixMilli(), 10))
	mac := hmac.New(sha256.New, []byte(apiSecret))
	_, _ = mac.Write([]byte(query.Encode()))
	query.Set("signature", hex.EncodeToString(mac.Sum(nil)))
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), nil)
	if err != nil {
		return nil, &PrivateError{Kind: PrivateErrorRejected}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-MBX-APIKEY", apiKey)
	response, err := client.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &PrivateError{Kind: PrivateErrorUnavailable}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, client.responseLimit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &PrivateError{Kind: PrivateErrorUnavailable}
	}
	if int64(len(body)) > client.responseLimit {
		return nil, &PrivateError{Kind: PrivateErrorProtocol}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, classifyPrivateResponse(response, body)
	}
	return body, nil
}

func privateBaseURL(raw, testnetHost string) (url.URL, error) {
	parsed, err := url.Parse(raw)
	loopback := err == nil && privateLoopbackHost(parsed.Hostname())
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || (parsed.Scheme == "http" && !loopback) ||
		(!loopback && !strings.EqualFold(parsed.Hostname(), testnetHost)) {
		return url.URL{}, errors.New("invalid Binance private endpoint URL")
	}
	return *parsed, nil
}

func privateLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func accountPath(marketType marketdata.MarketType) string {
	if marketType == marketdata.MarketTypeSpot {
		return "/api/v3/account"
	}
	return "/fapi/v3/account"
}

func openOrdersPath(marketType marketdata.MarketType) string {
	if marketType == marketdata.MarketTypeSpot {
		return "/api/v3/openOrders"
	}
	return "/fapi/v1/openOrders"
}

func orderPath(marketType marketdata.MarketType) string {
	if marketType == marketdata.MarketTypeSpot {
		return "/api/v3/order"
	}
	return "/fapi/v1/order"
}

func privateOrderIdentity(symbol, clientOrderID string) (string, string, error) {
	symbol, err := privateToken(symbol, 64)
	if err != nil {
		return "", "", err
	}
	clientOrderID, err = privateText(clientOrderID, 36)
	if err != nil {
		return "", "", err
	}
	return symbol, clientOrderID, nil
}

func (client *PrivateClient) parseOrderResult(body []byte) (OrderResult, error) {
	var payload struct {
		Symbol                  string `json:"symbol"`
		OrderID                 int64  `json:"orderId"`
		ClientOrderID           string `json:"clientOrderId"`
		Side                    string `json:"side"`
		OrderType               string `json:"type"`
		Status                  string `json:"status"`
		OriginalQuantity        string `json:"origQty"`
		ExecutedQuantity        string `json:"executedQty"`
		CumulativeQuoteQuantity string `json:"cummulativeQuoteQty"`
		CumQuote                string `json:"cumQuote"`
		AveragePrice            string `json:"avgPrice"`
		StopPrice               string `json:"stopPrice"`
		ClosePosition           bool   `json:"closePosition"`
		ReduceOnly              bool   `json:"reduceOnly"`
		WorkingType             string `json:"workingType"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.OrderID <= 0 {
		return OrderResult{}, &PrivateError{Kind: PrivateErrorProtocol}
	}
	symbol, err := privateToken(payload.Symbol, 64)
	if err != nil {
		return OrderResult{}, &PrivateError{Kind: PrivateErrorProtocol}
	}
	clientOrderID, err := privateText(payload.ClientOrderID, 36)
	if err != nil {
		return OrderResult{}, &PrivateError{Kind: PrivateErrorProtocol}
	}
	side, err := privateEnum(payload.Side, 8)
	if err != nil || (side != "buy" && side != "sell") {
		return OrderResult{}, &PrivateError{Kind: PrivateErrorProtocol}
	}
	orderType, err := privateEnum(payload.OrderType, 32)
	if err != nil {
		return OrderResult{}, &PrivateError{Kind: PrivateErrorProtocol}
	}
	status, err := privateEnum(payload.Status, 32)
	if err != nil {
		return OrderResult{}, &PrivateError{Kind: PrivateErrorProtocol}
	}
	originalQuantity, err := privateDecimal(payload.OriginalQuantity, false)
	closePositionOrder := payload.ClosePosition && orderType == "stop_market"
	if err != nil || (!closePositionOrder && !originalQuantity.IsPositive()) ||
		(closePositionOrder && !originalQuantity.IsZero()) {
		return OrderResult{}, &PrivateError{Kind: PrivateErrorProtocol}
	}
	executedQuantity, err := privateDecimal(payload.ExecutedQuantity, false)
	if err != nil || (!closePositionOrder && executedQuantity.GreaterThan(originalQuantity)) {
		return OrderResult{}, &PrivateError{Kind: PrivateErrorProtocol}
	}
	cumulativeQuoteRaw := payload.CumulativeQuoteQuantity
	if cumulativeQuoteRaw == "" {
		cumulativeQuoteRaw = payload.CumQuote
	}
	if cumulativeQuoteRaw == "" {
		cumulativeQuoteRaw = "0"
	}
	cumulativeQuote, err := privateDecimal(cumulativeQuoteRaw, false)
	if err != nil {
		return OrderResult{}, &PrivateError{Kind: PrivateErrorProtocol}
	}
	averagePrice := decimal.Zero
	if payload.AveragePrice != "" {
		averagePrice, err = privateDecimal(payload.AveragePrice, false)
		if err != nil {
			return OrderResult{}, &PrivateError{Kind: PrivateErrorProtocol}
		}
	} else if executedQuantity.IsPositive() {
		averagePrice = cumulativeQuote.Div(executedQuantity)
		if _, err := marketdata.ParseDecimal(averagePrice.String()); err != nil {
			return OrderResult{}, &PrivateError{Kind: PrivateErrorProtocol}
		}
	}
	if (executedQuantity.IsZero() && (!cumulativeQuote.IsZero() || !averagePrice.IsZero())) ||
		(executedQuantity.IsPositive() && (!cumulativeQuote.IsPositive() || !averagePrice.IsPositive())) {
		return OrderResult{}, &PrivateError{Kind: PrivateErrorProtocol}
	}
	stopPrice := decimal.Zero
	if payload.StopPrice != "" {
		stopPrice, err = privateDecimal(payload.StopPrice, false)
		if err != nil {
			return OrderResult{}, &PrivateError{Kind: PrivateErrorProtocol}
		}
	}
	workingType := ""
	if payload.WorkingType != "" {
		workingType, err = privateEnum(payload.WorkingType, 24)
		if err != nil {
			return OrderResult{}, &PrivateError{Kind: PrivateErrorProtocol}
		}
	}
	return OrderResult{
		Symbol: symbol, ExchangeOrderID: payload.OrderID, ClientOrderID: clientOrderID,
		Side: side, OrderType: orderType, Status: status, OriginalQuantity: originalQuantity,
		ExecutedQuantity: executedQuantity, CumulativeQuoteQuantity: cumulativeQuote,
		AveragePrice: averagePrice, StopPrice: stopPrice, ClosePosition: payload.ClosePosition,
		ReduceOnly: payload.ReduceOnly, WorkingType: workingType, ObservedAt: client.now().UTC(),
	}, nil
}

func parseAccountSnapshot(marketType marketdata.MarketType, accountBody, openOrdersBody []byte) (AccountSnapshot, error) {
	snapshot := AccountSnapshot{Balances: []AccountBalance{}, Positions: []AccountPosition{}, OpenOrders: []OpenOrder{}}
	if marketType == marketdata.MarketTypeSpot {
		var payload struct {
			CanTrade bool `json:"canTrade"`
			Balances []struct {
				Asset  string `json:"asset"`
				Free   string `json:"free"`
				Locked string `json:"locked"`
			} `json:"balances"`
		}
		if err := json.Unmarshal(accountBody, &payload); err != nil || payload.Balances == nil {
			return AccountSnapshot{}, errors.New("invalid Spot account response")
		}
		snapshot.CanTrade = payload.CanTrade
		for _, balance := range payload.Balances {
			asset, err := privateToken(balance.Asset, 32)
			if err != nil {
				return AccountSnapshot{}, err
			}
			available, err := privateDecimal(balance.Free, false)
			if err != nil {
				return AccountSnapshot{}, err
			}
			locked, err := privateDecimal(balance.Locked, false)
			if err != nil {
				return AccountSnapshot{}, err
			}
			total := available.Add(locked)
			if _, err := marketdata.ParseDecimal(total.String()); err != nil {
				return AccountSnapshot{}, errors.New("invalid Spot balance total")
			}
			if !total.IsZero() {
				snapshot.Balances = append(snapshot.Balances, AccountBalance{Asset: asset, Total: total, Available: available})
			}
		}
	} else if marketType == marketdata.MarketTypeUSDM {
		var payload struct {
			CanTrade bool `json:"canTrade"`
			Assets   []struct {
				Asset            string `json:"asset"`
				WalletBalance    string `json:"walletBalance"`
				AvailableBalance string `json:"availableBalance"`
			} `json:"assets"`
			Positions []struct {
				Symbol           string `json:"symbol"`
				PositionSide     string `json:"positionSide"`
				PositionAmount   string `json:"positionAmt"`
				EntryPrice       string `json:"entryPrice"`
				UnrealizedProfit string `json:"unrealizedProfit"`
			} `json:"positions"`
		}
		if err := json.Unmarshal(accountBody, &payload); err != nil || payload.Assets == nil || payload.Positions == nil {
			return AccountSnapshot{}, errors.New("invalid USD-M account response")
		}
		snapshot.CanTrade = payload.CanTrade
		for _, balance := range payload.Assets {
			asset, err := privateToken(balance.Asset, 32)
			if err != nil {
				return AccountSnapshot{}, err
			}
			total, err := privateDecimal(balance.WalletBalance, false)
			if err != nil {
				return AccountSnapshot{}, err
			}
			available, err := privateDecimal(balance.AvailableBalance, true)
			if err != nil {
				return AccountSnapshot{}, err
			}
			if !total.IsZero() || !available.IsZero() {
				snapshot.Balances = append(snapshot.Balances, AccountBalance{Asset: asset, Total: total, Available: available})
			}
		}
		for _, position := range payload.Positions {
			symbol, err := privateToken(position.Symbol, 64)
			if err != nil {
				return AccountSnapshot{}, err
			}
			positionSide, err := privateEnum(position.PositionSide, 8)
			if err != nil {
				return AccountSnapshot{}, err
			}
			quantity, err := privateDecimal(position.PositionAmount, true)
			if err != nil {
				return AccountSnapshot{}, err
			}
			entryPrice, err := privateDecimal(position.EntryPrice, false)
			if err != nil {
				return AccountSnapshot{}, err
			}
			unrealizedPnL, err := privateDecimal(position.UnrealizedProfit, true)
			if err != nil {
				return AccountSnapshot{}, err
			}
			if !quantity.IsZero() || !unrealizedPnL.IsZero() || positionSide != "both" {
				snapshot.Positions = append(snapshot.Positions, AccountPosition{
					Symbol: symbol, PositionSide: positionSide, Quantity: quantity,
					EntryPrice: entryPrice, UnrealizedPnL: unrealizedPnL,
				})
			}
		}
	} else {
		return AccountSnapshot{}, errors.New("unsupported market")
	}

	var orders []struct {
		Symbol           string `json:"symbol"`
		OrderID          int64  `json:"orderId"`
		ClientOrderID    string `json:"clientOrderId"`
		Side             string `json:"side"`
		OrderType        string `json:"type"`
		Status           string `json:"status"`
		Price            string `json:"price"`
		OriginalQuantity string `json:"origQty"`
		ExecutedQuantity string `json:"executedQty"`
		StopPrice        string `json:"stopPrice"`
		ClosePosition    bool   `json:"closePosition"`
		ReduceOnly       bool   `json:"reduceOnly"`
		WorkingType      string `json:"workingType"`
	}
	if err := json.Unmarshal(openOrdersBody, &orders); err != nil || orders == nil {
		return AccountSnapshot{}, errors.New("invalid open orders response")
	}
	for _, order := range orders {
		symbol, err := privateToken(order.Symbol, 64)
		if err != nil || order.OrderID <= 0 {
			return AccountSnapshot{}, errors.New("invalid open order identity")
		}
		clientOrderID, err := privateText(order.ClientOrderID, 64)
		if err != nil {
			return AccountSnapshot{}, err
		}
		side, err := privateEnum(order.Side, 8)
		if err != nil {
			return AccountSnapshot{}, err
		}
		orderType, err := privateEnum(order.OrderType, 32)
		if err != nil {
			return AccountSnapshot{}, err
		}
		status, err := privateEnum(order.Status, 32)
		if err != nil {
			return AccountSnapshot{}, err
		}
		price, err := privateDecimal(order.Price, false)
		if err != nil {
			return AccountSnapshot{}, err
		}
		originalQuantity, err := privateDecimal(order.OriginalQuantity, false)
		if err != nil || (order.ClosePosition && !originalQuantity.IsZero()) ||
			(!order.ClosePosition && !originalQuantity.IsPositive()) {
			return AccountSnapshot{}, errors.New("invalid open order quantity")
		}
		executedQuantity, err := privateDecimal(order.ExecutedQuantity, false)
		if err != nil || executedQuantity.GreaterThan(originalQuantity) {
			return AccountSnapshot{}, errors.New("invalid open order executed quantity")
		}
		stopPrice, err := privateDecimal(order.StopPrice, false)
		if err != nil {
			return AccountSnapshot{}, err
		}
		workingType := ""
		if order.WorkingType != "" {
			workingType, err = privateEnum(order.WorkingType, 24)
			if err != nil {
				return AccountSnapshot{}, err
			}
		}
		snapshot.OpenOrders = append(snapshot.OpenOrders, OpenOrder{
			Symbol: symbol, ExchangeOrderID: order.OrderID, ClientOrderID: clientOrderID,
			Side: side, OrderType: orderType, Status: status, Price: price,
			OriginalQuantity: originalQuantity, ExecutedQuantity: executedQuantity, StopPrice: stopPrice,
			ClosePosition: order.ClosePosition, ReduceOnly: order.ReduceOnly, WorkingType: workingType,
		})
	}
	sort.Slice(snapshot.Balances, func(i, j int) bool { return snapshot.Balances[i].Asset < snapshot.Balances[j].Asset })
	sort.Slice(snapshot.Positions, func(i, j int) bool {
		if snapshot.Positions[i].Symbol == snapshot.Positions[j].Symbol {
			return snapshot.Positions[i].PositionSide < snapshot.Positions[j].PositionSide
		}
		return snapshot.Positions[i].Symbol < snapshot.Positions[j].Symbol
	})
	sort.Slice(snapshot.OpenOrders, func(i, j int) bool {
		if snapshot.OpenOrders[i].Symbol == snapshot.OpenOrders[j].Symbol {
			return snapshot.OpenOrders[i].ExchangeOrderID < snapshot.OpenOrders[j].ExchangeOrderID
		}
		return snapshot.OpenOrders[i].Symbol < snapshot.OpenOrders[j].Symbol
	})
	return snapshot, nil
}

func privateDecimal(value string, allowNegative bool) (decimal.Decimal, error) {
	if value == "" || value != strings.TrimSpace(value) {
		return decimal.Zero, errors.New("invalid private decimal")
	}
	negative := strings.HasPrefix(value, "-")
	if negative {
		if !allowNegative || len(value) == 1 {
			return decimal.Zero, errors.New("invalid private decimal")
		}
		value = value[1:]
	}
	parsed, err := marketdata.ParseDecimal(value)
	if err != nil {
		return decimal.Zero, errors.New("invalid private decimal")
	}
	if negative {
		parsed = parsed.Neg()
	}
	return parsed, nil
}

func privateToken(value string, limit int) (string, error) {
	value, err := privateText(value, limit)
	if err != nil {
		return "", err
	}
	for _, char := range value {
		if !((char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-') {
			return "", errors.New("invalid private token")
		}
	}
	return value, nil
}

func privateEnum(value string, limit int) (string, error) {
	value, err := privateToken(value, limit)
	if err != nil {
		return "", err
	}
	return strings.ToLower(value), nil
}

func privateText(value string, limit int) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || len(value) > limit {
		return "", errors.New("invalid private text")
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return "", errors.New("invalid private text")
		}
	}
	return value, nil
}

func classifyPrivateResponse(response *http.Response, body []byte) error {
	switch response.StatusCode {
	case http.StatusTooManyRequests, http.StatusTeapot:
		return &PrivateError{Kind: PrivateErrorRateLimited, RetryAfter: privateRetryAfter(response.Header.Get("Retry-After"))}
	case http.StatusRequestTimeout, http.StatusTooEarly:
		return &PrivateError{Kind: PrivateErrorUnavailable}
	default:
		if response.StatusCode >= http.StatusInternalServerError {
			return &PrivateError{Kind: PrivateErrorUnavailable}
		}
	}
	var payload struct {
		Code int64 `json:"code"`
	}
	_ = json.Unmarshal(body, &payload)
	switch payload.Code {
	case -2014, -1022:
		return &PrivateError{Kind: PrivateErrorAuthentication}
	case -2015:
		return &PrivateError{Kind: PrivateErrorPermission}
	case -1021:
		return &PrivateError{Kind: PrivateErrorClockSkew}
	case -2013:
		return &PrivateError{Kind: PrivateErrorNotFound}
	}
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return &PrivateError{Kind: PrivateErrorAuthentication}
	default:
		return &PrivateError{Kind: PrivateErrorRejected}
	}
}

func privateRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 && seconds <= int64((time.Duration(1<<63-1))/time.Second) {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	if delay := time.Until(when); delay > 0 {
		return delay
	}
	return 0
}
