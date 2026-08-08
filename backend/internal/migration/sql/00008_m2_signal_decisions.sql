-- +goose Up
ALTER TABLE strategy_signals
    DROP CONSTRAINT ck_strategy_signals_status,
    ADD COLUMN decision_idempotency_record_id BIGINT,
    ADD COLUMN decided_by_user_id BIGINT,
    ADD COLUMN decided_at TIMESTAMPTZ,
    ADD CONSTRAINT fk_strategy_signals_decision_idempotency
        FOREIGN KEY (decision_idempotency_record_id) REFERENCES idempotency_records (id) ON DELETE RESTRICT,
    ADD CONSTRAINT fk_strategy_signals_decided_by
        FOREIGN KEY (decided_by_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    ADD CONSTRAINT uq_strategy_signals_decision_idempotency UNIQUE (decision_idempotency_record_id),
    ADD CONSTRAINT ck_strategy_signals_status
        CHECK (status IN ('active', 'approved', 'rejected', 'expired')),
    ADD CONSTRAINT ck_strategy_signals_decision_state CHECK (
        (
            status IN ('active', 'expired')
            AND decision_idempotency_record_id IS NULL
            AND decided_by_user_id IS NULL
            AND decided_at IS NULL
        )
        OR
        (
            status IN ('approved', 'rejected')
            AND mode = 'manual'
            AND decision_idempotency_record_id IS NOT NULL
            AND decided_by_user_id = owner_user_id
            AND decided_at IS NOT NULL
            AND isfinite(decided_at)
            AND expires_at IS NOT NULL
            AND decided_at < expires_at
        )
    );

ALTER TABLE notification_deliveries
    ADD COLUMN strategy_signal_id UUID,
    ADD CONSTRAINT fk_notification_deliveries_strategy_signal
        FOREIGN KEY (strategy_signal_id) REFERENCES strategy_signals (id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_notification_deliveries_strategy_signal CHECK (
        strategy_signal_id IS NULL
        OR (
            target_type = 'strategy_signal'
            AND target_id IS NULL
            AND recipient_user_id IS NOT NULL
        )
    );

CREATE UNIQUE INDEX ux_notification_deliveries_in_app_signal
    ON notification_deliveries (strategy_signal_id)
    WHERE strategy_signal_id IS NOT NULL AND channel_type = 'in_app';

CREATE UNIQUE INDEX ux_strategy_signals_manual_active_instance
    ON strategy_signals (strategy_instance_id)
    WHERE mode = 'manual' AND status = 'active';

-- +goose Down
LOCK TABLE strategy_signals, notification_deliveries IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m2_signal_decisions_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m2_signal_decisions_down_guard (row_count)
SELECT
    (SELECT COUNT(*) FROM strategy_signals
        WHERE status IN ('approved', 'rejected')
           OR decision_idempotency_record_id IS NOT NULL
           OR decided_by_user_id IS NOT NULL
           OR decided_at IS NOT NULL)
    + (SELECT COUNT(*) FROM notification_deliveries WHERE strategy_signal_id IS NOT NULL);

DROP INDEX ux_strategy_signals_manual_active_instance;
DROP INDEX ux_notification_deliveries_in_app_signal;
ALTER TABLE notification_deliveries
    DROP CONSTRAINT ck_notification_deliveries_strategy_signal,
    DROP CONSTRAINT fk_notification_deliveries_strategy_signal,
    DROP COLUMN strategy_signal_id;

ALTER TABLE strategy_signals
    DROP CONSTRAINT ck_strategy_signals_decision_state,
    DROP CONSTRAINT ck_strategy_signals_status,
    DROP CONSTRAINT uq_strategy_signals_decision_idempotency,
    DROP CONSTRAINT fk_strategy_signals_decided_by,
    DROP CONSTRAINT fk_strategy_signals_decision_idempotency,
    DROP COLUMN decided_at,
    DROP COLUMN decided_by_user_id,
    DROP COLUMN decision_idempotency_record_id,
    ADD CONSTRAINT ck_strategy_signals_status CHECK (status IN ('active', 'expired'));
