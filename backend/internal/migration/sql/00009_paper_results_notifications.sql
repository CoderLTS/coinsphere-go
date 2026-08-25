-- +goose Up

CREATE TABLE result_views (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(120) NOT NULL,
    plugin_id VARCHAR(128) NOT NULL,
    page_key VARCHAR(128) NOT NULL,
    scope_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    filters_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    allowed_actions JSONB NOT NULL DEFAULT '[]'::jsonb,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    created_by BIGINT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at TIMESTAMPTZ,
    CONSTRAINT ck_result_views_identity CHECK (
        BTRIM(name) <> '' AND BTRIM(plugin_id) <> '' AND BTRIM(page_key) <> ''
    ),
    CONSTRAINT ck_result_views_json CHECK (
        jsonb_typeof(scope_json) = 'object'
        AND jsonb_typeof(filters_json) = 'object'
        AND jsonb_typeof(allowed_actions) = 'array'
    ),
    CONSTRAINT ck_result_views_status CHECK (status IN ('active', 'revoked')),
    CONSTRAINT ck_result_views_revoked CHECK ((status = 'revoked') = (revoked_at IS NOT NULL))
);
CREATE INDEX ix_result_views_status ON result_views (status, created_at DESC, id DESC);

CREATE TABLE result_view_user_grants (
    view_id BIGINT NOT NULL REFERENCES result_views (id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (view_id, user_id)
);
CREATE INDEX ix_result_view_user_grants_user ON result_view_user_grants (user_id, view_id);

CREATE TABLE result_view_role_grants (
    view_id BIGINT NOT NULL REFERENCES result_views (id) ON DELETE CASCADE,
    role_id BIGINT NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (view_id, role_id)
);
CREATE INDEX ix_result_view_role_grants_role ON result_view_role_grants (role_id, view_id);

-- +goose StatementBegin
CREATE FUNCTION protect_result_view_scope() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.plugin_id <> NEW.plugin_id OR OLD.page_key <> NEW.page_key
        OR OLD.scope_json <> NEW.scope_json OR OLD.filters_json <> NEW.filters_json
        OR OLD.allowed_actions <> NEW.allowed_actions
        OR OLD.created_by <> NEW.created_by OR OLD.created_at <> NEW.created_at
    THEN
        RAISE EXCEPTION 'result view scope is immutable';
    END IF;
    RETURN NEW;
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_result_views_immutable
BEFORE UPDATE ON result_views
FOR EACH ROW EXECUTE FUNCTION protect_result_view_scope();

CREATE TABLE plugin_quant.signals (
    id BIGSERIAL PRIMARY KEY,
    operation_key VARCHAR(64) NOT NULL UNIQUE,
    paper_operation_key VARCHAR(64) UNIQUE,
    workflow_id BIGINT NOT NULL,
    revision_id BIGINT NOT NULL,
    node_instance_id VARCHAR(128) NOT NULL,
    strategy_id VARCHAR(128) NOT NULL,
    strategy_version VARCHAR(32) NOT NULL,
    market VARCHAR(8) NOT NULL,
    instrument VARCHAR(32) NOT NULL,
    business_key VARCHAR(256) NOT NULL,
    target NUMERIC(38,18) NOT NULL,
    evaluated_at TIMESTAMPTZ NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    decision_task_id BIGINT,
    superseded_by BIGINT REFERENCES plugin_quant.signals (id) ON DELETE RESTRICT,
    rejection_reason VARCHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    decided_at TIMESTAMPTZ,
    executed_at TIMESTAMPTZ,
    CONSTRAINT ck_quant_signal_identity CHECK (
        BTRIM(node_instance_id) <> '' AND BTRIM(strategy_id) <> ''
        AND BTRIM(instrument) <> '' AND BTRIM(business_key) <> ''
    ),
    CONSTRAINT ck_quant_signal_market CHECK (market IN ('spot', 'usdm')),
    CONSTRAINT ck_quant_signal_target CHECK (target BETWEEN -1 AND 1),
    CONSTRAINT ck_quant_signal_status CHECK (status IN ('pending', 'superseded', 'approved', 'rejected', 'executed')),
    CONSTRAINT ck_quant_signal_times CHECK (
        (status = 'pending' AND decided_at IS NULL AND executed_at IS NULL)
        OR (status = 'superseded' AND decided_at IS NOT NULL AND executed_at IS NULL)
        OR (status IN ('approved', 'rejected') AND decided_at IS NOT NULL AND executed_at IS NULL)
        OR (status = 'executed' AND decided_at IS NOT NULL AND executed_at IS NOT NULL)
    )
);
CREATE UNIQUE INDEX ux_quant_signal_pending_business
    ON plugin_quant.signals (workflow_id, node_instance_id, business_key)
    WHERE status = 'pending';
CREATE INDEX ix_quant_signals_scope
    ON plugin_quant.signals (market, instrument, created_at DESC, id DESC);

CREATE TABLE plugin_quant.paper_accounts (
    id BIGSERIAL PRIMARY KEY,
    workflow_id BIGINT NOT NULL,
    node_instance_id VARCHAR(128) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    initial_balance NUMERIC(38,18) NOT NULL,
    cash_balance NUMERIC(38,18) NOT NULL,
    equity NUMERIC(38,18) NOT NULL,
    peak_equity NUMERIC(38,18) NOT NULL,
    day_start_equity NUMERIC(38,18) NOT NULL,
    day_start_date DATE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ux_quant_paper_account_node UNIQUE (workflow_id, node_instance_id),
    CONSTRAINT ck_quant_paper_account_identity CHECK (BTRIM(node_instance_id) <> ''),
    CONSTRAINT ck_quant_paper_account_status CHECK (status IN ('active', 'paused')),
    CONSTRAINT ck_quant_paper_account_values CHECK (
        initial_balance > 0 AND peak_equity >= 0 AND day_start_equity >= 0
    )
);

CREATE TABLE plugin_quant.paper_orders (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES plugin_quant.paper_accounts (id) ON DELETE RESTRICT,
    signal_id BIGINT NOT NULL REFERENCES plugin_quant.signals (id) ON DELETE RESTRICT,
    operation_key VARCHAR(64) NOT NULL UNIQUE,
    market VARCHAR(8) NOT NULL,
    instrument VARCHAR(32) NOT NULL,
    side VARCHAR(4) NOT NULL,
    quantity NUMERIC(38,18) NOT NULL,
    quote_price NUMERIC(38,18) NOT NULL,
    notional NUMERIC(38,18) NOT NULL,
    status VARCHAR(16) NOT NULL,
    quoted_at TIMESTAMPTZ NOT NULL,
    cash_after NUMERIC(38,18) NOT NULL,
    equity_after NUMERIC(38,18) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ux_quant_paper_order_signal UNIQUE (signal_id),
    CONSTRAINT ck_quant_paper_order_market CHECK (market IN ('spot', 'usdm')),
    CONSTRAINT ck_quant_paper_order_side CHECK (side IN ('buy', 'sell')),
    CONSTRAINT ck_quant_paper_order_status CHECK (status = 'filled'),
    CONSTRAINT ck_quant_paper_order_values CHECK (quantity > 0 AND quote_price > 0 AND notional > 0 AND equity_after >= 0)
);
CREATE INDEX ix_quant_paper_orders_account ON plugin_quant.paper_orders (account_id, created_at DESC, id DESC);

CREATE TABLE plugin_quant.paper_fills (
    id BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL UNIQUE REFERENCES plugin_quant.paper_orders (id) ON DELETE RESTRICT,
    operation_key VARCHAR(64) NOT NULL UNIQUE,
    quantity_delta NUMERIC(38,18) NOT NULL,
    price NUMERIC(38,18) NOT NULL,
    notional NUMERIC(38,18) NOT NULL,
    filled_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT ck_quant_paper_fill_values CHECK (quantity_delta <> 0 AND price > 0 AND notional > 0)
);

CREATE TABLE plugin_quant.paper_fees (
    id BIGSERIAL PRIMARY KEY,
    fill_id BIGINT NOT NULL UNIQUE REFERENCES plugin_quant.paper_fills (id) ON DELETE RESTRICT,
    operation_key VARCHAR(64) NOT NULL UNIQUE,
    amount NUMERIC(38,18) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_quant_paper_fee_amount CHECK (amount >= 0)
);

CREATE TABLE plugin_quant.paper_ledger_entries (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES plugin_quant.paper_accounts (id) ON DELETE RESTRICT,
    operation_key VARCHAR(64) NOT NULL,
    entry_type VARCHAR(16) NOT NULL,
    amount NUMERIC(38,18) NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT ux_quant_paper_ledger_operation UNIQUE (operation_key, entry_type),
    CONSTRAINT ck_quant_paper_ledger_type CHECK (entry_type IN ('trade_cash', 'fee'))
);
CREATE INDEX ix_quant_paper_ledger_account
    ON plugin_quant.paper_ledger_entries (account_id, occurred_at, id);

CREATE TABLE plugin_quant.paper_positions (
    account_id BIGINT NOT NULL REFERENCES plugin_quant.paper_accounts (id) ON DELETE RESTRICT,
    market VARCHAR(8) NOT NULL,
    instrument VARCHAR(32) NOT NULL,
    quantity NUMERIC(38,18) NOT NULL,
    average_price NUMERIC(38,18) NOT NULL,
    last_price NUMERIC(38,18) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (account_id, market, instrument),
    CONSTRAINT ck_quant_paper_position_market CHECK (market IN ('spot', 'usdm')),
    CONSTRAINT ck_quant_paper_position_prices CHECK (average_price >= 0 AND last_price > 0)
);

-- +goose StatementBegin
CREATE FUNCTION plugin_quant.protect_paper_fact() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'Paper facts are immutable';
END
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_quant_paper_orders_immutable
BEFORE UPDATE OR DELETE ON plugin_quant.paper_orders
FOR EACH ROW EXECUTE FUNCTION plugin_quant.protect_paper_fact();
CREATE TRIGGER trg_quant_paper_fills_immutable
BEFORE UPDATE OR DELETE ON plugin_quant.paper_fills
FOR EACH ROW EXECUTE FUNCTION plugin_quant.protect_paper_fact();
CREATE TRIGGER trg_quant_paper_fees_immutable
BEFORE UPDATE OR DELETE ON plugin_quant.paper_fees
FOR EACH ROW EXECUTE FUNCTION plugin_quant.protect_paper_fact();
CREATE TRIGGER trg_quant_paper_ledger_immutable
BEFORE UPDATE OR DELETE ON plugin_quant.paper_ledger_entries
FOR EACH ROW EXECUTE FUNCTION plugin_quant.protect_paper_fact();

CREATE SCHEMA plugin_notification;

CREATE TABLE plugin_notification.deliveries (
    id BIGSERIAL PRIMARY KEY,
    operation_key VARCHAR(64) NOT NULL UNIQUE,
    workflow_id BIGINT NOT NULL,
    revision_id BIGINT NOT NULL,
    node_instance_id VARCHAR(128) NOT NULL,
    channel VARCHAR(32) NOT NULL,
    subject_key VARCHAR(256) NOT NULL,
    title VARCHAR(160) NOT NULL,
    message VARCHAR(2000) NOT NULL,
    status VARCHAR(16) NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 1,
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_notification_delivery_identity CHECK (
        BTRIM(node_instance_id) <> '' AND BTRIM(subject_key) <> ''
        AND BTRIM(title) <> '' AND BTRIM(message) <> ''
    ),
    CONSTRAINT ck_notification_delivery_channel CHECK (channel = 'in_app'),
    CONSTRAINT ck_notification_delivery_status CHECK (status IN ('delivered', 'failed')),
    CONSTRAINT ck_notification_delivery_attempts CHECK (attempt_count BETWEEN 1 AND 100),
    CONSTRAINT ck_notification_delivery_time CHECK ((status = 'delivered') = (delivered_at IS NOT NULL))
);
CREATE INDEX ix_notification_deliveries_status
    ON plugin_notification.deliveries (status, created_at DESC, id DESC);

-- +goose Down

LOCK TABLE
    result_views,
    result_view_user_grants,
    result_view_role_grants,
    plugin_quant.signals,
    plugin_quant.paper_accounts,
    plugin_quant.paper_orders,
    plugin_quant.paper_fills,
    plugin_quant.paper_fees,
    plugin_quant.paper_ledger_entries,
    plugin_quant.paper_positions,
    plugin_notification.deliveries
IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM result_views LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_quant.signals LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_quant.paper_accounts LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_quant.paper_orders LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_quant.paper_fills LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_quant.paper_fees LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_quant.paper_ledger_entries LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_quant.paper_positions LIMIT 1)
        OR EXISTS (SELECT 1 FROM plugin_notification.deliveries LIMIT 1)
    THEN
        RAISE EXCEPTION 'refusing to roll back Paper, ResultView, or Notification data';
    END IF;
END
$$;
-- +goose StatementEnd

DROP SCHEMA plugin_notification CASCADE;
DROP FUNCTION plugin_quant.protect_paper_fact() CASCADE;
DROP TABLE plugin_quant.paper_positions;
DROP TABLE plugin_quant.paper_ledger_entries;
DROP TABLE plugin_quant.paper_fees;
DROP TABLE plugin_quant.paper_fills;
DROP TABLE plugin_quant.paper_orders;
DROP TABLE plugin_quant.paper_accounts;
DROP TABLE plugin_quant.signals;
DROP TRIGGER trg_result_views_immutable ON result_views;
DROP FUNCTION protect_result_view_scope();
DROP TABLE result_view_role_grants;
DROP TABLE result_view_user_grants;
DROP TABLE result_views;
