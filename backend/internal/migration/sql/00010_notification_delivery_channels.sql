-- +goose Up

ALTER TABLE plugin_notification.deliveries
    DROP CONSTRAINT deliveries_operation_key_key,
    DROP CONSTRAINT ck_notification_delivery_channel,
    DROP CONSTRAINT ck_notification_delivery_status,
    DROP CONSTRAINT ck_notification_delivery_time,
    ADD COLUMN recipient_user_id BIGINT REFERENCES users (id) ON DELETE CASCADE,
    ADD COLUMN is_read BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN read_at TIMESTAMPTZ,
    ADD COLUMN last_error_category VARCHAR(64),
    ADD CONSTRAINT ck_notification_delivery_channel CHECK (
        channel IN ('in_app', 'dingtalk', 'qq', 'smtp')
    ),
    ADD CONSTRAINT ck_notification_delivery_status CHECK (
        status IN ('pending', 'delivered', 'failed')
    ),
    ADD CONSTRAINT ck_notification_delivery_time CHECK (
        (status = 'delivered') = (delivered_at IS NOT NULL)
    ),
    ADD CONSTRAINT ck_notification_delivery_recipient CHECK (
        channel = 'in_app' OR recipient_user_id IS NULL
    ),
    ADD CONSTRAINT ck_notification_delivery_read CHECK (
        is_read = (read_at IS NOT NULL) AND (NOT is_read OR channel = 'in_app')
    );

CREATE UNIQUE INDEX ux_notification_delivery_external_operation
    ON plugin_notification.deliveries (operation_key)
    WHERE recipient_user_id IS NULL;

CREATE UNIQUE INDEX ux_notification_delivery_recipient_operation
    ON plugin_notification.deliveries (operation_key, recipient_user_id)
    WHERE recipient_user_id IS NOT NULL;

CREATE INDEX ix_notification_deliveries_recipient_unread
    ON plugin_notification.deliveries (recipient_user_id, id DESC)
    WHERE channel = 'in_app' AND status = 'delivered' AND is_read = FALSE;

CREATE INDEX ix_notification_deliveries_recipient
    ON plugin_notification.deliveries (recipient_user_id, id DESC)
    WHERE channel = 'in_app' AND status = 'delivered';

CREATE INDEX ix_notification_deliveries_channel_status
    ON plugin_notification.deliveries (channel, status, created_at DESC, id DESC);

-- +goose Down

LOCK TABLE plugin_notification.deliveries IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM plugin_notification.deliveries
        WHERE channel <> 'in_app'
           OR status = 'pending'
           OR recipient_user_id IS NOT NULL
           OR is_read
           OR read_at IS NOT NULL
           OR last_error_category IS NOT NULL
        LIMIT 1
    ) THEN
        RAISE EXCEPTION 'refusing to roll back multi-channel notification data';
    END IF;
END
$$;
-- +goose StatementEnd

DROP INDEX plugin_notification.ix_notification_deliveries_channel_status;
DROP INDEX plugin_notification.ix_notification_deliveries_recipient;
DROP INDEX plugin_notification.ix_notification_deliveries_recipient_unread;
DROP INDEX plugin_notification.ux_notification_delivery_recipient_operation;
DROP INDEX plugin_notification.ux_notification_delivery_external_operation;

ALTER TABLE plugin_notification.deliveries
    DROP CONSTRAINT ck_notification_delivery_read,
    DROP CONSTRAINT ck_notification_delivery_recipient,
    DROP CONSTRAINT ck_notification_delivery_time,
    DROP CONSTRAINT ck_notification_delivery_status,
    DROP CONSTRAINT ck_notification_delivery_channel,
    DROP COLUMN last_error_category,
    DROP COLUMN read_at,
    DROP COLUMN is_read,
    DROP COLUMN recipient_user_id,
    ADD CONSTRAINT ck_notification_delivery_channel CHECK (channel = 'in_app'),
    ADD CONSTRAINT ck_notification_delivery_status CHECK (status IN ('delivered', 'failed')),
    ADD CONSTRAINT ck_notification_delivery_time CHECK ((status = 'delivered') = (delivered_at IS NOT NULL)),
    ADD CONSTRAINT deliveries_operation_key_key UNIQUE (operation_key);
