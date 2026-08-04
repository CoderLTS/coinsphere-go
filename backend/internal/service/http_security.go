package service

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

var errUnsafeHTTPRequest = errors.New("HTTP request blocked by SSRF policy")

type lookupNetIPFunc func(context.Context, string, string) ([]netip.Addr, error)
type dialContextFunc func(context.Context, string, string) (net.Conn, error)

type safeHTTPClient struct {
	client       *http.Client
	allowedHosts map[string]struct{}
	lookup       lookupNetIPFunc
	dial         dialContextFunc
}

// 这些网段虽可能满足 GlobalUnicast，但不能作为工作流可访问的公网目标。
var nonPublicHTTPPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func newSafeHTTPClient(allowedHosts []string) (*safeHTTPClient, error) {
	dialer := &net.Dialer{}
	return newSafeHTTPClientWithDeps(allowedHosts, net.DefaultResolver.LookupNetIP, dialer.DialContext)
}

func newSafeHTTPClientWithDeps(allowedHosts []string, lookup lookupNetIPFunc, dial dialContextFunc) (*safeHTTPClient, error) {
	allowed := make(map[string]struct{}, len(allowedHosts))
	for _, raw := range allowedHosts {
		host, ok := normalizeHTTPDomain(raw)
		if !ok {
			return nil, unsafeHTTPRequest("invalid allowed host %q", raw)
		}
		allowed[host] = struct{}{}
	}

	safeClient := &safeHTTPClient{allowedHosts: allowed, lookup: lookup, dial: dial}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = safeClient.dialContext
	transport.DisableKeepAlives = true
	safeClient.client = &http.Client{
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			stripSensitiveHTTPHeaders(request.Header)
			return safeClient.validateURL(request.Context(), request.URL)
		},
	}
	return safeClient, nil
}

func (c *safeHTTPClient) Do(request *http.Request) (*http.Response, error) {
	stripSensitiveHTTPHeaders(request.Header)
	if err := c.validateURL(request.Context(), request.URL); err != nil {
		return nil, err
	}
	return c.client.Do(request)
}

func (c *safeHTTPClient) validateURL(ctx context.Context, target *url.URL) error {
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return unsafeHTTPRequest("only absolute http/https URLs are allowed")
	}
	if target.User != nil {
		return unsafeHTTPRequest("URL userinfo is not allowed")
	}
	_, err := c.resolvePublic(ctx, target.Hostname())
	return err
}

// 拨号前重新解析并只拨已校验的 IP，避免校验与连接之间发生 DNS rebinding。
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
		if (network == "tcp4" && !ip.Is4()) || (network == "tcp6" && !ip.Is6()) {
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
	host, ok := normalizeHTTPDomain(rawHost)
	if !ok {
		return nil, unsafeHTTPRequest("target host must be a domain name")
	}
	if _, ok := c.allowedHosts[host]; !ok {
		return nil, unsafeHTTPRequest("target host %q is not allowed", host)
	}

	addresses, err := c.lookup(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve HTTP target %s: %w", host, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("resolve HTTP target %s: no addresses", host)
	}
	for index, address := range addresses {
		address = address.Unmap()
		if !isPublicHTTPAddress(address) {
			return nil, unsafeHTTPRequest("target host %q resolves to a non-public address", host)
		}
		addresses[index] = address
	}
	return addresses, nil
}

func normalizeHTTPDomain(raw string) (string, bool) {
	host := strings.ToLower(strings.TrimSpace(raw))
	host = strings.TrimSuffix(host, ".")
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

func isPublicHTTPAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() {
		return false
	}
	for _, prefix := range nonPublicHTTPPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func stripSensitiveHTTPHeaders(headers http.Header) {
	for name := range headers {
		lower := strings.ToLower(name)
		compact := strings.NewReplacer("-", "", "_", "").Replace(lower)
		if lower == "authorization" || lower == "cookie" || lower == "proxy-authorization" ||
			strings.Contains(compact, "apikey") || strings.Contains(compact, "token") ||
			strings.Contains(compact, "secret") || strings.Contains(compact, "credential") {
			delete(headers, name)
		}
	}
}

func unsafeHTTPRequest(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errUnsafeHTTPRequest, fmt.Sprintf(format, args...))
}
