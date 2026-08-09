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
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/internal/marketdata"
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
	if client == nil || ctx == nil {
		return &PrivateError{Kind: PrivateErrorRejected}
	}
	baseURL, ok := client.baseURLs[marketType]
	if !ok || apiKey == "" || apiSecret == "" {
		return &PrivateError{Kind: PrivateErrorAuthentication}
	}
	endpoint := baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + accountPath(marketType)
	endpoint.RawPath = ""
	query := url.Values{
		"recvWindow": {strconv.FormatInt(client.recvWindow.Milliseconds(), 10)},
		"timestamp":  {strconv.FormatInt(client.now().UTC().UnixMilli(), 10)},
	}
	mac := hmac.New(sha256.New, []byte(apiSecret))
	_, _ = mac.Write([]byte(query.Encode()))
	query.Set("signature", hex.EncodeToString(mac.Sum(nil)))
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return &PrivateError{Kind: PrivateErrorRejected}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-MBX-APIKEY", apiKey)
	response, err := client.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &PrivateError{Kind: PrivateErrorUnavailable}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, client.responseLimit+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &PrivateError{Kind: PrivateErrorUnavailable}
	}
	if int64(len(body)) > client.responseLimit {
		return &PrivateError{Kind: PrivateErrorProtocol}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return classifyPrivateResponse(response, body)
	}
	var account map[string]json.RawMessage
	if err := json.Unmarshal(body, &account); err != nil || account == nil {
		return &PrivateError{Kind: PrivateErrorProtocol}
	}
	return nil
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
