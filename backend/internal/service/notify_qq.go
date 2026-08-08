package service

import (
	"bytes"
	"context"
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
)

const (
	qqBotDefaultTokenBaseURL = "https://bots.qq.com"
	qqBotDefaultAPIBaseURL   = "https://api.sgroup.qq.com"
	qqBotResponseLimit       = 64 << 10
)

type qqAccessToken struct {
	Value       string
	Fingerprint string
	ExpiresAt   time.Time
}

type qqBotConfig struct {
	AppID, ClientSecret, TargetType, TargetID string
}

func parseQQBotConfig(channel *notifyRuntimeChannel) (qqBotConfig, error) {
	config := qqBotConfig{
		AppID:        strings.TrimSpace(asString(channel.Config["appId"])),
		ClientSecret: strings.TrimSpace(asString(channel.Secrets["clientSecret"])),
		TargetType:   strings.TrimSpace(asString(channel.Config["targetType"])),
		TargetID:     strings.TrimSpace(asString(channel.Config["targetId"])),
	}
	if config.AppID == "" {
		return qqBotConfig{}, bizErr("QQ Bot AppID 不能为空")
	}
	if config.ClientSecret == "" {
		return qqBotConfig{}, bizErr("QQ Bot Client Secret 不能为空")
	}
	if config.TargetType != "group" && config.TargetType != "channel" {
		return qqBotConfig{}, bizErr("QQ Bot 目标类型必须为 group 或 channel")
	}
	if config.TargetID == "" {
		return qqBotConfig{}, bizErr("QQ Bot 目标 ID 不能为空")
	}
	return config, nil
}

func (a *App) sendQQBot(ctx context.Context, channel *notifyRuntimeChannel, title, content string) (bool, string) {
	config, err := parseQQBotConfig(channel)
	if err != nil {
		return false, qqBotProviderResult(0, "", "invalid_configuration")
	}
	token, err := a.qqBotToken(ctx, channel.ChannelID, config)
	if err != nil {
		return false, qqBotProviderResult(0, "", "authentication_failed")
	}
	endpoint := "/channels/" + url.PathEscape(config.TargetID) + "/messages"
	if config.TargetType == "group" {
		endpoint = "/v2/groups/" + url.PathEscape(config.TargetID) + "/messages"
	}
	message := strings.TrimSpace(title)
	if body := strings.TrimSpace(content); body != "" {
		if message != "" {
			message += "\n"
		}
		message += body
	}
	payload := M{"content": message}
	if config.TargetType == "group" {
		payload["msg_type"] = 0
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, qqBotProviderResult(0, "", "encode_failed")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, qqBotDefaultAPIBaseURL+endpoint, bytes.NewReader(raw))
	if err != nil {
		return false, qqBotProviderResult(0, "", "request_failed")
	}
	request.Header.Set("Authorization", "QQBot "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Union-Appid", config.AppID)
	response, err := a.qqBotClient().Do(request)
	if err != nil {
		return false, qqBotProviderResult(0, "", "request_failed")
	}
	defer response.Body.Close()
	if _, err := readQQBotResponse(response.Body); err != nil {
		return false, qqBotProviderResult(response.StatusCode, response.Header.Get("X-Tps-trace-ID"), "invalid_response")
	}
	ok := response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
	category := ""
	if !ok {
		category = "provider_rejected"
	}
	return ok, qqBotProviderResult(response.StatusCode, response.Header.Get("X-Tps-trace-ID"), category)
}

func (a *App) qqBotToken(ctx context.Context, channelID int64, config qqBotConfig) (string, error) {
	fingerprintRaw := sha256.Sum256([]byte(config.AppID + "\x00" + config.ClientSecret))
	fingerprint := hex.EncodeToString(fingerprintRaw[:])

	a.qqTokenMu.Lock()
	defer a.qqTokenMu.Unlock()
	if a.qqTokens == nil {
		a.qqTokens = map[int64]qqAccessToken{}
	}
	if cached, ok := a.qqTokens[channelID]; ok && cached.Fingerprint == fingerprint &&
		time.Now().UTC().Add(30*time.Second).Before(cached.ExpiresAt) {
		return cached.Value, nil
	}

	raw, err := json.Marshal(M{"appId": config.AppID, "clientSecret": config.ClientSecret})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, qqBotDefaultTokenBaseURL+"/app/getAppAccessToken", bytes.NewReader(raw),
	)
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := a.qqBotClient().Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := readQQBotResponse(response.Body)
	if err != nil {
		return "", err
	}
	var payload struct {
		Code        int             `json:"code"`
		AccessToken string          `json:"access_token"`
		ExpiresIn   json.RawMessage `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", errors.New("decode QQ Bot token response")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices ||
		payload.Code != 0 || strings.TrimSpace(payload.AccessToken) == "" {
		return "", errors.New("QQ Bot token request rejected")
	}
	ttl, err := parseQQBotExpiresIn(payload.ExpiresIn)
	if err != nil || ttl <= 0 {
		return "", errors.New("QQ Bot token expiry is invalid")
	}
	token := strings.TrimSpace(payload.AccessToken)
	a.qqTokens[channelID] = qqAccessToken{
		Value: token, Fingerprint: fingerprint, ExpiresAt: time.Now().UTC().Add(time.Duration(ttl) * time.Second),
	}
	return token, nil
}

func (a *App) qqBotClient() *http.Client {
	if a.qqHTTPClient != nil {
		return a.qqHTTPClient
	}
	return &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func parseQQBotExpiresIn(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 {
		return 0, errors.New("missing expires_in")
	}
	var text string
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, err
		}
	} else {
		text = string(raw)
	}
	return strconv.ParseInt(strings.TrimSpace(text), 10, 64)
}

func readQQBotResponse(reader io.Reader) ([]byte, error) {
	limited := io.LimitReader(reader, qqBotResponseLimit+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(body) > qqBotResponseLimit {
		return nil, errors.New("QQ Bot response is too large")
	}
	return body, nil
}

func qqBotProviderResult(status int, traceID, category string) string {
	result := M{"status": status}
	if traceID = strings.TrimSpace(traceID); traceID != "" {
		result["traceId"] = traceID
	}
	if category != "" {
		result["category"] = category
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf(`{"status":%d}`, status)
	}
	return string(raw)
}
