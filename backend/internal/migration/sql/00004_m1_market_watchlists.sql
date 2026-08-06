-- +goose Up
CREATE TABLE watchlist_items (
    id UUID NOT NULL,
    owner_user_id BIGINT NOT NULL,
    instrument_id UUID NOT NULL,
    interval_code VARCHAR(4) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT watchlist_items_pkey PRIMARY KEY (id),
    CONSTRAINT fk_watchlist_items_owner
        FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT fk_watchlist_items_instrument
        FOREIGN KEY (instrument_id) REFERENCES market_instruments (id) ON DELETE RESTRICT,
    CONSTRAINT uq_watchlist_items_owner_instrument_interval
        UNIQUE (owner_user_id, instrument_id, interval_code),
    CONSTRAINT ck_watchlist_items_id_uuidv7
        CHECK (id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT ck_watchlist_items_interval
        CHECK (interval_code IN ('1m', '5m', '15m', '1h', '4h', '1d')),
    CONSTRAINT ck_watchlist_items_created_at
        CHECK (isfinite(created_at))
);

CREATE INDEX ix_watchlist_items_instrument_interval
    ON watchlist_items (instrument_id, interval_code);

-- +goose Down
LOCK TABLE watchlist_items IN ACCESS EXCLUSIVE MODE;

CREATE TEMPORARY TABLE m1_market_watchlists_down_guard (
    row_count BIGINT NOT NULL CHECK (row_count = 0)
) ON COMMIT DROP;

INSERT INTO m1_market_watchlists_down_guard (row_count)
SELECT COUNT(*) FROM watchlist_items;

DROP TABLE watchlist_items;
