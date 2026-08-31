package safehttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var ErrUnsafeEndpoint = errors.New("endpoint blocked by network policy")

type lookupNetIPFunc func(context.Context, string, string) ([]netip.Addr, error)
type dialContextFunc func(context.Context, string, string) (net.Conn, error)

type Client struct {
	client       *http.Client
	allowedHosts map[string]struct{}
	lookup       lookupNetIPFunc
	dial         dialContextFunc
}

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"), netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"), netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func New(allowedHosts []string) (*Client, error) {
	dialer := &net.Dialer{}
	allowed := make(map[string]struct{}, len(allowedHosts))
	for _, raw := range allowedHosts {
		host, ok := NormalizeDomain(raw)
		if !ok {
			return nil, unsafeEndpoint("invalid allowed host %q", raw)
		}
		allowed[host] = struct{}{}
	}
	client := &Client{allowedHosts: allowed, lookup: net.DefaultResolver.LookupNetIP, dial: dialer.DialContext}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = client.DialContext
	transport.DisableKeepAlives = true
	client.client = &http.Client{
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			request.Header.Del("Authorization")
			return client.validateHTTPURL(request.Context(), request.Method, request.URL)
		},
	}
	return client, nil
}

func (c *Client) Do(request *http.Request) (*http.Response, error) {
	if err := c.validateHTTPURL(request.Context(), request.Method, request.URL); err != nil {
		return nil, err
	}
	return c.client.Do(request)
}

// DoProxied keeps the fixed-host and public-endpoint checks while letting the configured proxy resolve the target.
func (c *Client) DoProxied(request *http.Request, proxyURL *url.URL) (*http.Response, error) {
	if proxyURL == nil {
		return c.Do(request)
	}
	if err := c.validateProxiedHTTPURL(request.Method, request.URL); err != nil {
		return nil, err
	}
	if err := validateProxyURL(proxyURL); err != nil {
		return nil, err
	}
	transport := c.client.Transport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	transport.DialContext = (&net.Dialer{}).DialContext
	client := &http.Client{
		Transport: transport,
		Timeout:   c.client.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("proxied redirects are disabled")
		},
	}
	return client.Do(request)
}

func (c *Client) SetTimeout(timeout time.Duration) {
	c.client.Timeout = timeout
}

func (c *Client) DisableRedirects() {
	c.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
}

func (c *Client) validateHTTPURL(ctx context.Context, method string, target *url.URL) error {
	if err := c.validateURL(ctx, target, "http", "https"); err != nil {
		return err
	}
	if isBinanceDomain(target.Hostname()) && !isBinancePublicHTTP(method, target.Path) {
		return unsafeEndpoint("Binance private or unknown endpoint is not available to generic connectors")
	}
	return nil
}

func (c *Client) validateProxiedHTTPURL(method string, target *url.URL) error {
	if err := c.validateURLWithoutResolution(target, "http", "https"); err != nil {
		return err
	}
	if isBinanceDomain(target.Hostname()) && !isBinancePublicHTTP(method, target.Path) {
		return unsafeEndpoint("Binance private or unknown endpoint is not available to generic connectors")
	}
	return nil
}

func (c *Client) ValidateWebSocketURL(ctx context.Context, target *url.URL, usesAuthorization bool) error {
	if err := c.validateURL(ctx, target, "ws", "wss"); err != nil {
		return err
	}
	if isBinanceDomain(target.Hostname()) && (usesAuthorization ||
		!(strings.HasPrefix(target.Path, "/ws/") || target.Path == "/stream")) {
		return unsafeEndpoint("Binance WebSocket connector is limited to public streams")
	}
	return nil
}

func (c *Client) ValidateProxiedWebSocketURL(target *url.URL, usesAuthorization bool) error {
	if err := c.validateURLWithoutResolution(target, "ws", "wss"); err != nil {
		return err
	}
	if isBinanceDomain(target.Hostname()) && (usesAuthorization ||
		!(strings.HasPrefix(target.Path, "/ws/") || target.Path == "/stream")) {
		return unsafeEndpoint("Binance WebSocket connector is limited to public streams")
	}
	return nil
}

func (c *Client) validateURL(ctx context.Context, target *url.URL, schemes ...string) error {
	if err := c.validateURLWithoutResolution(target, schemes...); err != nil {
		return err
	}
	_, err := c.resolvePublic(ctx, target.Hostname())
	return err
}

func (c *Client) validateURLWithoutResolution(target *url.URL, schemes ...string) error {
	if target == nil || target.Host == "" || target.User != nil {
		return unsafeEndpoint("an absolute URL without userinfo is required")
	}
	allowedScheme := false
	for _, scheme := range schemes {
		allowedScheme = allowedScheme || target.Scheme == scheme
	}
	if !allowedScheme {
		return unsafeEndpoint("URL scheme is not allowed")
	}
	host, ok := NormalizeDomain(target.Hostname())
	if !ok {
		return unsafeEndpoint("target host must be a domain name")
	}
	if _, ok := c.allowedHosts[host]; !ok {
		return unsafeEndpoint("target host %q is not allowed", host)
	}
	return nil
}

func validateProxyURL(proxyURL *url.URL) error {
	if proxyURL == nil || !proxyURL.IsAbs() || proxyURL.Opaque != "" || proxyURL.Hostname() == "" || proxyURL.Port() == "" ||
		proxyURL.Scheme != "http" && proxyURL.Scheme != "socks5" || proxyURL.RawQuery != "" ||
		proxyURL.Fragment != "" || proxyURL.Path != "" || proxyURL.RawPath != "" || proxyURL.ForceQuery {
		return unsafeEndpoint("configured proxy URL is invalid")
	}
	return nil
}

func (c *Client) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := c.resolvePublic(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ip := range addresses {
		if network == "tcp4" && !ip.Is4() || network == "tcp6" && !ip.Is6() {
			continue
		}
		connection, dialErr := c.dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no resolved address supports network %s", network)
}

func (c *Client) resolvePublic(ctx context.Context, rawHost string) ([]netip.Addr, error) {
	return c.resolveDomain(ctx, rawHost, true)
}

func (c *Client) ResolvePublicDomain(ctx context.Context, rawHost string) ([]netip.Addr, error) {
	return c.resolveDomain(ctx, rawHost, false)
}

func (c *Client) resolveDomain(ctx context.Context, rawHost string, requireAllowed bool) ([]netip.Addr, error) {
	host, ok := NormalizeDomain(rawHost)
	if !ok {
		return nil, unsafeEndpoint("target host must be a domain name")
	}
	if _, ok := c.allowedHosts[host]; requireAllowed && !ok {
		return nil, unsafeEndpoint("target host %q is not allowed", host)
	}
	addresses, err := c.lookup(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve endpoint %s: %w", host, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("resolve endpoint %s: no addresses", host)
	}
	for index, address := range addresses {
		address = address.Unmap()
		if !isPublicAddress(address) {
			return nil, unsafeEndpoint("target host %q resolves to a non-public address", host)
		}
		addresses[index] = address
	}
	return addresses, nil
}

func NormalizeDomain(raw string) (string, bool) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if host == "" || len(host) > 253 || net.ParseIP(host) != nil {
		return "", false
	}
	hasLetter := false
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, char := range label {
			switch {
			case char >= 'a' && char <= 'z':
				hasLetter = true
			case char >= '0' && char <= '9', char == '-':
			default:
				return "", false
			}
		}
	}
	return host, hasLetter
}

func isPublicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func isBinanceDomain(host string) bool {
	host = strings.ToLower(host)
	return host == "binance.com" || strings.HasSuffix(host, ".binance.com") ||
		host == "binance.vision" || strings.HasSuffix(host, ".binance.vision")
}

func isBinancePublicHTTP(method, path string) bool {
	if method != http.MethodGet {
		return false
	}
	for _, prefix := range []string{
		"/api/v3/ping", "/api/v3/time", "/api/v3/exchangeInfo", "/api/v3/depth",
		"/api/v3/trades", "/api/v3/aggTrades", "/api/v3/klines", "/api/v3/uiKlines",
		"/api/v3/avgPrice", "/api/v3/ticker", "/fapi/v1/ping", "/fapi/v1/time",
		"/fapi/v1/exchangeInfo", "/fapi/v1/depth", "/fapi/v1/trades", "/fapi/v1/aggTrades",
		"/fapi/v1/klines", "/fapi/v1/continuousKlines", "/fapi/v1/indexPriceKlines",
		"/fapi/v1/markPriceKlines", "/fapi/v1/premiumIndex", "/fapi/v1/fundingRate",
		"/fapi/v1/fundingInfo", "/fapi/v1/ticker", "/fapi/v1/openInterest", "/fapi/v1/indexInfo",
		"/fapi/v1/assetIndex", "/fapi/v1/constituents",
	} {
		if path == prefix {
			return true
		}
	}
	return false
}

func PermanentWebSocketStatus(status int) bool {
	return status >= 400 && status < 500 && status != http.StatusRequestTimeout && status != http.StatusTooManyRequests
}

func Blocked(reason string) error {
	return fmt.Errorf("%w: %s", ErrUnsafeEndpoint, reason)
}

func unsafeEndpoint(format string, args ...any) error {
	return Blocked(fmt.Sprintf(format, args...))
}
