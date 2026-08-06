package binance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/internal/marketdata"
	"github.com/gorilla/websocket"
)

const (
	defaultSpotRESTURL      = "https://api.binance.com"
	defaultUSDMRESTURL      = "https://fapi.binance.com"
	defaultSpotWebSocketURL = "wss://stream.binance.com:9443/ws"
	defaultUSDMWebSocketURL = "wss://fstream.binance.com/ws"

	defaultReconnectBackoff    = 250 * time.Millisecond
	defaultMaxReconnectBackoff = 5 * time.Second
	defaultResponseLimit       = 16 << 20
)

// SourceConfig configures Binance's public REST and WebSocket endpoints.
//
// The generic base URLs are convenient for local test servers. Per-market
// values take precedence when a deployment uses Binance's separate hosts.
// No API key or other private-authentication setting is accepted here.
type SourceConfig struct {
	RESTBaseURL      string
	WebSocketBaseURL string

	SpotRESTBaseURL  string
	USDMRESTBaseURL  string
	SpotWebSocketURL string
	USDMWebSocketURL string

	HTTPClient          *http.Client
	WebSocketDialer     *websocket.Dialer
	ReconnectBackoff    time.Duration
	MaxReconnectBackoff time.Duration
	ResponseLimit       int64
}

// Config is retained as a concise name for callers constructing a source.
type Config = SourceConfig

// Source implements marketdata.MarketSource with Binance public endpoints.
type Source struct {
	rest             map[marketdata.MarketType]url.URL
	websocket        map[marketdata.MarketType]url.URL
	httpClient       *http.Client
	dialer           *websocket.Dialer
	reconnectWait    time.Duration
	maxReconnectWait time.Duration
	responseLimit    int64
}

var _ marketdata.MarketSource = (*Source)(nil)

// NewSource creates a public-only Binance market source.
func NewSource(config SourceConfig) (*Source, error) {
	if config.ReconnectBackoff < 0 || config.MaxReconnectBackoff < 0 {
		return nil, errors.New("reconnect backoff cannot be negative")
	}
	initialBackoff := config.ReconnectBackoff
	if initialBackoff == 0 {
		initialBackoff = defaultReconnectBackoff
	}
	maxBackoff := config.MaxReconnectBackoff
	if maxBackoff == 0 {
		maxBackoff = defaultMaxReconnectBackoff
	}
	if maxBackoff < initialBackoff {
		return nil, errors.New("max reconnect backoff must not be less than reconnect backoff")
	}
	responseLimit := config.ResponseLimit
	if responseLimit == 0 {
		responseLimit = defaultResponseLimit
	}
	if responseLimit < 1 {
		return nil, errors.New("response limit must be positive")
	}

	rest := make(map[marketdata.MarketType]url.URL, 2)
	websocketEndpoints := make(map[marketdata.MarketType]url.URL, 2)
	for _, marketType := range []marketdata.MarketType{marketdata.MarketTypeSpot, marketdata.MarketTypeUSDM} {
		restURL, err := sourceURL(config.restURL(marketType), []string{"http", "https"})
		if err != nil {
			return nil, err
		}
		wsURL, err := sourceURL(config.websocketURL(marketType), []string{"ws", "wss"})
		if err != nil {
			return nil, err
		}
		rest[marketType] = restURL
		websocketEndpoints[marketType] = wsURL
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	dialer := config.WebSocketDialer
	if dialer == nil {
		copy := *websocket.DefaultDialer
		copy.HandshakeTimeout = 15 * time.Second
		dialer = &copy
	}

	return &Source{
		rest:             rest,
		websocket:        websocketEndpoints,
		httpClient:       httpClient,
		dialer:           dialer,
		reconnectWait:    initialBackoff,
		maxReconnectWait: maxBackoff,
		responseLimit:    responseLimit,
	}, nil
}

// NewPublicSource is an explicit alias for NewSource at call sites that have
// multiple exchange clients in scope.
func NewPublicSource(config SourceConfig) (*Source, error) {
	return NewSource(config)
}

func (config SourceConfig) restURL(marketType marketdata.MarketType) string {
	if marketType == marketdata.MarketTypeSpot {
		if config.SpotRESTBaseURL != "" {
			return config.SpotRESTBaseURL
		}
		if config.RESTBaseURL != "" {
			return config.RESTBaseURL
		}
		return defaultSpotRESTURL
	}
	if config.USDMRESTBaseURL != "" {
		return config.USDMRESTBaseURL
	}
	if config.RESTBaseURL != "" {
		return config.RESTBaseURL
	}
	return defaultUSDMRESTURL
}

func (config SourceConfig) websocketURL(marketType marketdata.MarketType) string {
	if marketType == marketdata.MarketTypeSpot {
		if config.SpotWebSocketURL != "" {
			return config.SpotWebSocketURL
		}
		if config.WebSocketBaseURL != "" {
			return config.WebSocketBaseURL
		}
		return defaultSpotWebSocketURL
	}
	if config.USDMWebSocketURL != "" {
		return config.USDMWebSocketURL
	}
	if config.WebSocketBaseURL != "" {
		return config.WebSocketBaseURL
	}
	return defaultUSDMWebSocketURL
}

// SnapshotInstruments fetches and normalizes one Binance exchangeInfo
// snapshot. Spot and USD-M are deliberately routed to their distinct public
// REST paths.
func (source *Source) SnapshotInstruments(ctx context.Context, marketType marketdata.MarketType) ([]marketdata.InstrumentMetadata, error) {
	if err := source.validateContext(ctx); err != nil {
		return nil, err
	}
	if err := validateMarketType(marketType); err != nil {
		return nil, err
	}
	body, err := source.get(ctx, marketType, "/exchangeInfo")
	if err != nil {
		return nil, err
	}
	return NormalizeInstrumentSnapshot(body, marketType)
}

// FetchCandlePage fetches one normalized Binance klines page. Domain end time
// is exclusive, so the REST endTime parameter is the final millisecond before
// that boundary.
func (source *Source) FetchCandlePage(ctx context.Context, request marketdata.CandlePageRequest) (marketdata.CandlePage, error) {
	if err := source.validateContext(ctx); err != nil {
		return marketdata.CandlePage{}, err
	}
	if err := validateSourceCandleRequest(request); err != nil {
		return marketdata.CandlePage{}, err
	}
	startTime := request.StartTime
	if request.Cursor != "" {
		parsed, err := parseCanonicalCursor(request.Cursor)
		if err != nil {
			return marketdata.CandlePage{}, invalidRequestError("invalid candle cursor")
		}
		startTime = parsed
	}
	query := url.Values{}
	query.Set("symbol", request.Instrument.NativeSymbol)
	query.Set("interval", string(request.Interval))
	query.Set("startTime", strconv.FormatInt(startTime.UnixMilli(), 10))
	query.Set("endTime", strconv.FormatInt(request.EndTime.UnixMilli()-1, 10))
	query.Set("limit", strconv.Itoa(request.Limit))

	body, err := source.get(ctx, request.Instrument.MarketType, "/klines", query)
	if err != nil {
		return marketdata.CandlePage{}, err
	}
	return NormalizeCandlePage(body, request)
}

// SubscribeCandles consumes Binance's public kline stream until cancellation,
// a non-retryable protocol error, or a handler error. Transport failures are
// retried with bounded exponential backoff.
func (source *Source) SubscribeCandles(ctx context.Context, instrument marketdata.Instrument, interval marketdata.CandleInterval, handle marketdata.CandleHandler) error {
	if err := source.validateContext(ctx); err != nil {
		return err
	}
	if err := validateInstrumentForSource(instrument); err != nil {
		return err
	}
	if _, ok := marketdata.CandleIntervalDuration(interval); !ok {
		return invalidRequestError("invalid candle interval")
	}
	if handle == nil {
		return invalidRequestError("nil candle handler")
	}
	stream := strings.ToLower(instrument.NativeSymbol) + "@kline_" + string(interval)
	var latestClosed time.Time
	var hasClosed bool
	return source.subscribe(ctx, instrument.MarketType, stream, func(payload []byte) error {
		candle, err := NormalizeCandleEvent(unwrapEventPayload(payload), instrument, interval)
		if err != nil {
			return err
		}
		if candle.IsClosed && hasClosed && !latestClosed.Before(candle.OpenTime) {
			return nil
		}
		if err := handle(candle); err != nil {
			return err
		}
		if candle.IsClosed && (!hasClosed || latestClosed.Before(candle.OpenTime)) {
			latestClosed = candle.OpenTime
			hasClosed = true
		}
		return nil
	})
}

// SubscribeTickers consumes Binance's public 24-hour ticker stream until
// cancellation or a terminal error.
func (source *Source) SubscribeTickers(ctx context.Context, instrument marketdata.Instrument, handle marketdata.TickerHandler) error {
	if err := source.validateContext(ctx); err != nil {
		return err
	}
	if err := validateInstrumentForSource(instrument); err != nil {
		return err
	}
	if handle == nil {
		return invalidRequestError("nil ticker handler")
	}
	stream := strings.ToLower(instrument.NativeSymbol) + "@ticker"
	return source.subscribe(ctx, instrument.MarketType, stream, func(payload []byte) error {
		ticker, err := NormalizeTickerEvent(unwrapEventPayload(payload), instrument)
		if err != nil {
			return err
		}
		return handle(ticker)
	})
}

func (source *Source) get(ctx context.Context, marketType marketdata.MarketType, resource string, query ...url.Values) ([]byte, error) {
	base, ok := source.rest[marketType]
	if !ok {
		return nil, invalidRequestError("invalid market type")
	}
	endpoint := appendPath(base, restPath(marketType, resource))
	if len(query) != 0 {
		endpoint.RawQuery = query[0].Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, invalidRequestError("invalid Binance endpoint")
	}
	request.Header.Set("Accept", "application/json")
	response, err := source.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, contextError(ctx)
		}
		return nil, unavailableError()
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, source.httpError(response)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, source.responseLimit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, contextError(ctx)
		}
		return nil, unavailableError()
	}
	if int64(len(body)) > source.responseLimit {
		return nil, protocolError("Binance response is too large")
	}
	return body, nil
}

func (source *Source) httpError(response *http.Response) error {
	switch response.StatusCode {
	case http.StatusTooManyRequests, http.StatusTeapot:
		return &marketdata.SourceError{Kind: marketdata.SourceErrorRateLimited, RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"))}
	case http.StatusRequestTimeout, http.StatusTooEarly:
		return unavailableError()
	default:
		if response.StatusCode >= http.StatusInternalServerError {
			return unavailableError()
		}
		return invalidRequestError("Binance request rejected")
	}
}

func (source *Source) subscribe(ctx context.Context, marketType marketdata.MarketType, stream string, handle func([]byte) error) error {
	backoff := source.reconnectWait
	for {
		if err := contextError(ctx); err != nil {
			return err
		}
		err := source.consumeWebSocket(ctx, marketType, stream, handle)
		if err == nil {
			return nil
		}
		var retryErr *websocketRetryError
		if !errors.As(err, &retryErr) || retryErr == nil {
			return err
		}
		if contextErr := contextError(ctx); contextErr != nil {
			return contextErr
		}
		if err := waitContext(ctx, backoff); err != nil {
			return err
		}
		if backoff >= source.maxReconnectWait/2 {
			backoff = source.maxReconnectWait
		} else {
			backoff *= 2
		}
	}
}

func (source *Source) consumeWebSocket(ctx context.Context, marketType marketdata.MarketType, stream string, handle func([]byte) error) error {
	base, ok := source.websocket[marketType]
	if !ok {
		return invalidRequestError("invalid market type")
	}
	endpoint := websocketEndpoint(base, stream)
	connection, _, err := source.dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		if ctx.Err() != nil {
			return contextError(ctx)
		}
		return websocketUnavailableError()
	}
	stopWatcher := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-stopWatcher:
		}
	}()
	defer func() {
		close(stopWatcher)
		_ = connection.Close()
	}()
	connection.SetReadLimit(source.responseLimit)

	for {
		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return contextError(ctx)
			}
			return websocketUnavailableError()
		}
		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			return protocolError("unexpected Binance WebSocket message")
		}
		if err := handle(payload); err != nil {
			return err
		}
	}
}

func (source *Source) validateContext(ctx context.Context) error {
	if source == nil {
		return invalidRequestError("source is required")
	}
	if ctx == nil {
		return invalidRequestError("context is required")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	return nil
}

func validateMarketType(marketType marketdata.MarketType) error {
	if marketType != marketdata.MarketTypeSpot && marketType != marketdata.MarketTypeUSDM {
		return invalidRequestError("invalid market type")
	}
	return nil
}

func validateSourceCandleRequest(request marketdata.CandlePageRequest) error {
	if err := marketdata.ValidateCandlePageRequest(request); err != nil {
		return invalidRequestError("invalid candle request")
	}
	if err := validateMarketType(request.Instrument.MarketType); err != nil {
		return err
	}
	if request.Instrument.Venue != marketdata.VenueBinance {
		return invalidRequestError("instrument venue does not match Binance")
	}
	return nil
}

func validateInstrumentForSource(instrument marketdata.Instrument) error {
	if err := marketdata.ValidateInstrument(instrument); err != nil {
		return invalidRequestError("invalid instrument")
	}
	if err := validateMarketType(instrument.MarketType); err != nil {
		return err
	}
	if instrument.Venue != marketdata.VenueBinance {
		return invalidRequestError("instrument venue does not match Binance")
	}
	return nil
}

func sourceURL(raw string, schemes []string) (url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return url.URL{}, errors.New("invalid Binance endpoint URL")
	}
	for _, scheme := range schemes {
		if parsed.Scheme == scheme {
			return *parsed, nil
		}
	}
	return url.URL{}, errors.New("invalid Binance endpoint URL scheme")
}

func restPath(marketType marketdata.MarketType, resource string) string {
	if marketType == marketdata.MarketTypeSpot {
		return path.Join("/api/v3", resource)
	}
	return path.Join("/fapi/v1", resource)
}

func appendPath(base url.URL, suffix string) url.URL {
	base.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(suffix, "/")
	base.RawPath = ""
	return base
}

func websocketEndpoint(base url.URL, stream string) string {
	base.RawQuery = ""
	base.Fragment = ""
	basePath := strings.TrimRight(base.Path, "/")
	if strings.HasSuffix(basePath, "/stream") {
		base.Path = basePath
		base.RawQuery = url.Values{"streams": []string{stream}}.Encode()
		return base.String()
	}
	if basePath == "" || basePath == "/" {
		basePath = "/ws"
	}
	base.Path = basePath + "/" + stream
	base.RawPath = ""
	return base.String()
}

func unwrapEventPayload(payload []byte) []byte {
	trimmed := strings.TrimSpace(string(payload))
	if !strings.HasPrefix(trimmed, "{") {
		return payload
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err == nil && len(envelope.Data) != 0 {
		return []byte(envelope.Data)
	}
	return payload
}

func parseCanonicalCursor(cursor marketdata.CandleCursor) (time.Time, error) {
	text := string(cursor)
	if !strings.HasSuffix(text, "Z") {
		return time.Time{}, errors.New("cursor must be UTC")
	}
	value, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || value.Location() != time.UTC || value.Format(time.RFC3339Nano) != text || value.Nanosecond() != 0 {
		return time.Time{}, errors.New("invalid cursor")
	}
	return value, nil
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
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

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return contextError(ctx)
	case <-timer.C:
		return nil
	}
}

func unavailableError() *marketdata.SourceError {
	return &marketdata.SourceError{Kind: marketdata.SourceErrorUnavailable, Err: errors.New("binance public endpoint unavailable")}
}

type websocketRetryError struct {
	err *marketdata.SourceError
}

func (errorValue *websocketRetryError) Error() string {
	return errorValue.err.Error()
}

func (errorValue *websocketRetryError) Unwrap() error {
	return errorValue.err
}

func websocketUnavailableError() error {
	return &websocketRetryError{err: unavailableError()}
}

func contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return context.Cause(ctx)
}
