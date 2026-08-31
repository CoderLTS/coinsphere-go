package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"coinsphere/backend/internal/db"
	"gorm.io/gorm"
)

type OutboundProxyUpsertPayload struct {
	Name          string  `json:"name"`
	Protocol      string  `json:"protocol"`
	Host          string  `json:"host"`
	Port          int     `json:"port"`
	Username      string  `json:"username"`
	Password      *string `json:"password"`
	ClearPassword bool    `json:"clearPassword"`
	IsEnabled     *bool   `json:"isEnabled"`
}

type OutboundProxyPatchPayload struct {
	IsEnabled *bool `json:"isEnabled"`
}

type OutboundProxyView struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Protocol           string `json:"protocol"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	Username           string `json:"username"`
	PasswordConfigured bool   `json:"passwordConfigured"`
	IsEnabled          bool   `json:"isEnabled"`
	LastCheckStatus    string `json:"lastCheckStatus"`
	LastCheckedAt      string `json:"lastCheckedAt"`
	LastLatencyMS      *int   `json:"lastLatencyMs"`
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
}

func (a *App) ListOutboundProxies(ctx context.Context) ([]OutboundProxyView, error) {
	var rows []db.OutboundProxy
	if err := a.DB.WithContext(ctx).Order("name, id").Find(&rows).Error; err != nil {
		return nil, errors.New("list outbound proxies failed")
	}
	result := make([]OutboundProxyView, len(rows))
	for index := range rows {
		result[index] = outboundProxyView(rows[index])
	}
	return result, nil
}

func (a *App) CreateOutboundProxy(ctx context.Context, payload OutboundProxyUpsertPayload, principal *Principal) (OutboundProxyView, error) {
	if principal == nil || principal.User == nil {
		return OutboundProxyView{}, ErrPermission
	}
	proxy, err := a.buildOutboundProxy(ctx, payload, nil, principal.User.ID)
	if err != nil {
		return OutboundProxyView{}, err
	}
	if err := a.DB.WithContext(ctx).Create(&proxy).Error; err != nil {
		return OutboundProxyView{}, errors.New("create outbound proxy failed")
	}
	return outboundProxyView(proxy), nil
}

func (a *App) UpdateOutboundProxy(ctx context.Context, proxyID int64, payload OutboundProxyUpsertPayload, principal *Principal) (OutboundProxyView, error) {
	if principal == nil || principal.User == nil {
		return OutboundProxyView{}, ErrPermission
	}
	var current db.OutboundProxy
	if err := a.DB.WithContext(ctx).First(&current, proxyID).Error; err != nil {
		return OutboundProxyView{}, outboundProxyLookupError(err)
	}
	proxy, err := a.buildOutboundProxy(ctx, payload, &current, principal.User.ID)
	if err != nil {
		return OutboundProxyView{}, err
	}
	updates := map[string]any{
		"name": proxy.Name, "protocol": proxy.Protocol, "host": proxy.Host, "port": proxy.Port,
		"username": proxy.Username, "password_ciphertext": proxy.PasswordCiphertext,
		"is_enabled": proxy.IsEnabled, "last_check_status": "unchecked",
		"last_checked_at": nil, "last_latency_ms": nil,
		"updated_by": proxy.UpdatedBy, "updated_at": proxy.UpdatedAt,
	}
	if err := a.DB.WithContext(ctx).Model(&current).Updates(updates).Error; err != nil {
		return OutboundProxyView{}, errors.New("update outbound proxy failed")
	}
	proxy.ID, proxy.CreatedAt = current.ID, current.CreatedAt
	proxy.LastCheckStatus, proxy.LastCheckedAt, proxy.LastLatencyMS = "unchecked", nil, nil
	return outboundProxyView(proxy), nil
}

func (a *App) PatchOutboundProxy(ctx context.Context, proxyID int64, payload OutboundProxyPatchPayload, principal *Principal) (OutboundProxyView, error) {
	if principal == nil || principal.User == nil {
		return OutboundProxyView{}, ErrPermission
	}
	if payload.IsEnabled == nil {
		return OutboundProxyView{}, errors.New("isEnabled is required")
	}
	result := a.DB.WithContext(ctx).Model(&db.OutboundProxy{}).Where("id = ?", proxyID).Updates(map[string]any{
		"is_enabled": *payload.IsEnabled, "updated_by": principal.User.ID, "updated_at": time.Now().UTC(),
	})
	if result.Error != nil {
		return OutboundProxyView{}, errors.New("update outbound proxy status failed")
	}
	if result.RowsAffected == 0 {
		return OutboundProxyView{}, fmt.Errorf("%w: outbound proxy", ErrNotFound)
	}
	var proxy db.OutboundProxy
	if err := a.DB.WithContext(ctx).First(&proxy, proxyID).Error; err != nil {
		return OutboundProxyView{}, outboundProxyLookupError(err)
	}
	return outboundProxyView(proxy), nil
}

func (a *App) DeleteOutboundProxy(ctx context.Context, proxyID int64) error {
	var references int64
	err := a.DB.WithContext(ctx).Raw(`
		SELECT COUNT(*)
		FROM workflow_revisions revision
		WHERE EXISTS (
			SELECT 1
			FROM jsonb_array_elements(COALESCE(revision.graph_json::jsonb -> 'nodes', '[]'::jsonb)) node
			WHERE node ->> 'nodeType' IN (
				'official.quant.sync_instruments',
				'official.quant.backfill_candles',
				'official.quant.realtime_candles'
			)
			AND node -> 'config' ->> 'proxyId' = ?
		)
	`, strconv.FormatInt(proxyID, 10)).Scan(&references).Error
	if err != nil {
		return errors.New("count outbound proxy references failed")
	}
	if references > 0 {
		return fmt.Errorf("%w: outbound proxy is referenced by workflow revisions", ErrConflict)
	}
	result := a.DB.WithContext(ctx).Delete(&db.OutboundProxy{}, proxyID)
	if result.Error != nil {
		return errors.New("delete outbound proxy failed")
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: outbound proxy", ErrNotFound)
	}
	return nil
}

func (a *App) ValidateOutboundProxy(ctx context.Context, proxyID int64) (M, error) {
	proxyURL, err := a.loadOutboundProxyURL(ctx, proxyID, false)
	if err != nil {
		return nil, err
	}
	checkedAt := time.Now().UTC()
	startedAt := time.Now()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	client := &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("proxy validation redirects are disabled")
		},
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://data-api.binance.vision/api/v3/ping", nil)
	request.Header.Set("Accept", "application/json")
	response, requestErr := client.Do(request)
	status := "healthy"
	message := "代理连接正常"
	var latencyMS *int
	if response != nil {
		defer response.Body.Close()
		if requestErr == nil {
			_, requestErr = io.ReadAll(io.LimitReader(response.Body, 64<<10))
			if response.StatusCode != http.StatusOK {
				requestErr = errors.New("unexpected Binance status")
			}
		}
	}
	if requestErr != nil {
		status, message = "failed", "无法通过该代理访问 Binance 公共接口"
	} else {
		latency := int(time.Since(startedAt).Milliseconds())
		latencyMS = &latency
	}
	if err := a.DB.WithContext(ctx).Model(&db.OutboundProxy{}).Where("id = ?", proxyID).Updates(map[string]any{
		"last_check_status": status, "last_checked_at": checkedAt, "last_latency_ms": latencyMS,
	}).Error; err != nil {
		return nil, errors.New("save outbound proxy validation failed")
	}
	return M{
		"success": status == "healthy", "status": status, "message": message,
		"checkedAt": formatWorkflowTime(checkedAt), "latencyMs": latencyMS,
	}, nil
}

// ResolveOutboundProxy 只供显式选择代理的可信 Binance 公共行情节点使用。
func (a *App) ResolveOutboundProxy(ctx context.Context, proxyID int64) (string, error) {
	if proxyID == 0 {
		return "", nil
	}
	proxyURL, err := a.loadOutboundProxyURL(ctx, proxyID, true)
	if err != nil {
		return "", err
	}
	return proxyURL.String(), nil
}

func (a *App) buildOutboundProxy(ctx context.Context, payload OutboundProxyUpsertPayload, current *db.OutboundProxy, userID int64) (db.OutboundProxy, error) {
	name := strings.TrimSpace(payload.Name)
	protocol := strings.ToLower(strings.TrimSpace(payload.Protocol))
	host, validHost := normalizeProxyHost(payload.Host)
	username := strings.TrimSpace(payload.Username)
	if name == "" || utf8.RuneCountInString(name) > 120 {
		return db.OutboundProxy{}, errors.New("name must contain 1 to 120 characters")
	}
	if protocol != "http" && protocol != "socks5" {
		return db.OutboundProxy{}, errors.New("protocol must be http or socks5")
	}
	if !validHost {
		return db.OutboundProxy{}, errors.New("host must be a valid IP address or domain name")
	}
	if payload.Port < 1 || payload.Port > 65535 {
		return db.OutboundProxy{}, errors.New("port must be between 1 and 65535")
	}
	if utf8.RuneCountInString(username) > 255 {
		return db.OutboundProxy{}, errors.New("username must not exceed 255 characters")
	}
	if payload.Password != nil && len(*payload.Password) > 4096 {
		return db.OutboundProxy{}, errors.New("password must not exceed 4096 bytes")
	}
	if payload.Password != nil && payload.ClearPassword {
		return db.OutboundProxy{}, errors.New("password and clearPassword cannot be used together")
	}
	query := a.DB.WithContext(ctx).Model(&db.OutboundProxy{}).Where("LOWER(name) = LOWER(?)", name)
	if current != nil {
		query = query.Where("id <> ?", current.ID)
	}
	var duplicate int64
	if err := query.Count(&duplicate).Error; err != nil {
		return db.OutboundProxy{}, errors.New("check outbound proxy name failed")
	}
	if duplicate > 0 {
		return db.OutboundProxy{}, fmt.Errorf("%w: outbound proxy name already exists", ErrConflict)
	}
	enabled, ciphertext, createdAt, createdBy := true, "", time.Now().UTC(), userID
	if current != nil {
		enabled, ciphertext, createdAt, createdBy = current.IsEnabled, current.PasswordCiphertext, current.CreatedAt, current.CreatedBy
	}
	if payload.IsEnabled != nil {
		enabled = *payload.IsEnabled
	}
	if payload.Password != nil {
		ciphertext = a.Cipher.Encrypt(*payload.Password)
	} else if payload.ClearPassword {
		ciphertext = ""
	}
	now := time.Now().UTC()
	return db.OutboundProxy{
		Name: name, Protocol: protocol, Host: host, Port: payload.Port, Username: username,
		PasswordCiphertext: ciphertext, IsEnabled: enabled, LastCheckStatus: "unchecked",
		CreatedBy: createdBy, UpdatedBy: userID, CreatedAt: createdAt, UpdatedAt: now,
	}, nil
}

func (a *App) loadOutboundProxyURL(ctx context.Context, proxyID int64, requireEnabled bool) (*url.URL, error) {
	var proxy db.OutboundProxy
	query := a.DB.WithContext(ctx).Where("id = ?", proxyID)
	if requireEnabled {
		query = query.Where("is_enabled = ?", true)
	}
	if err := query.First(&proxy).Error; err != nil {
		return nil, outboundProxyLookupError(err)
	}
	password, err := a.Cipher.Decrypt(proxy.PasswordCiphertext)
	if err != nil {
		return nil, errors.New("decrypt outbound proxy password failed")
	}
	proxyURL := &url.URL{Scheme: proxy.Protocol, Host: net.JoinHostPort(proxy.Host, strconv.Itoa(proxy.Port))}
	if proxy.Username != "" || password != "" {
		proxyURL.User = url.UserPassword(proxy.Username, password)
	}
	return proxyURL, nil
}

func outboundProxyView(proxy db.OutboundProxy) OutboundProxyView {
	checkedAt := ""
	if proxy.LastCheckedAt != nil {
		checkedAt = formatWorkflowTime(*proxy.LastCheckedAt)
	}
	return OutboundProxyView{
		ID: proxy.ID, Name: proxy.Name, Protocol: proxy.Protocol, Host: proxy.Host, Port: proxy.Port,
		Username: proxy.Username, PasswordConfigured: proxy.PasswordCiphertext != "", IsEnabled: proxy.IsEnabled,
		LastCheckStatus: proxy.LastCheckStatus, LastCheckedAt: checkedAt, LastLatencyMS: proxy.LastLatencyMS,
		CreatedAt: formatWorkflowTime(proxy.CreatedAt), UpdatedAt: formatWorkflowTime(proxy.UpdatedAt),
	}
}

func normalizeProxyHost(raw string) (string, bool) {
	host := strings.Trim(strings.TrimSpace(raw), "[]")
	if parsed := net.ParseIP(host); parsed != nil {
		return parsed.String(), true
	}
	if host == "" || len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return "", false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, character := range label {
			if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
				character >= '0' && character <= '9' || character == '-' {
				continue
			}
			return "", false
		}
	}
	return strings.ToLower(host), true
}

func outboundProxyLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: outbound proxy", ErrNotFound)
	}
	return errors.New("load outbound proxy failed")
}
