package official

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
