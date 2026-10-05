package binance

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"coinsphere/backend/internal/testdb"
	"coinsphere/backend/plugin/sdk"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type syntheticQuoteClient struct {
	sdk.NetworkClient
	calls int
	t     *testing.T
}

func (s *syntheticQuoteClient) DoProxied(r *http.Request, _ *url.URL) (*http.Response, error) {
	s.calls++
	if r.URL.Path != "/api/v3/ticker/price" {
		s.t.Fatal("unexpected public operation")
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"symbol":"BTCUSDT","price":"0.125"}`))}, nil
}
func (s *syntheticQuoteClient) DoPrivate(*http.Request) (*http.Response, error) {
	s.t.Fatal("private call reached a synthetic test")
	return nil, nil
}
func (s *syntheticQuoteClient) DoPrivateProxied(*http.Request, *url.URL) (*http.Response, error) {
	s.t.Fatal("private call reached a synthetic test")
	return nil, nil
}

func TestPaperExactLedgerIdempotenceAndOrderScope(t *testing.T) {
	_, database := testdb.Open(t, true)
	client := &syntheticQuoteClient{t: t}
	runtime := &binanceRuntime{db: database, client: client}
	request := sdk.ActionRequest{Revision: sdk.RevisionRef{WorkflowID: "7", RevisionID: "11"}, NodeInstanceID: "paper", OperationKey: strings.Repeat("a", 64), Config: json.RawMessage(`{"initialBalance":"1000.123456789012345678","feeRate":"0.001","maxOrderNotional":"100","maxInstrumentNotional":"100"}`), Input: json.RawMessage(`{"venue":"binance","account":"synthetic-paper","market":"spot","instrument":"BTCUSDT","side":"buy","quoteAmount":"1.25","positionEffect":"open","clientOrderId":"synthetic-buy-1"}`), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	result, err := (paperExecuteAction{runtime}).Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := (paperExecuteAction{runtime}).Execute(context.Background(), request)
	if err != nil || string(result.Output) != string(duplicate.Output) || client.calls != 1 {
		t.Fatal("Paper repeated a committed operation", err)
	}
	var balance decimal.Decimal
	if err := database.Model(&paperLedgerEntry{}).Select("coalesce(sum(amount),0)").Where("account=?", "synthetic-paper").Scan(&balance).Error; err != nil || !balance.Equal(decimal.RequireFromString("998.872206789012345678")) {
		t.Fatal("Paper cash lost Decimal precision", balance, err)
	}
	var ledger int64
	database.Model(&paperLedgerEntry{}).Count(&ledger)
	if ledger != 3 {
		t.Fatal("Paper retry duplicated ledger entries")
	}
	second := request
	second.Revision.WorkflowID = "8"
	second.OperationKey = strings.Repeat("b", 64)
	second.Input = json.RawMessage(`{"venue":"binance","account":"other-paper","market":"spot","instrument":"BTCUSDT","side":"buy","quoteAmount":"1.25","positionEffect":"open","clientOrderId":"synthetic-buy-2"}`)
	if _, err := (paperExecuteAction{runtime}).Execute(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]int64{{7}, {}} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/orders", nil)
		runtime.handleOrders(c, sdk.SystemScope{PluginID: pluginID, UserID: 2, WorkflowIDs: ids})
		var envelope struct {
			Data struct {
				Items []json.RawMessage `json:"items"`
			} `json:"data"`
		}
		if json.Unmarshal(recorder.Body.Bytes(), &envelope) != nil || recorder.Code != 200 || len(envelope.Data.Items) != len(ids) {
			t.Fatal("order scope escaped its workflow list")
		}
	}
	var output map[string]any
	_ = json.Unmarshal(result.Output, &output)
	if _, ok := output["executed"].(string); !ok {
		t.Fatal("financial wire values must remain strings")
	}
}

func TestLiveDefaultsAndEveryMissingLimitFailBeforeNetwork(t *testing.T) {
	client := &syntheticQuoteClient{t: t}
	runtime := &binanceRuntime{client: client}
	base := map[string]any{"liveTradingEnabled": true, "accountConfirmed": true, "maxOrderNotional": "100", "maxInstrumentNotional": "100", "maxDailyLoss": "10", "maxSlippage": "0.001", "maxDailyOrders": 10, "maxQuoteAgeSeconds": 10}
	for _, missing := range []string{"liveTradingEnabled", "accountConfirmed", "maxOrderNotional", "maxInstrumentNotional", "maxDailyLoss", "maxSlippage", "maxDailyOrders", "maxQuoteAgeSeconds"} {
		config := map[string]any{}
		for k, v := range base {
			if k != missing {
				config[k] = v
			}
		}
		raw, _ := json.Marshal(config)
		_, err := (liveExecuteAction{runtime}).Execute(context.Background(), sdk.ActionRequest{Config: raw, Input: json.RawMessage(`{"venue":"binance","clientOrderId":"synthetic-never-send","referencePrice":"1","quotedAt":"2026-10-05T00:00:00Z"}`)})
		if err == nil {
			t.Fatal("missing live gate was accepted: " + missing)
		}
	}
	if client.calls != 0 {
		t.Fatal("disabled Live gate issued a network request")
	}
}
