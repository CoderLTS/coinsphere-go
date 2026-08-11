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
	defaultSpotLiveURL    = "https://api.binance.com"
	defaultUSDMLiveURL    = "https://fapi.binance.com"
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
	Environment string
	Market      marketdata.MarketType
	SpotBaseURL string
	USDMBaseURL string
	HTTPClient  *http.Client
	Now         func() time.Time
}

// PrivateClient signs requests for one explicitly selected environment without retaining credentials.
type PrivateClient struct {
	baseURLs      map[marketdata.MarketType]url.URL
	environment   string
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
	Symbol                   string
	PositionSide             string
	Quantity                 decimal.Decimal
	EntryPrice               decimal.Decimal
	MarkPrice                decimal.Decimal
	LiquidationPrice         decimal.Decimal
	LiquidationDistanceRatio decimal.Decimal
	UnrealizedPnL            decimal.Decimal
	Leverage                 int
	Isolated                 bool
}

type OpenOrder struct {
	Symbol                  string
	ExchangeOrderID         int64
	ClientOrderID           string
	Side                    string
	OrderType               string
	Status                  string
	Price                   decimal.Decimal
	OriginalQuantity        decimal.Decimal
	ExecutedQuantity        decimal.Decimal
	CumulativeQuoteQuantity decimal.Decimal
	AveragePrice            decimal.Decimal
	StopPrice               decimal.Decimal
	ClosePosition           bool
	ReduceOnly              bool
	WorkingType             string
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

// AccountTrade is one exchange-authoritative fill. The client normalizes Spot
// and USD-M responses to the same Decimal/UTC shape before they cross the
// private-protocol boundary.
type AccountTrade struct {
	Symbol          string
	ExchangeTradeID int64
	ExchangeOrderID int64
	Side            string
	PositionSide    string
	Quantity        decimal.Decimal
	Price           decimal.Decimal
	QuoteQuantity   decimal.Decimal
	Commission      decimal.Decimal
	CommissionAsset string
	RealizedPnL     decimal.Decimal
	Buyer           bool
	Maker           bool
	OccurredAt      time.Time
}

// FundingIncome is a USD-M funding fee fact. Other income types are rejected
// by QueryFundingIncome and never enter the reconciliation layer.
type FundingIncome struct {
	TransactionID string
	Symbol        string
	IncomeType    string
	Asset         string
	Amount        decimal.Decimal
	OccurredAt    time.Time
}

type AccountSnapshot struct {
	CanTrade   bool
	Balances   []AccountBalance
	Positions  []AccountPosition
	OpenOrders []OpenOrder
	ObservedAt time.Time
}

func NewPrivateClient(config PrivateClientConfig) (*PrivateClient, error) {
	environment := strings.TrimSpace(config.Environment)
	if environment == "" {
		environment = "testnet"
	}
	if environment != "testnet" && environment != "live" {
		return nil, errors.New("private client environment must be testnet or live")
	}
	if environment == "live" && config.Market != marketdata.MarketTypeSpot && config.Market != marketdata.MarketTypeUSDM {
		return nil, errors.New("live private client market must be spot or usd_m")
	}
	baseURLs := make(map[marketdata.MarketType]url.URL, 2)
	markets := []marketdata.MarketType{marketdata.MarketTypeSpot, marketdata.MarketTypeUSDM}
	if environment == "live" {
		markets = []marketdata.MarketType{config.Market}
	}
	for _, marketType := range markets {
		raw, host := config.SpotBaseURL, "testnet.binance.vision"
		if marketType == marketdata.MarketTypeUSDM {
			raw, host = config.USDMBaseURL, "testnet.binancefuture.com"
		}
		if environment == "live" {
			if marketType == marketdata.MarketTypeSpot {
				host = "api.binance.com"
			} else {
				host = "fapi.binance.com"
			}
		}
		if raw == "" {
			switch {
			case environment == "live" && marketType == marketdata.MarketTypeSpot:
				raw = defaultSpotLiveURL
			case environment == "live":
				raw = defaultUSDMLiveURL
			case marketType == marketdata.MarketTypeSpot:
				raw = defaultSpotTestnetURL
			default:
				raw = defaultUSDMTestnetURL
			}
		}
		endpoint, err := privateBaseURL(raw, host)
		if err != nil {
			return nil, err
		}
		baseURLs[marketType] = endpoint
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
		baseURLs: baseURLs, environment: environment,
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

// SnapshotAccount reads authoritative private state without creating or changing orders.
func (client *PrivateClient) SnapshotAccount(
	ctx context.Context,
	marketType marketdata.MarketType,
	apiKey, apiSecret string,
	whitelistedSymbols ...string,
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
	if client.environment == "live" && marketType == marketdata.MarketTypeUSDM && len(whitelistedSymbols) > 0 {
		positionRiskBody, err := client.signedGET(ctx, marketType, "/fapi/v3/positionRisk", apiKey, apiSecret)
		if err != nil {
			return AccountSnapshot{}, err
		}
		snapshot.Positions, err = parseUSDMPositionRisk(positionRiskBody, whitelistedSymbols)
		if err != nil {
			return AccountSnapshot{}, &PrivateError{Kind: PrivateErrorProtocol}
		}
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

// QueryOrderTrades reads every known fill for one deterministic exchange order.
// A full response page is treated as protocol uncertainty because silently
// truncating fills would make the local position and fee projection unsafe.
func (client *PrivateClient) QueryOrderTrades(
	ctx context.Context,
	marketType marketdata.MarketType,
	apiKey, apiSecret, symbol string,
	orderID int64,
) ([]AccountTrade, error) {
	if marketType != marketdata.MarketTypeSpot && marketType != marketdata.MarketTypeUSDM {
		return nil, &PrivateError{Kind: PrivateErrorRejected}
	}
	if _, err := privateToken(symbol, 64); err != nil || orderID <= 0 {
		return nil, &PrivateError{Kind: PrivateErrorRejected}
	}
	parameters := url.Values{
		"symbol":  {symbol},
		"orderId": {strconv.FormatInt(orderID, 10)},
		"limit":   {"1000"},
	}
	body, err := client.signedRequest(ctx, marketType, http.MethodGet, tradesPath(marketType), parameters, apiKey, apiSecret)
	if err != nil {
		return nil, err
	}
	trades, err := parseAccountTrades(marketType, body)
	if err != nil {
		return nil, &PrivateError{Kind: PrivateErrorProtocol}
	}
	if len(trades) >= 1000 {
		return nil, &PrivateError{Kind: PrivateErrorProtocol}
	}
	return trades, nil
}

// QueryFundingIncome reads the bounded USD-M funding fee window. Binance does
// not expose a stable cursor for this endpoint; callers overlap windows and
// deduplicate by transaction ID. A full page is therefore unsafe to accept.
func (client *PrivateClient) QueryFundingIncome(
	ctx context.Context,
	apiKey, apiSecret, symbol string,
	startTime, endTime time.Time,
) ([]FundingIncome, error) {
	startTime = startTime.UTC()
	endTime = endTime.UTC()
	if startTime.IsZero() || endTime.IsZero() || !endTime.After(startTime) ||
		endTime.Sub(startTime) > 7*24*time.Hour {
		return nil, &PrivateError{Kind: PrivateErrorRejected}
	}
	parameters := url.Values{
		"incomeType": {"FUNDING_FEE"},
		"startTime":  {strconv.FormatInt(startTime.UnixMilli(), 10)},
		"endTime":    {strconv.FormatInt(endTime.UnixMilli(), 10)},
		"limit":      {"1000"},
	}
	if symbol != "" {
		if _, err := privateToken(symbol, 64); err != nil {
			return nil, &PrivateError{Kind: PrivateErrorRejected}
		}
		parameters.Set("symbol", symbol)
	}
	body, err := client.signedRequest(ctx, marketdata.MarketTypeUSDM, http.MethodGet, incomePath(), parameters, apiKey, apiSecret)
	if err != nil {
		return nil, err
	}
	income, err := parseFundingIncome(body)
	if err != nil {
		return nil, &PrivateError{Kind: PrivateErrorProtocol}
	}
	if len(income) >= 1000 {
		return nil, &PrivateError{Kind: PrivateErrorProtocol}
	}
	return income, nil
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
		(!loopback && (testnetHost == "" || !strings.EqualFold(parsed.Hostname(), testnetHost))) {
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

func tradesPath(marketType marketdata.MarketType) string {
	if marketType == marketdata.MarketTypeSpot {
		return "/api/v3/myTrades"
	}
	return "/fapi/v1/userTrades"
}

func incomePath() string { return "/fapi/v1/income" }

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

type spotTradePayload struct {
	Symbol          string          `json:"symbol"`
	TradeID         json.RawMessage `json:"id"`
	OrderID         json.RawMessage `json:"orderId"`
	Price           string          `json:"price"`
	Quantity        string          `json:"qty"`
	QuoteQuantity   string          `json:"quoteQty"`
	Commission      string          `json:"commission"`
	CommissionAsset string          `json:"commissionAsset"`
	OccurredAt      json.RawMessage `json:"time"`
	IsBuyer         *bool           `json:"isBuyer"`
	IsMaker         *bool           `json:"isMaker"`
}

type usdmTradePayload struct {
	Symbol          string          `json:"symbol"`
	TradeID         json.RawMessage `json:"id"`
	OrderID         json.RawMessage `json:"orderId"`
	Side            string          `json:"side"`
	PositionSide    string          `json:"positionSide"`
	Price           string          `json:"price"`
	Quantity        string          `json:"qty"`
	QuoteQuantity   string          `json:"quoteQty"`
	Commission      string          `json:"commission"`
	CommissionAsset string          `json:"commissionAsset"`
	RealizedPnL     string          `json:"realizedPnl"`
	Buyer           *bool           `json:"buyer"`
	Maker           *bool           `json:"maker"`
	OccurredAt      json.RawMessage `json:"time"`
}

type fundingIncomePayload struct {
	Symbol        string          `json:"symbol"`
	IncomeType    string          `json:"incomeType"`
	Asset         string          `json:"asset"`
	Amount        string          `json:"income"`
	TransactionID json.RawMessage `json:"tranId"`
	OccurredAt    json.RawMessage `json:"time"`
}

// parseAccountTrades converts the two Binance fill shapes to one strict
// internal representation. A malformed row invalidates the complete response;
// accepting a partial page would make local position and fee totals unsafe.
func parseAccountTrades(marketType marketdata.MarketType, body []byte) ([]AccountTrade, error) {
	switch marketType {
	case marketdata.MarketTypeSpot:
		var payload []spotTradePayload
		if err := json.Unmarshal(body, &payload); err != nil || payload == nil {
			return nil, errors.New("invalid Spot trade response")
		}
		trades := make([]AccountTrade, 0, len(payload))
		seen := make(map[int64]struct{}, len(payload))
		for _, row := range payload {
			trade, err := normalizeSpotTrade(row)
			if err != nil {
				return nil, err
			}
			if _, exists := seen[trade.ExchangeTradeID]; exists {
				return nil, errors.New("duplicate Spot trade ID")
			}
			seen[trade.ExchangeTradeID] = struct{}{}
			trades = append(trades, trade)
		}
		sortAccountTrades(trades)
		return trades, nil
	case marketdata.MarketTypeUSDM:
		var payload []usdmTradePayload
		if err := json.Unmarshal(body, &payload); err != nil || payload == nil {
			return nil, errors.New("invalid USD-M trade response")
		}
		trades := make([]AccountTrade, 0, len(payload))
		seen := make(map[int64]struct{}, len(payload))
		for _, row := range payload {
			trade, err := normalizeUSDMTrade(row)
			if err != nil {
				return nil, err
			}
			if _, exists := seen[trade.ExchangeTradeID]; exists {
				return nil, errors.New("duplicate USD-M trade ID")
			}
			seen[trade.ExchangeTradeID] = struct{}{}
			trades = append(trades, trade)
		}
		sortAccountTrades(trades)
		return trades, nil
	default:
		return nil, errors.New("unsupported trade market")
	}
}

func normalizeSpotTrade(row spotTradePayload) (AccountTrade, error) {
	symbol, err := privateToken(row.Symbol, 64)
	if err != nil {
		return AccountTrade{}, errors.New("invalid Spot trade symbol")
	}
	tradeID, err := privatePositiveInt(row.TradeID)
	if err != nil {
		return AccountTrade{}, errors.New("invalid Spot trade ID")
	}
	orderID, err := privatePositiveInt(row.OrderID)
	if err != nil {
		return AccountTrade{}, errors.New("invalid Spot order ID")
	}
	if row.IsBuyer == nil || row.IsMaker == nil {
		return AccountTrade{}, errors.New("invalid Spot trade flags")
	}
	price, err := privateDecimal(row.Price, false)
	if err != nil || !price.IsPositive() {
		return AccountTrade{}, errors.New("invalid Spot trade price")
	}
	quantity, err := privateDecimal(row.Quantity, false)
	if err != nil || !quantity.IsPositive() {
		return AccountTrade{}, errors.New("invalid Spot trade quantity")
	}
	quoteQuantity, err := privateDecimal(row.QuoteQuantity, false)
	if err != nil || !quoteQuantity.IsPositive() {
		return AccountTrade{}, errors.New("invalid Spot trade quote quantity")
	}
	commission, err := privateDecimal(row.Commission, false)
	if err != nil || commission.IsNegative() {
		return AccountTrade{}, errors.New("invalid Spot trade commission")
	}
	commissionAsset, err := privateToken(row.CommissionAsset, 32)
	if err != nil {
		return AccountTrade{}, errors.New("invalid Spot trade commission asset")
	}
	occurredAt, err := privateUnixMillis(row.OccurredAt)
	if err != nil {
		return AccountTrade{}, errors.New("invalid Spot trade time")
	}
	side := "sell"
	if *row.IsBuyer {
		side = "buy"
	}
	return AccountTrade{
		Symbol: symbol, ExchangeTradeID: tradeID, ExchangeOrderID: orderID,
		Side: side, PositionSide: "both", Quantity: quantity, Price: price,
		QuoteQuantity: quoteQuantity, Commission: commission, CommissionAsset: commissionAsset,
		Buyer: *row.IsBuyer, Maker: *row.IsMaker, OccurredAt: occurredAt,
	}, nil
}

func normalizeUSDMTrade(row usdmTradePayload) (AccountTrade, error) {
	symbol, err := privateToken(row.Symbol, 64)
	if err != nil {
		return AccountTrade{}, errors.New("invalid USD-M trade symbol")
	}
	tradeID, err := privatePositiveInt(row.TradeID)
	if err != nil {
		return AccountTrade{}, errors.New("invalid USD-M trade ID")
	}
	orderID, err := privatePositiveInt(row.OrderID)
	if err != nil {
		return AccountTrade{}, errors.New("invalid USD-M order ID")
	}
	side, err := privateEnum(row.Side, 8)
	if err != nil || (side != "buy" && side != "sell") {
		return AccountTrade{}, errors.New("invalid USD-M trade side")
	}
	positionSide, err := privateEnum(row.PositionSide, 8)
	if err != nil || (positionSide != "both" && positionSide != "long" && positionSide != "short") {
		return AccountTrade{}, errors.New("invalid USD-M position side")
	}
	if row.Buyer == nil || row.Maker == nil {
		return AccountTrade{}, errors.New("invalid USD-M trade flags")
	}
	price, err := privateDecimal(row.Price, false)
	if err != nil || !price.IsPositive() {
		return AccountTrade{}, errors.New("invalid USD-M trade price")
	}
	quantity, err := privateDecimal(row.Quantity, false)
	if err != nil || !quantity.IsPositive() {
		return AccountTrade{}, errors.New("invalid USD-M trade quantity")
	}
	quoteQuantity, err := privateDecimal(row.QuoteQuantity, false)
	if err != nil || !quoteQuantity.IsPositive() {
		return AccountTrade{}, errors.New("invalid USD-M trade quote quantity")
	}
	commission, err := privateDecimal(row.Commission, false)
	if err != nil || commission.IsNegative() {
		return AccountTrade{}, errors.New("invalid USD-M trade commission")
	}
	commissionAsset, err := privateToken(row.CommissionAsset, 32)
	if err != nil {
		return AccountTrade{}, errors.New("invalid USD-M trade commission asset")
	}
	realizedPnL, err := privateDecimal(row.RealizedPnL, true)
	if err != nil {
		return AccountTrade{}, errors.New("invalid USD-M realized PnL")
	}
	occurredAt, err := privateUnixMillis(row.OccurredAt)
	if err != nil {
		return AccountTrade{}, errors.New("invalid USD-M trade time")
	}
	return AccountTrade{
		Symbol: symbol, ExchangeTradeID: tradeID, ExchangeOrderID: orderID,
		Side: side, PositionSide: positionSide, Quantity: quantity, Price: price,
		QuoteQuantity: quoteQuantity, Commission: commission, CommissionAsset: commissionAsset,
		RealizedPnL: realizedPnL, Buyer: *row.Buyer, Maker: *row.Maker, OccurredAt: occurredAt,
	}, nil
}

func parseFundingIncome(body []byte) ([]FundingIncome, error) {
	var payload []fundingIncomePayload
	if err := json.Unmarshal(body, &payload); err != nil || payload == nil {
		return nil, errors.New("invalid funding income response")
	}
	income := make([]FundingIncome, 0, len(payload))
	seen := make(map[string]struct{}, len(payload))
	for _, row := range payload {
		incomeType, err := privateEnum(row.IncomeType, 32)
		if err != nil || incomeType != "funding_fee" {
			return nil, errors.New("invalid funding income type")
		}
		transactionID, err := privateExternalID(row.TransactionID)
		if err != nil {
			return nil, errors.New("invalid funding transaction ID")
		}
		if _, exists := seen[transactionID]; exists {
			return nil, errors.New("duplicate funding transaction ID")
		}
		seen[transactionID] = struct{}{}
		symbol := ""
		if row.Symbol != "" {
			symbol, err = privateToken(row.Symbol, 64)
			if err != nil {
				return nil, errors.New("invalid funding symbol")
			}
		}
		asset, err := privateToken(row.Asset, 32)
		if err != nil {
			return nil, errors.New("invalid funding asset")
		}
		amount, err := privateDecimal(row.Amount, true)
		if err != nil {
			return nil, errors.New("invalid funding amount")
		}
		occurredAt, err := privateUnixMillis(row.OccurredAt)
		if err != nil {
			return nil, errors.New("invalid funding time")
		}
		income = append(income, FundingIncome{
			TransactionID: transactionID, Symbol: symbol, IncomeType: "FUNDING_FEE",
			Asset: asset, Amount: amount, OccurredAt: occurredAt,
		})
	}
	sort.Slice(income, func(i, j int) bool {
		if income[i].OccurredAt.Equal(income[j].OccurredAt) {
			return income[i].TransactionID < income[j].TransactionID
		}
		return income[i].OccurredAt.Before(income[j].OccurredAt)
	})
	return income, nil
}

func sortAccountTrades(trades []AccountTrade) {
	sort.Slice(trades, func(i, j int) bool {
		if trades[i].OccurredAt.Equal(trades[j].OccurredAt) {
			return trades[i].ExchangeTradeID < trades[j].ExchangeTradeID
		}
		return trades[i].OccurredAt.Before(trades[j].OccurredAt)
	})
}

func privatePositiveInt(raw json.RawMessage) (int64, error) {
	value, err := privateExternalID(raw)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, errors.New("invalid positive integer")
	}
	return parsed, nil
}

func privateExternalID(raw json.RawMessage) (string, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return "", errors.New("missing external ID")
	}
	quoted := false
	if strings.HasPrefix(value, "\"") {
		quoted = true
		var decoded string
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return "", err
		}
		value = decoded
	} else if value[0] < '0' || value[0] > '9' {
		return "", errors.New("invalid external ID type")
	}
	if value == "" || value != strings.TrimSpace(value) || len(value) > 64 {
		return "", errors.New("invalid external ID")
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || quoted && ((char >= 'A' && char <= 'Z') ||
			(char >= 'a' && char <= 'z') || char == '_' || char == '-')) {
			return "", errors.New("invalid external ID")
		}
	}
	return value, nil
}

func privateUnixMillis(raw json.RawMessage) (time.Time, error) {
	value, err := privateExternalID(raw)
	if err != nil {
		return time.Time{}, err
	}
	millis, err := strconv.ParseInt(value, 10, 64)
	if err != nil || millis <= 0 {
		return time.Time{}, errors.New("invalid Unix milliseconds")
	}
	result := time.UnixMilli(millis).UTC()
	if result.IsZero() {
		return time.Time{}, errors.New("invalid Unix milliseconds")
	}
	return result, nil
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
		Symbol                  string `json:"symbol"`
		OrderID                 int64  `json:"orderId"`
		ClientOrderID           string `json:"clientOrderId"`
		Side                    string `json:"side"`
		OrderType               string `json:"type"`
		Status                  string `json:"status"`
		Price                   string `json:"price"`
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
		cumulativeQuoteRaw := order.CumulativeQuoteQuantity
		if cumulativeQuoteRaw == "" {
			cumulativeQuoteRaw = order.CumQuote
		}
		if cumulativeQuoteRaw == "" {
			cumulativeQuoteRaw = "0"
		}
		cumulativeQuote, err := privateDecimal(cumulativeQuoteRaw, false)
		if err != nil {
			return AccountSnapshot{}, err
		}
		averagePrice := decimal.Zero
		if order.AveragePrice != "" {
			averagePrice, err = privateDecimal(order.AveragePrice, false)
			if err != nil {
				return AccountSnapshot{}, err
			}
		} else if executedQuantity.IsPositive() {
			if !cumulativeQuote.IsPositive() {
				return AccountSnapshot{}, errors.New("invalid open order average price")
			}
			averagePrice = cumulativeQuote.Div(executedQuantity)
			if _, err := marketdata.ParseDecimal(averagePrice.String()); err != nil {
				return AccountSnapshot{}, err
			}
		}
		if (executedQuantity.IsZero() && (!cumulativeQuote.IsZero() || !averagePrice.IsZero())) ||
			(executedQuantity.IsPositive() && (!cumulativeQuote.IsPositive() || !averagePrice.IsPositive())) {
			return AccountSnapshot{}, errors.New("invalid open order fill values")
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
			OriginalQuantity: originalQuantity, ExecutedQuantity: executedQuantity,
			CumulativeQuoteQuantity: cumulativeQuote, AveragePrice: averagePrice, StopPrice: stopPrice,
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

func parseUSDMPositionRisk(body []byte, whitelistedSymbols []string) ([]AccountPosition, error) {
	wanted := make(map[string]struct{}, len(whitelistedSymbols))
	for _, raw := range whitelistedSymbols {
		symbol, err := privateToken(raw, 64)
		if err != nil {
			return nil, err
		}
		wanted[symbol] = struct{}{}
	}
	var payload []struct {
		Symbol           string `json:"symbol"`
		PositionSide     string `json:"positionSide"`
		PositionAmount   string `json:"positionAmt"`
		EntryPrice       string `json:"entryPrice"`
		MarkPrice        string `json:"markPrice"`
		LiquidationPrice string `json:"liquidationPrice"`
		UnrealizedProfit string `json:"unrealizedProfit"`
		UnRealizedProfit string `json:"unRealizedProfit"`
		Leverage         string `json:"leverage"`
		MarginType       string `json:"marginType"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload == nil {
		return nil, errors.New("invalid USD-M position risk response")
	}
	found := make(map[string]struct{}, len(wanted))
	positions := make([]AccountPosition, 0, len(wanted))
	for _, row := range payload {
		symbol, err := privateToken(row.Symbol, 64)
		if err != nil {
			return nil, err
		}
		if _, ok := wanted[symbol]; !ok {
			continue
		}
		positionSide, err := privateEnum(row.PositionSide, 8)
		if err != nil || (positionSide != "both" && positionSide != "long" && positionSide != "short") {
			return nil, errors.New("invalid USD-M position side")
		}
		quantity, err := privateDecimal(row.PositionAmount, true)
		if err != nil {
			return nil, err
		}
		entryPrice, err := privateDecimal(row.EntryPrice, false)
		if err != nil || (!quantity.IsZero() && !entryPrice.IsPositive()) {
			return nil, errors.New("invalid USD-M entry price")
		}
		markPrice, err := privateDecimal(row.MarkPrice, false)
		if err != nil || !markPrice.IsPositive() {
			return nil, errors.New("invalid USD-M mark price")
		}
		liquidationPrice, err := privateDecimal(row.LiquidationPrice, false)
		if err != nil || (!quantity.IsZero() && !liquidationPrice.IsPositive()) {
			return nil, errors.New("invalid USD-M liquidation price")
		}
		unrealizedRaw := row.UnrealizedProfit
		if unrealizedRaw == "" {
			unrealizedRaw = row.UnRealizedProfit
		}
		unrealizedPnL, err := privateDecimal(unrealizedRaw, true)
		if err != nil {
			return nil, err
		}
		leverage, err := strconv.Atoi(row.Leverage)
		if err != nil || leverage < 1 || leverage > 125 || strconv.Itoa(leverage) != row.Leverage {
			return nil, errors.New("invalid USD-M leverage")
		}
		marginType, err := privateText(row.MarginType, 16)
		if err != nil {
			return nil, err
		}
		marginType = strings.ToLower(marginType)
		if marginType != "isolated" && marginType != "cross" {
			return nil, errors.New("invalid USD-M margin type")
		}
		liquidationDistance := decimal.Zero
		if liquidationPrice.IsPositive() {
			liquidationDistance = markPrice.Sub(liquidationPrice).Abs().DivRound(markPrice, 18)
		}
		positions = append(positions, AccountPosition{
			Symbol: symbol, PositionSide: positionSide, Quantity: quantity, EntryPrice: entryPrice,
			MarkPrice: markPrice, LiquidationPrice: liquidationPrice,
			LiquidationDistanceRatio: liquidationDistance, UnrealizedPnL: unrealizedPnL,
			Leverage: leverage, Isolated: marginType == "isolated",
		})
		found[symbol] = struct{}{}
	}
	for symbol := range wanted {
		if _, ok := found[symbol]; !ok {
			return nil, errors.New("USD-M position risk is missing a whitelisted symbol")
		}
	}
	sort.Slice(positions, func(i, j int) bool {
		if positions[i].Symbol == positions[j].Symbol {
			return positions[i].PositionSide < positions[j].PositionSide
		}
		return positions[i].Symbol < positions[j].Symbol
	})
	return positions, nil
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
