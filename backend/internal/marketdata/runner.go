package marketdata

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrFlowLeaseLost 表示当前进程已无法确认自己仍拥有行情流的写入权。
var ErrFlowLeaseLost = errors.New("market flow lease lost")

// FlowLease 是由 PostgreSQL 时钟裁定的单条行情流租约。
type FlowLease struct {
	FlowKey         string
	OwnerID         string
	FencingToken    int64
	LeaseExpiresAt  time.Time
	LastHeartbeatAt time.Time
}

// Subscription 在传入 Context 存活期间消费单条行情流。
type Subscription func(context.Context) error

// FlowRunner 在持有租约时维持订阅和续租，失租即停止订阅。
type FlowRunner struct {
	store              *PostgresStore
	ownerID            string
	leaseDuration      time.Duration
	unavailableBackoff time.Duration
}

// ClaimFlowLease 原子认领已过期或尚不存在的租约；冲突但未过期时返回 false。
func (store *PostgresStore) ClaimFlowLease(ctx context.Context, flowKey, ownerID string, leaseDuration time.Duration) (FlowLease, bool, error) {
	flowKey, err := normalizeFlowKey(flowKey)
	if err != nil {
		return FlowLease{}, false, err
	}
	if err := validateOwnerID(ownerID); err != nil {
		return FlowLease{}, false, err
	}
	leaseSeconds, err := leaseDurationSeconds(leaseDuration)
	if err != nil {
		return FlowLease{}, false, err
	}

	var lease FlowLease
	err = store.db.QueryRowContext(ctx, `
INSERT INTO market_flow_leases (
    flow_key, owner_id, fencing_token, lease_expires_at, last_heartbeat_at, created_at, updated_at
) VALUES (
    $1, $2, 1,
    statement_timestamp() + $3::bigint * INTERVAL '1 second',
    statement_timestamp(), statement_timestamp(), statement_timestamp()
)
ON CONFLICT (flow_key) DO UPDATE SET
    owner_id = EXCLUDED.owner_id,
    fencing_token = market_flow_leases.fencing_token + 1,
    lease_expires_at = statement_timestamp() + $3::bigint * INTERVAL '1 second',
    last_heartbeat_at = statement_timestamp(),
    updated_at = statement_timestamp()
WHERE market_flow_leases.lease_expires_at <= statement_timestamp()
RETURNING flow_key, owner_id, fencing_token, lease_expires_at, last_heartbeat_at
`, flowKey, ownerID, leaseSeconds).Scan(
		&lease.FlowKey,
		&lease.OwnerID,
		&lease.FencingToken,
		&lease.LeaseExpiresAt,
		&lease.LastHeartbeatAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return FlowLease{}, false, nil
	}
	if err != nil {
		return FlowLease{}, false, err
	}
	lease.LeaseExpiresAt = lease.LeaseExpiresAt.UTC()
	lease.LastHeartbeatAt = lease.LastHeartbeatAt.UTC()
	return lease, true, nil
}

// RenewFlowLease 只允许当前 owner 使用未过期的 fencing token 延长租约。
func (store *PostgresStore) RenewFlowLease(ctx context.Context, lease FlowLease, leaseDuration time.Duration) (bool, error) {
	flowKey, err := validateLeaseIdentity(lease)
	if err != nil {
		return false, err
	}
	leaseSeconds, err := leaseDurationSeconds(leaseDuration)
	if err != nil {
		return false, err
	}

	result, err := store.db.ExecContext(ctx, `
UPDATE market_flow_leases
SET
    lease_expires_at = statement_timestamp() + $4::bigint * INTERVAL '1 second',
    last_heartbeat_at = statement_timestamp(),
    updated_at = statement_timestamp()
WHERE flow_key = $1
  AND owner_id = $2
  AND fencing_token = $3
  AND lease_expires_at > statement_timestamp()
`, flowKey, lease.OwnerID, lease.FencingToken, leaseSeconds)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return updated == 1, nil
}

// ReleaseFlowLease 保留 fencing token，仅使当前持有者的租约立即到期。
func (store *PostgresStore) ReleaseFlowLease(ctx context.Context, lease FlowLease) (bool, error) {
	flowKey, err := validateLeaseIdentity(lease)
	if err != nil {
		return false, err
	}

	result, err := store.db.ExecContext(ctx, `
UPDATE market_flow_leases
SET lease_expires_at = statement_timestamp(), updated_at = statement_timestamp()
WHERE flow_key = $1
  AND owner_id = $2
  AND fencing_token = $3
  AND lease_expires_at > statement_timestamp()
`, flowKey, lease.OwnerID, lease.FencingToken)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return updated == 1, nil
}

// NewFlowRunner 固定单个 owner 的续租与瞬时错误退避策略。
func NewFlowRunner(store *PostgresStore, ownerID string, leaseDuration, unavailableBackoff time.Duration) (*FlowRunner, error) {
	if store == nil || store.db == nil {
		return nil, errors.New("postgres store is required")
	}
	if err := validateOwnerID(ownerID); err != nil {
		return nil, err
	}
	leaseSeconds, err := leaseDurationSeconds(leaseDuration)
	if err != nil {
		return nil, err
	}
	if unavailableBackoff <= 0 {
		return nil, errors.New("unavailable backoff must be positive")
	}
	return &FlowRunner{
		store:              store,
		ownerID:            ownerID,
		leaseDuration:      time.Duration(leaseSeconds) * time.Second,
		unavailableBackoff: unavailableBackoff,
	}, nil
}

// Run 在租约有效时运行订阅；短暂来源错误不会释放已有租约。
func (runner *FlowRunner) Run(ctx context.Context, flowKey string, subscribe Subscription) error {
	if runner == nil || runner.store == nil {
		return errors.New("flow runner is required")
	}
	if ctx == nil {
		return errors.New("context is required")
	}
	if subscribe == nil {
		return errors.New("subscription is required")
	}
	flowKey, err := normalizeFlowKey(flowKey)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return contextError(ctx)
	}

	for {
		lease, claimed, err := runner.store.ClaimFlowLease(ctx, flowKey, runner.ownerID, runner.leaseDuration)
		if err != nil {
			if ctx.Err() != nil {
				return contextError(ctx)
			}
			slog.ErrorContext(ctx, "market flow lease claim failed", "flow", flowKey, "owner", runner.ownerID, "error_category", "database")
			return ErrFlowLeaseLost
		}
		if !claimed {
			if err := waitForContext(ctx, runner.unavailableBackoff); err != nil {
				return err
			}
			continue
		}

		slog.InfoContext(ctx, "market flow lease claimed", "flow", lease.FlowKey, "owner", lease.OwnerID, "token", lease.FencingToken, "status", "claimed")
		result := contextError(ctx)
		if ctx.Err() == nil {
			result = runner.runLease(ctx, lease, subscribe)
		}

		// 调用方取消后仍尝试 fenced Release，但收尾不能无限阻塞运行时退出。
		releaseCtx, cancelRelease := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		released, releaseErr := runner.store.ReleaseFlowLease(releaseCtx, lease)
		cancelRelease()
		if releaseErr != nil {
			slog.ErrorContext(ctx, "market flow lease release failed", "flow", lease.FlowKey, "owner", lease.OwnerID, "token", lease.FencingToken, "error_category", "database")
		} else if !released {
			slog.WarnContext(ctx, "market flow lease release lost", "flow", lease.FlowKey, "owner", lease.OwnerID, "token", lease.FencingToken, "error_category", "lease_lost")
		}
		slog.InfoContext(ctx, "market flow runner stopped", "flow", lease.FlowKey, "owner", lease.OwnerID, "token", lease.FencingToken, "status", "stopped")
		return result
	}
}

func (runner *FlowRunner) runLease(ctx context.Context, lease FlowLease, subscribe Subscription) error {
	renewal := time.NewTicker(runner.leaseDuration / 3)
	defer renewal.Stop()

	for {
		subscriptionCtx, cancelSubscription := context.WithCancelCause(ctx)
		completed := make(chan error, 1)
		go func() {
			completed <- subscribe(subscriptionCtx)
		}()

		err := runner.waitForSubscription(ctx, subscriptionCtx, lease, completed, cancelSubscription, renewal.C)
		cancelSubscription(nil)
		if errors.Is(err, ErrFlowLeaseLost) || errors.Is(context.Cause(subscriptionCtx), ErrFlowLeaseLost) {
			return ErrFlowLeaseLost
		}
		if ctx.Err() != nil {
			return contextError(ctx)
		}

		delay, category, retry := runner.retryDelay(err)
		if !retry {
			return err
		}
		slog.WarnContext(ctx, "market flow subscription retry", "flow", lease.FlowKey, "owner", lease.OwnerID, "token", lease.FencingToken, "error_category", category, "backoff", delay)
		if err := runner.waitForRetry(ctx, lease, renewal.C, delay); err != nil {
			return err
		}
	}
}

func (runner *FlowRunner) waitForSubscription(ctx, subscriptionCtx context.Context, lease FlowLease, completed <-chan error, cancelSubscription context.CancelCauseFunc, renewal <-chan time.Time) error {
	for {
		select {
		case err := <-completed:
			return err
		case <-ctx.Done():
			cancelSubscription(contextError(ctx))
			<-completed
			return contextError(ctx)
		case <-renewal:
			if err := runner.renewLease(ctx, lease); err != nil {
				if ctx.Err() != nil {
					cancelSubscription(contextError(ctx))
					<-completed
					return contextError(ctx)
				}
				cancelSubscription(ErrFlowLeaseLost)
				<-completed
				return ErrFlowLeaseLost
			}
			if errors.Is(context.Cause(subscriptionCtx), ErrFlowLeaseLost) {
				return ErrFlowLeaseLost
			}
		}
	}
}

func (runner *FlowRunner) waitForRetry(ctx context.Context, lease FlowLease, renewal <-chan time.Time, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return contextError(ctx)
		case <-timer.C:
			return nil
		case <-renewal:
			if err := runner.renewLease(ctx, lease); err != nil {
				if ctx.Err() != nil {
					return contextError(ctx)
				}
				return ErrFlowLeaseLost
			}
		}
	}
}

func (runner *FlowRunner) renewLease(ctx context.Context, lease FlowLease) error {
	// 续租查询必须在下一次心跳前结束；超时后不再假定本进程仍安全持有流。
	renewCtx, cancelRenew := context.WithTimeout(ctx, runner.leaseDuration/3)
	defer cancelRenew()
	renewed, err := runner.store.RenewFlowLease(renewCtx, lease, runner.leaseDuration)
	if err != nil {
		if ctx.Err() != nil {
			return contextError(ctx)
		}
		slog.ErrorContext(ctx, "market flow lease renewal failed", "flow", lease.FlowKey, "owner", lease.OwnerID, "token", lease.FencingToken, "error_category", "database")
		return ErrFlowLeaseLost
	}
	if !renewed {
		slog.WarnContext(ctx, "market flow lease lost", "flow", lease.FlowKey, "owner", lease.OwnerID, "token", lease.FencingToken, "error_category", "lease_lost")
		return ErrFlowLeaseLost
	}
	return nil
}

func (runner *FlowRunner) retryDelay(err error) (time.Duration, SourceErrorKind, bool) {
	var sourcePointer *SourceError
	if errors.As(err, &sourcePointer) && sourcePointer != nil {
		return retryDelayForSource(*sourcePointer, runner.unavailableBackoff)
	}
	var sourceValue SourceError
	if errors.As(err, &sourceValue) {
		return retryDelayForSource(sourceValue, runner.unavailableBackoff)
	}
	return 0, "", false
}

func retryDelayForSource(source SourceError, unavailableBackoff time.Duration) (time.Duration, SourceErrorKind, bool) {
	if ValidateSourceError(source) != nil {
		return 0, "", false
	}
	switch source.Kind {
	case SourceErrorRateLimited:
		if source.RetryAfter > 0 {
			return source.RetryAfter, source.Kind, true
		}
		return unavailableBackoff, source.Kind, true
	case SourceErrorUnavailable:
		return unavailableBackoff, source.Kind, true
	default:
		return 0, source.Kind, false
	}
}

func normalizeFlowKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || value == "" || utf8.RuneCountInString(value) > 200 {
		return "", errors.New("invalid flow key")
	}
	return value, nil
}

func validateOwnerID(value string) error {
	if !utf8.ValidString(value) || value != strings.TrimSpace(value) || value == "" || utf8.RuneCountInString(value) > 120 {
		return errors.New("invalid owner ID")
	}
	return nil
}

func validateLeaseIdentity(lease FlowLease) (string, error) {
	flowKey, err := normalizeFlowKey(lease.FlowKey)
	if err != nil {
		return "", err
	}
	if err := validateOwnerID(lease.OwnerID); err != nil {
		return "", err
	}
	if lease.FencingToken <= 0 {
		return "", errors.New("invalid fencing token")
	}
	return flowKey, nil
}

func leaseDurationSeconds(value time.Duration) (int64, error) {
	if value <= 0 {
		return 0, errors.New("lease duration must be positive")
	}
	seconds := int64(value / time.Second)
	if value%time.Second != 0 {
		seconds++
	}
	return seconds, nil
}

func waitForContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return contextError(ctx)
	case <-timer.C:
		return nil
	}
}

func contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return context.Cause(ctx)
}
