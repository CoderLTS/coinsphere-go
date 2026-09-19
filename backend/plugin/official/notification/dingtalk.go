package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/plugin/official/internal/safehttp"
	"coinsphere/backend/plugin/sdk"
)

const notificationResponseLimit = 64 << 10

func (n *notificationRuntime) sendDingTalk(ctx context.Context, request sdk.ActionRequest, title, message string) (string, error) {
	var config struct {
		Format string `json:"format"`
		Signed bool   `json:"signed"`
	}
	if json.Unmarshal(request.Config, &config) != nil || config.Format != "text" && config.Format != "markdown" {
		return "configuration", errors.New("invalid DingTalk configuration")
	}
	accessToken, err := request.Secrets.Read(ctx, "accessToken")
	if err != nil || strings.TrimSpace(string(accessToken)) == "" {
		return "configuration", errors.New("DingTalk access token is unavailable")
	}
	query := url.Values{"access_token": []string{strings.TrimSpace(string(accessToken))}}
	if config.Signed {
		secret, readErr := request.Secrets.Read(ctx, "signingSecret")
		if readErr != nil || strings.TrimSpace(string(secret)) == "" {
			return "configuration", errors.New("DingTalk signing secret is unavailable")
		}
		timestamp := strconv.FormatInt(time.Now().UTC().UnixMilli(), 10)
		signer := hmac.New(sha256.New, []byte(strings.TrimSpace(string(secret))))
		_, _ = signer.Write([]byte(timestamp + "\n" + strings.TrimSpace(string(secret))))
		query.Set("timestamp", timestamp)
		query.Set("sign", base64.StdEncoding.EncodeToString(signer.Sum(nil)))
	}
	payload := map[string]any{"msgtype": config.Format}
	if config.Format == "markdown" {
		payload["markdown"] = map[string]string{"title": title, "text": "### " + title + "\n\n" + message}
	} else {
		payload["text"] = map[string]string{"content": title + "\n" + message}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "configuration", err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oapi.dingtalk.com/robot/send?"+query.Encode(), bytes.NewReader(raw))
	if err != nil {
		return "configuration", err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := n.http.Do(httpRequest)
	if err != nil {
		return notificationRequestCategory(ctx, err), err
	}
	defer response.Body.Close()
	body, err := readNotificationResponse(response.Body)
	if err != nil {
		return "invalid_response", err
	}
	var result struct {
		ErrorCode int `json:"errcode"`
	}
	if json.Unmarshal(body, &result) != nil {
		return "invalid_response", errors.New("decode DingTalk response")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || result.ErrorCode != 0 {
		return "provider_rejected", errors.New("DingTalk rejected notification")
	}
	return "", nil
}

func readNotificationResponse(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, notificationResponseLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > notificationResponseLimit {
		return nil, errors.New("notification provider response is too large")
	}
	return body, nil
}

func notificationRequestCategory(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, safehttp.ErrUnsafeEndpoint) {
		return "network_policy"
	}
	return "network"
}
