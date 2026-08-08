-- +goose Up
CREATE UNIQUE INDEX ux_notification_deliveries_signal_channel
    ON notification_deliveries (strategy_signal_id, channel_id)
    WHERE strategy_signal_id IS NOT NULL AND channel_id IS NOT NULL;

-- +goose Down
DROP INDEX ux_notification_deliveries_signal_channel;
