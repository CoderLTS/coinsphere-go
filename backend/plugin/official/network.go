package official

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

var errUnsafeEndpoint = errors.New("endpoint blocked by network policy")

type lookupNetIPFunc func(context.Context, string, string) ([]netip.Addr, error)
type dialContextFunc func(context.Context, string, string) (net.Conn, error)

type safeHTTPClient struct {
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

func newSafeHTTPClient(allowedHosts []string) (*safeHTTPClient, error) {
	dialer := &net.Dialer{}
	return newSafeHTTPClientWithDeps(allowedHosts, net.DefaultResolver.LookupNetIP, dialer.DialContext)
}

func newSafeHTTPClientWithDeps(allowedHosts []string, lookup lookupNetIPFunc, dial dialContextFunc) (*safeHTTPClient, error) {
	allowed := make(map[string]struct{}, len(allowedHosts))
	for _, raw := range allowedHosts {
		host, ok := normalizeDomain(raw)
		if !ok {
			return nil, unsafeEndpoint("invalid allowed host %q", raw)
		}
		allowed[host] = struct{}{}
	}
	client := &safeHTTPClient{allowedHosts: allowed, lookup: lookup, dial: dial}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = client.dialContext
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

func (c *safeHTTPClient) Do(request *http.Request) (*http.Response, error) {
	if err := c.validateHTTPURL(request.Context(), request.Method, request.URL); err != nil {
		return nil, err
	}
	return c.client.Do(request)
}

func (c *safeHTTPClient) validateHTTPURL(ctx context.Context, method string, target *url.URL) error {
	if err := c.validateURL(ctx, target, "http", "https"); err != nil {
		return err
	}
	if isBinanceDomain(target.Hostname()) && !isBinancePublicHTTP(method, target.Path) {
		return unsafeEndpoint("Binance private or unknown endpoint is not available to generic connectors")
	}
	return nil
}

func (c *safeHTTPClient) validateWebSocketURL(ctx context.Context, target *url.URL, usesAuthorization bool) error {
	if err := c.validateURL(ctx, target, "ws", "wss"); err != nil {
		return err
	}
	if isBinanceDomain(target.Hostname()) && (usesAuthorization ||
		!(strings.HasPrefix(target.Path, "/ws/") || target.Path == "/stream")) {
		return unsafeEndpoint("Binance WebSocket connector is limited to public streams")
	}
	return nil
}

func (c *safeHTTPClient) validateURL(ctx context.Context, target *url.URL, schemes ...string) error {
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
	_, err := c.resolvePublic(ctx, target.Hostname())
	return err
}

func (c *safeHTTPClient) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
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

func (c *safeHTTPClient) resolvePublic(ctx context.Context, rawHost string) ([]netip.Addr, error) {
	host, ok := normalizeDomain(rawHost)
	if !ok {
		return nil, unsafeEndpoint("target host must be a domain name")
	}
	if _, ok := c.allowedHosts[host]; !ok {
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

func normalizeDomain(raw string) (string, bool) {
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

func unsafeEndpoint(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errUnsafeEndpoint, fmt.Sprintf(format, args...))
}
