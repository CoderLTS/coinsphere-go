package official

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"coinsphere/backend/internal/migration"
	"coinsphere/backend/plugin/sdk"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/shopspring/decimal"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var quantSchemaSequence atomic.Uint64

func TestQuantCandleAndBacktestOperationIdempotency(t *testing.T) {
	gdb, database := openQuantTestDatabase(t)
	runtime := &quantRuntime{db: gdb}
	candles := testQuantCandles(6)
	for index := range candles {
		candles[index].SourceEventID = fmt.Sprintf("spot:BTCUSDT:1h:%d", candles[index].OpenTime.UnixMilli())
		if err := runtime.persistQuantCandle(context.Background(), candles[index]); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.persistQuantCandle(context.Background(), candles[0]); err != nil {
		t.Fatal(err)
	}
	var candleCount int64
	if err := database.QueryRow(`SELECT COUNT(*) FROM plugin_quant.candles`).Scan(&candleCount); err != nil || candleCount != int64(len(candles)) {
		t.Fatalf("candles=%d err=%v", candleCount, err)
	}

	registry := sdk.NewRegistry()
	if err := RegisterQuant(registry, gdb); err != nil {
		t.Fatal(err)
	}
	_, action, ok := registry.Action("official.quant.backtest")
	if !ok {
		t.Fatal("Quant backtest action is unavailable")
	}
	artifacts := &quantTestArtifacts{values: map[string][]byte{}}
	config := mustMarshal(map[string]any{
		"market": "spot", "instrument": "BTCUSDT", "interval": "1h",
		"strategyId": smaStrategyID, "parameters": map[string]any{"fastPeriod": 2, "slowPeriod": 3},
		"startTime":      candles[0].OpenTime.Format(time.RFC3339),
		"endTime":        candles[len(candles)-1].CloseTime.Add(time.Millisecond).Format(time.RFC3339),
		"initialCapital": "1000", "feeRate": "0.001", "slippageRate": "0.001",
	})
	request := sdk.ActionRequest{
		Revision: sdk.RevisionRef{WorkflowID: "1", RevisionID: "1"}, NodeInstanceID: "backtest",
		OperationKey: "quant-operation-1", Config: config, Artifacts: artifacts,
	}
	first, err := action.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := action.Execute(context.Background(), request)
	if err != nil || !bytes.Equal(first.Output, second.Output) {
		t.Fatalf("idempotent output first=%s second=%s err=%v", first.Output, second.Output, err)
	}
	var backtests int64
	var parametersType, manifestType string
	if err := database.QueryRow(`SELECT COUNT(*), MIN(jsonb_typeof(parameters)), MIN(jsonb_typeof(data_manifest)) FROM plugin_quant.backtests`).Scan(&backtests, &parametersType, &manifestType); err != nil {
		t.Fatal(err)
	}
	if backtests != 1 || parametersType != "object" || manifestType != "object" || len(artifacts.values) != 1 {
		t.Fatalf("backtests=%d parameters=%s manifest=%s artifacts=%d", backtests, parametersType, manifestType, len(artifacts.values))
	}
}

func TestQuantPaperLedgerRiskAndIdempotency(t *testing.T) {
	gdb, database := openQuantTestDatabase(t)
	if _, err := database.Exec(`INSERT INTO plugin_quant.instruments
        (market, symbol, base_asset, quote_asset, status, price_tick, quantity_step, min_quantity)
        VALUES ('spot', 'BTCUSDT', 'BTC', 'USDT', 'TRADING', 0.01, 0.01, 0.01)`); err != nil {
		t.Fatal(err)
	}
	runtime := &quantRuntime{db: gdb}
	runtime.quote = func(context.Context, quantSeriesConfig) (quantPublicQuote, error) {
		return quantPublicQuote{Price: decimal.NewFromInt(100), Retrieved: time.Now().UTC()}, nil
	}
	signalAction, paperAction := quantSignalAction{runtime: runtime}, quantPaperAction{runtime: runtime}
	createSignal := func(operation, businessKey, target string) int64 {
		t.Helper()
		result, err := signalAction.Execute(context.Background(), sdk.ActionRequest{
			Revision: sdk.RevisionRef{WorkflowID: "41", RevisionID: "7"}, NodeInstanceID: "signal",
			OperationKey: operation, Config: mustMarshal(map[string]any{"market": "spot", "instrument": "BTCUSDT", "interval": "1h"}),
			Input: mustMarshal(map[string]any{
				"strategyId": smaStrategyID, "strategyVersion": "1.0.0", "target": target,
				"evaluatedAt": time.Now().UTC().Format(time.RFC3339Nano), "businessKey": businessKey,
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		var output struct {
			SignalID int64 `json:"signalId"`
		}
		if json.Unmarshal(result.Output, &output) != nil || output.SignalID <= 0 {
			t.Fatalf("signal output = %s", result.Output)
		}
		return output.SignalID
	}
	paperConfig := func(overrides map[string]string) json.RawMessage {
		values := map[string]any{
			"decisionMode": "auto", "market": "spot", "instrument": "BTCUSDT", "interval": "1h",
			"initialBalance": "10000", "feeRate": "0.001", "maxTotalNotional": "20000",
			"maxInstrumentNotional": "20000", "maxOperationNotional": "20000",
			"maxDailyLoss": "1000", "maxDrawdown": "0.5", "maxQuoteAgeSeconds": 10,
		}
		for key, value := range overrides {
			values[key] = value
		}
		return mustMarshal(values)
	}
	execute := func(operation, node string, signalID int64, config json.RawMessage) sdk.ActionResult {
		t.Helper()
		result, err := paperAction.Execute(context.Background(), sdk.ActionRequest{
			Revision: sdk.RevisionRef{WorkflowID: "41", RevisionID: "7"}, NodeInstanceID: node,
			OperationKey: operation, Config: config,
			Input: mustMarshal(map[string]any{"signalId": signalID, "decisionTaskId": 0, "decisionStatus": "approved"}),
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	firstSignal := createSignal("signal-replaced", "same-candle", "0.25")
	activeSignal := createSignal("signal-active", "same-candle", "0.5")
	var firstStatus string
	var supersededBy sql.NullInt64
	if err := database.QueryRow(`SELECT status, superseded_by FROM plugin_quant.signals WHERE id = $1`, firstSignal).Scan(&firstStatus, &supersededBy); err != nil || firstStatus != "superseded" || supersededBy.Int64 != activeSignal {
		t.Fatalf("superseded signal status=%s replacement=%v err=%v", firstStatus, supersededBy, err)
	}

	requestConfig := paperConfig(nil)
	first := execute("paper-success-a", "paper-a", activeSignal, requestConfig)
	restarted := quantPaperAction{runtime: &quantRuntime{db: gdb, quote: runtime.quote}}
	second, err := restarted.Execute(context.Background(), sdk.ActionRequest{
		Revision: sdk.RevisionRef{WorkflowID: "41", RevisionID: "7"}, NodeInstanceID: "paper-a",
		OperationKey: "paper-success-a", Config: requestConfig,
		Input: mustMarshal(map[string]any{"signalId": activeSignal, "decisionTaskId": 0, "decisionStatus": "approved"}),
	})
	if err != nil || !bytes.Equal(first.Output, second.Output) {
		t.Fatalf("Paper restart output first=%s second=%s err=%v", first.Output, second.Output, err)
	}
	secondAccountSignal := createSignal("signal-node-b", "node-b", "0.25")
	execute("paper-success-b", "paper-b", secondAccountSignal, requestConfig)

	for reason, limits := range map[string]map[string]string{
		"operation_notional":  {"maxOperationNotional": "4999"},
		"instrument_notional": {"maxInstrumentNotional": "4999"},
		"total_notional":      {"maxTotalNotional": "4999"},
		"daily_loss":          {"maxDailyLoss": "0"},
		"drawdown":            {"maxDrawdown": "0"},
	} {
		signalID := createSignal("signal-risk-"+reason, "risk-"+reason, "0.5")
		result := execute("paper-risk-"+reason, "paper-risk-"+reason, signalID, paperConfig(limits))
		var output struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(result.Output, &output) != nil || output.Reason != reason {
			t.Fatalf("risk %s output = %s", reason, result.Output)
		}
	}
	runtime.quote = func(context.Context, quantSeriesConfig) (quantPublicQuote, error) {
		return quantPublicQuote{Price: decimal.NewFromInt(100), Retrieved: time.Now().UTC().Add(-time.Minute)}, nil
	}
	staleSignal := createSignal("signal-stale", "stale", "0.5")
	stale := execute("paper-stale", "paper-stale", staleSignal, requestConfig)
	if !bytes.Contains(stale.Output, []byte(`"reason":"stale_quote"`)) {
		t.Fatalf("stale quote output = %s", stale.Output)
	}

	for table, want := range map[string]int64{
		"paper_accounts": 2, "paper_orders": 2, "paper_fills": 2, "paper_fees": 2, "paper_ledger_entries": 4,
	} {
		var count int64
		if err := database.QueryRow(`SELECT COUNT(*) FROM plugin_quant.` + table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d want=%d err=%v", table, count, want, err)
		}
	}
	var accountID int64
	if err := database.QueryRow(`SELECT id FROM plugin_quant.paper_accounts WHERE workflow_id = 41 AND node_instance_id = 'paper-a'`).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE plugin_quant.paper_accounts SET cash_balance = 1, equity = 1 WHERE id = $1`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE plugin_quant.paper_positions SET quantity = 1 WHERE account_id = $1`, accountID); err != nil {
		t.Fatal(err)
	}
	if err := runtime.rebuildQuantPaperAccount(context.Background(), accountID); err != nil {
		t.Fatal(err)
	}
	var cash, equity decimal.Decimal
	if err := database.QueryRow(`SELECT cash_balance, equity FROM plugin_quant.paper_accounts WHERE id = $1`, accountID).Scan(&cash, &equity); err != nil || !cash.Equal(decimal.NewFromInt(4995)) || !equity.Equal(decimal.NewFromInt(9995)) {
		t.Fatalf("rebuilt account cash=%s equity=%s err=%v", cash, equity, err)
	}
}

func TestNotificationOperationIdempotency(t *testing.T) {
	gdb, database := openQuantTestDatabase(t)
	action := notificationInAppAction{runtime: &notificationRuntime{db: gdb}}
	request := sdk.ActionRequest{
		Revision: sdk.RevisionRef{WorkflowID: "1", RevisionID: "2"}, NodeInstanceID: "notify", OperationKey: "notify-once",
		Config: mustMarshal(map[string]any{"title": "Paper completed"}),
		Input:  mustMarshal(map[string]any{"subjectKey": "signal-1", "message": "Paper execution finished"}),
	}
	first, err := action.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := (notificationInAppAction{runtime: &notificationRuntime{db: gdb}}).Execute(context.Background(), request)
	if err != nil || !bytes.Equal(first.Output, second.Output) {
		t.Fatalf("notification output first=%s second=%s err=%v", first.Output, second.Output, err)
	}
	var count int64
	if err := database.QueryRow(`SELECT COUNT(*) FROM plugin_notification.deliveries`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("notification deliveries=%d err=%v", count, err)
	}
}

type quantTestArtifacts struct{ values map[string][]byte }

func (s *quantTestArtifacts) Put(_ context.Context, mediaType string, source io.Reader) (sdk.Artifact, error) {
	raw, err := io.ReadAll(source)
	if err != nil {
		return sdk.Artifact{}, err
	}
	digest := sha256.Sum256(raw)
	key := hex.EncodeToString(digest[:])
	s.values[key] = raw
	return sdk.Artifact{SHA256: key, MediaType: mediaType, Size: int64(len(raw))}, nil
}

func (s *quantTestArtifacts) Open(_ context.Context, digest string) (io.ReadCloser, error) {
	raw, ok := s.values[digest]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(raw)), nil
}

func openQuantTestDatabase(t *testing.T) (*gorm.DB, *sql.DB) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("COINSPHERE_TEST_POSTGRES_DSN"))
	if dsn == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("COINSPHERE_TEST_POSTGRES_DSN is required in CI")
		}
		t.Skip("COINSPHERE_TEST_POSTGRES_DSN is not configured")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	lock, err := admin.Conn(context.Background())
	if err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	if _, err := lock.ExecContext(context.Background(), "SELECT pg_advisory_lock(671908427)"); err != nil {
		_ = lock.Close()
		_ = admin.Close()
		t.Fatal(err)
	}
	schema := fmt.Sprintf("quant_test_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), quantSchemaSequence.Add(1))
	if _, err := admin.Exec("CREATE SCHEMA " + pgx.Identifier{schema}.Sanitize()); err != nil {
		_, _ = lock.ExecContext(context.Background(), "SELECT pg_advisory_unlock(671908427)")
		_ = lock.Close()
		_ = admin.Close()
		t.Fatal(err)
	}
	testConfig := config.Copy()
	if testConfig.RuntimeParams == nil {
		testConfig.RuntimeParams = map[string]string{}
	}
	testConfig.RuntimeParams["search_path"] = schema
	database := stdlib.OpenDB(*testConfig)
	t.Cleanup(func() {
		_ = database.Close()
		_, _ = admin.Exec("DROP SCHEMA IF EXISTS plugin_quant CASCADE")
		_, _ = admin.Exec("DROP SCHEMA IF EXISTS plugin_notification CASCADE")
		_, _ = admin.Exec("DROP SCHEMA " + pgx.Identifier{schema}.Sanitize() + " CASCADE")
		_, _ = lock.ExecContext(context.Background(), "SELECT pg_advisory_unlock(671908427)")
		_ = lock.Close()
		_ = admin.Close()
	})
	runner, err := migration.New(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: database}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return gdb, database
}

var _ sdk.ArtifactStore = (*quantTestArtifacts)(nil)
