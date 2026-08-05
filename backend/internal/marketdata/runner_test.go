package marketdata_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"coinsphere/backend/internal/marketdata"
)

func TestPostgresStoreFlowLeaseLifecycle(t *testing.T) {
	database := openStoreTestDatabase(t)
	store := marketdata.NewPostgresStore(database)
	ctx := context.Background()

	first, claimed, err := store.ClaimFlowLease(ctx, "  binance:spot:BTCUSDT:ticker  ", "collector-a", 1100*time.Millisecond)
	if err != nil || !claimed {
		t.Fatalf("claim first lease: claimed=%t err=%v", claimed, err)
	}
	if first.FlowKey != "binance:spot:BTCUSDT:ticker" || first.FencingToken != 1 || first.LeaseExpiresAt.Location() != time.UTC || first.LastHeartbeatAt.Location() != time.UTC {
		t.Fatalf("first lease = %#v", first)
	}
	if duration := first.LeaseExpiresAt.Sub(first.LastHeartbeatAt); duration != 2*time.Second {
		t.Fatalf("rounded lease duration = %s, want 2s", duration)
	}

	if _, claimed, err := store.ClaimFlowLease(ctx, first.FlowKey, "collector-b", time.Second); err != nil || claimed {
		t.Fatalf("claim occupied lease: claimed=%t err=%v", claimed, err)
	}
	if renewed, err := store.RenewFlowLease(ctx, first, time.Second); err != nil || !renewed {
		t.Fatalf("renew first lease: renewed=%t err=%v", renewed, err)
	}
	if released, err := store.ReleaseFlowLease(ctx, first); err != nil || !released {
		t.Fatalf("release first lease: released=%t err=%v", released, err)
	}

	second, claimed, err := store.ClaimFlowLease(ctx, first.FlowKey, "collector-b", time.Second)
	if err != nil || !claimed || second.FencingToken != 2 {
		t.Fatalf("claim released lease: lease=%#v claimed=%t err=%v", second, claimed, err)
	}
	if renewed, err := store.RenewFlowLease(ctx, first, time.Second); err != nil || renewed {
		t.Fatalf("renew stale lease: renewed=%t err=%v", renewed, err)
	}
	if released, err := store.ReleaseFlowLease(ctx, first); err != nil || released {
		t.Fatalf("release stale lease: released=%t err=%v", released, err)
	}

	// 直接在数据库时钟下标记过期，验证接管不依赖 Go 本地时钟。
	if _, err := database.ExecContext(ctx, `
UPDATE market_flow_leases
SET
    last_heartbeat_at = statement_timestamp() - INTERVAL '2 seconds',
    lease_expires_at = statement_timestamp() - INTERVAL '1 second',
    updated_at = statement_timestamp()
WHERE flow_key = $1
`, first.FlowKey); err != nil {
		t.Fatalf("expire lease in database: %v", err)
	}
	third, claimed, err := store.ClaimFlowLease(ctx, first.FlowKey, "collector-c", time.Second)
	if err != nil || !claimed || third.FencingToken != 3 {
		t.Fatalf("claim expired lease: lease=%#v claimed=%t err=%v", third, claimed, err)
	}
}

func TestPostgresStoreFlowLeaseValidationAndExclusion(t *testing.T) {
	database := openStoreTestDatabase(t)
	store := marketdata.NewPostgresStore(database)
	for _, test := range []struct {
		name     string
		flowKey  string
		ownerID  string
		duration time.Duration
	}{
		{name: "blank flow", flowKey: " \t", ownerID: "collector", duration: time.Second},
		{name: "long flow", flowKey: strings.Repeat("x", 201), ownerID: "collector", duration: time.Second},
		{name: "blank owner", flowKey: "flow", ownerID: "", duration: time.Second},
		{name: "trimmed owner", flowKey: "flow", ownerID: " collector ", duration: time.Second},
		{name: "long owner", flowKey: "flow", ownerID: strings.Repeat("x", 121), duration: time.Second},
		{name: "zero lease", flowKey: "flow", ownerID: "collector", duration: 0},
		{name: "negative lease", flowKey: "flow", ownerID: "collector", duration: -time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := store.ClaimFlowLease(context.Background(), test.flowKey, test.ownerID, test.duration); err == nil {
				t.Fatal("invalid claim was accepted")
			}
		})
	}

	const contenders = 8
	ready := make(chan struct{}, contenders)
	start := make(chan struct{})
	results := make(chan bool, contenders)
	errorsByContender := make(chan error, contenders)
	for index := 0; index < contenders; index++ {
		owner := "collector-" + string(rune('a'+index))
		go func() {
			ready <- struct{}{}
			<-start
			_, claimed, err := store.ClaimFlowLease(context.Background(), "concurrent-flow", owner, time.Second)
			results <- claimed
			errorsByContender <- err
		}()
	}
	// 屏障确保多个连接同时竞争同一 flow，而不是按启动顺序串行认领。
	for range contenders {
		<-ready
	}
	close(start)
	claimedCount := 0
	for range contenders {
		if err := <-errorsByContender; err != nil {
			t.Fatalf("concurrent claim: %v", err)
		}
		if <-results {
			claimedCount++
		}
	}
	if claimedCount != 1 {
		t.Fatalf("concurrent claimed count = %d, want 1", claimedCount)
	}
}

func TestFlowRunnerRetryAndPermanentErrors(t *testing.T) {
	database := openStoreTestDatabase(t)
	store := marketdata.NewPostgresStore(database)
	runner, err := marketdata.NewFlowRunner(store, "runner-a", time.Second, 30*time.Millisecond)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	for _, test := range []struct {
		name      string
		sourceErr error
		minimum   time.Duration
	}{
		{name: "rate limited retry after", sourceErr: &marketdata.SourceError{Kind: marketdata.SourceErrorRateLimited, RetryAfter: 70 * time.Millisecond}, minimum: 50 * time.Millisecond},
		{name: "rate limited fallback", sourceErr: &marketdata.SourceError{Kind: marketdata.SourceErrorRateLimited}, minimum: 20 * time.Millisecond},
		{name: "unavailable fallback", sourceErr: &marketdata.SourceError{Kind: marketdata.SourceErrorUnavailable}, minimum: 20 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			permanent := errors.New("subscription completed")
			started := time.Now()
			err := runner.Run(context.Background(), "retry-"+test.name, func(context.Context) error {
				if calls.Add(1) == 1 {
					return test.sourceErr
				}
				return permanent
			})
			if err != permanent || calls.Load() != 2 {
				t.Fatalf("retry result: calls=%d err=%v", calls.Load(), err)
			}
			if elapsed := time.Since(started); elapsed < test.minimum {
				t.Fatalf("retry delay = %s, want at least %s", elapsed, test.minimum)
			}
		})
	}

	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "invalid request", err: &marketdata.SourceError{Kind: marketdata.SourceErrorInvalidRequest}},
		{name: "protocol", err: &marketdata.SourceError{Kind: marketdata.SourceErrorProtocol}},
		{name: "handler", err: errors.New("handler failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			err := runner.Run(context.Background(), "permanent-"+test.name, func(context.Context) error {
				calls.Add(1)
				return test.err
			})
			if err != test.err || calls.Load() != 1 {
				t.Fatalf("permanent result: calls=%d err=%v", calls.Load(), err)
			}
		})
	}
}

func TestFlowRunnerCancellationAndLeaseLoss(t *testing.T) {
	t.Run("caller cancellation", func(t *testing.T) {
		database := openStoreTestDatabase(t)
		runner, err := marketdata.NewFlowRunner(marketdata.NewPostgresStore(database), "runner-cancel", time.Second, 10*time.Millisecond)
		if err != nil {
			t.Fatalf("new runner: %v", err)
		}
		ctx, cancel := context.WithCancelCause(context.Background())
		started := make(chan struct{})
		result := make(chan error, 1)
		go func() {
			result <- runner.Run(ctx, "cancel-flow", func(subscriptionCtx context.Context) error {
				close(started)
				<-subscriptionCtx.Done()
				return subscriptionCtx.Err()
			})
		}()
		<-started
		cancel(errors.New("caller shutdown"))
		if err := awaitRunner(t, result); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled runner error = %v", err)
		}
	})

	t.Run("renewal fenced", func(t *testing.T) {
		database := openStoreTestDatabase(t)
		runner, err := marketdata.NewFlowRunner(marketdata.NewPostgresStore(database), "runner-fenced", time.Second, 10*time.Millisecond)
		if err != nil {
			t.Fatalf("new runner: %v", err)
		}
		started := make(chan struct{})
		cause := make(chan error, 1)
		result := make(chan error, 1)
		go func() {
			result <- runner.Run(context.Background(), "fenced-flow", func(subscriptionCtx context.Context) error {
				close(started)
				<-subscriptionCtx.Done()
				cause <- context.Cause(subscriptionCtx)
				return subscriptionCtx.Err()
			})
		}()
		<-started
		if _, err := database.Exec(`
UPDATE market_flow_leases
SET
    last_heartbeat_at = statement_timestamp() - INTERVAL '2 seconds',
    lease_expires_at = statement_timestamp() - INTERVAL '1 second',
    updated_at = statement_timestamp()
WHERE flow_key = 'fenced-flow'
`); err != nil {
			t.Fatalf("expire active lease: %v", err)
		}
		if err := awaitRunner(t, result); !errors.Is(err, marketdata.ErrFlowLeaseLost) {
			t.Fatalf("fenced runner error = %v", err)
		}
		if subscriptionCause := <-cause; !errors.Is(subscriptionCause, marketdata.ErrFlowLeaseLost) {
			t.Fatalf("subscription cause = %v", subscriptionCause)
		}
	})

	t.Run("waits for initial owner", func(t *testing.T) {
		database := openStoreTestDatabase(t)
		store := marketdata.NewPostgresStore(database)
		held, claimed, err := store.ClaimFlowLease(context.Background(), "occupied-flow", "holder", 10*time.Second)
		if err != nil || !claimed {
			t.Fatalf("claim occupied lease: claimed=%t err=%v", claimed, err)
		}
		runner, err := marketdata.NewFlowRunner(store, "runner-wait", time.Second, 10*time.Millisecond)
		if err != nil {
			t.Fatalf("new runner: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		started := make(chan struct{})
		result := make(chan error, 1)
		go func() {
			result <- runner.Run(ctx, held.FlowKey, func(subscriptionCtx context.Context) error {
				close(started)
				<-subscriptionCtx.Done()
				return subscriptionCtx.Err()
			})
		}()
		select {
		case <-started:
			t.Fatal("subscription started while another owner held the lease")
		case <-time.After(50 * time.Millisecond):
		}
		if released, err := store.ReleaseFlowLease(context.Background(), held); err != nil || !released {
			t.Fatalf("release held lease: released=%t err=%v", released, err)
		}
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("runner did not claim after release")
		}
		cancel()
		if err := awaitRunner(t, result); !errors.Is(err, context.Canceled) {
			t.Fatalf("runner after release error = %v", err)
		}
	})
}

func TestFlowRunnerLogsFingerprints(t *testing.T) {
	database := openStoreTestDatabase(t)
	flowKey := "binance:spot:BTCUSDT:ws?token=flow-secret"
	ownerID := "collector?token=owner-secret"
	runner, err := marketdata.NewFlowRunner(marketdata.NewPostgresStore(database), ownerID, time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	completed := errors.New("subscription completed")
	if err := runner.Run(context.Background(), flowKey, func(context.Context) error { return completed }); err != completed {
		t.Fatalf("run result = %v", err)
	}

	logs := output.String()
	for _, value := range []string{flowKey, ownerID, "flow-secret", "owner-secret"} {
		if strings.Contains(logs, value) {
			t.Fatalf("log exposed sensitive identifier %q: %s", value, logs)
		}
	}
	flowHash := sha256.Sum256([]byte(flowKey))
	ownerHash := sha256.Sum256([]byte(ownerID))
	if want := "flow_fingerprint=" + hex.EncodeToString(flowHash[:]); !strings.Contains(logs, want) {
		t.Fatalf("log missing flow fingerprint %q: %s", want, logs)
	}
	if want := "owner_fingerprint=" + hex.EncodeToString(ownerHash[:]); !strings.Contains(logs, want) {
		t.Fatalf("log missing owner fingerprint %q: %s", want, logs)
	}
}

func awaitRunner(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not stop")
		return nil
	}
}
