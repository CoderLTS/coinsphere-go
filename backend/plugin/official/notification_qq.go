package official

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
)

type qqAccessToken struct {
	Value     string
	ExpiresAt time.Time
}

type qqNotificationConfig struct {
	AppID        string `json:"appId"`
	TargetType   string `json:"targetType"`
	TargetID     string `json:"targetId"`
	ClientSecret string
}

func (n *notificationRuntime) sendQQ(ctx context.Context, request sdk.ActionRequest, title, message string) (string, error) {
	var config qqNotificationConfig
	if json.Unmarshal(request.Config, &config) != nil {
		return "configuration", errors.New("invalid QQ configuration")
	}
	secret, err := request.Secrets.Read(ctx, "clientSecret")
	config.AppID = strings.TrimSpace(config.AppID)
	config.TargetType = strings.TrimSpace(config.TargetType)
	config.TargetID = strings.TrimSpace(config.TargetID)
	config.ClientSecret = strings.TrimSpace(string(secret))
	if err != nil || config.AppID == "" || config.TargetID == "" || config.ClientSecret == "" ||
		config.TargetType != "group" && config.TargetType != "channel" {
		return "configuration", errors.New("QQ credentials or target are invalid")
	}
	token, category, err := n.qqAccessToken(ctx, config)
	if err != nil {
		return category, err
	}
	endpoint := "/channels/" + url.PathEscape(config.TargetID) + "/messages"
	if config.TargetType == "group" {
		endpoint = "/v2/groups/" + url.PathEscape(config.TargetID) + "/messages"
	}
	payload := map[string]any{"content": title + "\n" + message}
	if config.TargetType == "group" {
		payload["msg_type"] = 0
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "configuration", err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.sgroup.qq.com"+endpoint, bytes.NewReader(raw))
	if err != nil {
		return "configuration", err
	}
	httpRequest.Header.Set("Authorization", "QQBot "+token)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("X-Union-Appid", config.AppID)
	response, err := n.http.Do(httpRequest)
	if err != nil {
		return notificationRequestCategory(ctx, err), err
	}
	defer response.Body.Close()
	if _, err := readNotificationResponse(response.Body); err != nil {
		return "invalid_response", err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "provider_rejected", errors.New("QQ rejected notification")
	}
	return "", nil
}

func (n *notificationRuntime) qqAccessToken(ctx context.Context, config qqNotificationConfig) (string, string, error) {
	fingerprintRaw := sha256.Sum256([]byte(config.AppID + "\x00" + config.ClientSecret))
	fingerprint := hex.EncodeToString(fingerprintRaw[:])
	n.qqMu.Lock()
	defer n.qqMu.Unlock()
	if cached, ok := n.qqToken[fingerprint]; ok && time.Now().UTC().Add(30*time.Second).Before(cached.ExpiresAt) {
		return cached.Value, "", nil
	}
	raw, err := json.Marshal(map[string]string{"appId": config.AppID, "clientSecret": config.ClientSecret})
	if err != nil {
		return "", "configuration", err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://bots.qq.com/app/getAppAccessToken", bytes.NewReader(raw))
	if err != nil {
		return "", "configuration", err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := n.http.Do(httpRequest)
	if err != nil {
		return "", notificationRequestCategory(ctx, err), err
	}
	defer response.Body.Close()
	body, err := readNotificationResponse(response.Body)
	if err != nil {
		return "", "invalid_response", err
	}
	var result struct {
		Code        int             `json:"code"`
		AccessToken string          `json:"access_token"`
		ExpiresIn   json.RawMessage `json:"expires_in"`
	}
	if json.Unmarshal(body, &result) != nil {
		return "", "invalid_response", errors.New("decode QQ access token response")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || result.Code != 0 || strings.TrimSpace(result.AccessToken) == "" {
		return "", "authentication", errors.New("QQ access token request rejected")
	}
	ttl, err := qqTokenTTL(result.ExpiresIn)
	if err != nil || ttl <= 0 {
		return "", "invalid_response", errors.New("QQ access token expiry is invalid")
	}
	token := strings.TrimSpace(result.AccessToken)
	n.qqToken[fingerprint] = qqAccessToken{Value: token, ExpiresAt: time.Now().UTC().Add(time.Duration(ttl) * time.Second)}
	return token, "", nil
}

func qqTokenTTL(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 {
		return 0, errors.New("missing QQ token expiry")
	}
	text := string(raw)
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, err
		}
	}
	return strconv.ParseInt(strings.TrimSpace(text), 10, 64)
}
