-- +goose Up

CREATE EXTENSION IF NOT EXISTS timescaledb WITH SCHEMA public;

CREATE TABLE roles (
    id BIGSERIAL PRIMARY KEY,
    display_name VARCHAR(100) NOT NULL DEFAULT '',
    code VARCHAR(50) NOT NULL,
    description VARCHAR(255) NOT NULL DEFAULT '',
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    is_system BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_roles_code CHECK (code <> '')
);
CREATE UNIQUE INDEX idx_roles_code ON roles (code);

CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY,
    username VARCHAR(100) NOT NULL,
    password_hash VARCHAR(255) NOT NULL DEFAULT '',
    nickname VARCHAR(100) NOT NULL DEFAULT '',
    full_name VARCHAR(100) NOT NULL DEFAULT '',
    gender VARCHAR(20) NOT NULL DEFAULT 'unknown',
    phone VARCHAR(32) NOT NULL DEFAULT '',
    email VARCHAR(150) NOT NULL DEFAULT '',
    avatar VARCHAR(500) NOT NULL DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    job_title VARCHAR(100) NOT NULL DEFAULT '',
    location VARCHAR(120) NOT NULL DEFAULT '',
    company VARCHAR(120) NOT NULL DEFAULT '',
    bio TEXT NOT NULL DEFAULT '',
    tags_json TEXT NOT NULL DEFAULT '',
    created_by VARCHAR(100) NOT NULL DEFAULT 'system',
    updated_by VARCHAR(100) NOT NULL DEFAULT 'system',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login_at TIMESTAMPTZ,
    CONSTRAINT ck_users_username CHECK (username <> '')
);
CREATE UNIQUE INDEX idx_users_username ON users (username);

CREATE TABLE user_roles (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role_id BIGINT NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX ux_user_role ON user_roles (user_id, role_id);

CREATE TABLE menus (
    id BIGSERIAL PRIMARY KEY,
    parent_id BIGINT REFERENCES menus (id) ON DELETE CASCADE,
    path VARCHAR(255) NOT NULL DEFAULT '',
    name VARCHAR(100) NOT NULL,
    permission_code VARCHAR(120),
    component VARCHAR(255) NOT NULL DEFAULT '',
    title VARCHAR(100) NOT NULL DEFAULT '',
    icon VARCHAR(100) NOT NULL DEFAULT '',
    menu_type VARCHAR(20) NOT NULL DEFAULT 'menu',
    external_url VARCHAR(500) NOT NULL DEFAULT '',
    active_menu_path VARCHAR(255) NOT NULL DEFAULT '',
    sort BIGINT NOT NULL DEFAULT 0,
    keep_alive BOOLEAN NOT NULL DEFAULT FALSE,
    is_hidden BOOLEAN NOT NULL DEFAULT FALSE,
    is_hide_tab BOOLEAN NOT NULL DEFAULT FALSE,
    is_full_screen BOOLEAN NOT NULL DEFAULT FALSE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    use_iframe BOOLEAN NOT NULL DEFAULT FALSE,
    fixed_tab BOOLEAN NOT NULL DEFAULT FALSE,
    badge_label VARCHAR(50) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_menus_name CHECK (name <> '')
);
CREATE UNIQUE INDEX idx_menus_name ON menus (name);
CREATE INDEX idx_menus_permission_code ON menus (permission_code);

CREATE TABLE menu_buttons (
    id BIGSERIAL PRIMARY KEY,
    menu_id BIGINT NOT NULL REFERENCES menus (id) ON DELETE CASCADE,
    title VARCHAR(100) NOT NULL DEFAULT '',
    permission_code VARCHAR(120) NOT NULL,
    sort BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_menu_buttons_permission_code CHECK (permission_code <> '')
);
CREATE UNIQUE INDEX idx_menu_buttons_permission_code ON menu_buttons (permission_code);

CREATE TABLE role_menus (
    id BIGSERIAL PRIMARY KEY,
    role_id BIGINT NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    menu_id BIGINT NOT NULL REFERENCES menus (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX ux_role_menu ON role_menus (role_id, menu_id);

CREATE TABLE role_menu_buttons (
    id BIGSERIAL PRIMARY KEY,
    role_id BIGINT NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    button_id BIGINT NOT NULL REFERENCES menu_buttons (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX ux_role_button ON role_menu_buttons (role_id, button_id);

CREATE TABLE i18n_texts (
    id BIGSERIAL PRIMARY KEY,
    biz_type VARCHAR(20) NOT NULL,
    biz_id BIGINT NOT NULL,
    i18n_key VARCHAR(255) NOT NULL,
    locale VARCHAR(10) NOT NULL,
    text VARCHAR(255) NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_i18n_texts_biz_type CHECK (biz_type IN ('menu', 'button')),
    CONSTRAINT ck_i18n_texts_locale CHECK (locale IN ('zh', 'en'))
);
CREATE UNIQUE INDEX ux_i18n_key_locale ON i18n_texts (i18n_key, locale);
CREATE UNIQUE INDEX ux_i18n_biz ON i18n_texts (biz_type, biz_id, locale);

CREATE TABLE audit_records (
    id BIGSERIAL PRIMARY KEY,
    request_id VARCHAR(64) NOT NULL,
    actor_user_id BIGINT REFERENCES users (id) ON DELETE SET NULL,
    action VARCHAR(255) NOT NULL,
    resource_path VARCHAR(500) NOT NULL,
    outcome VARCHAR(16) NOT NULL,
    status_code INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_audit_records_request_id CHECK (request_id ~ '^[A-Za-z0-9._-]{1,64}$'),
    CONSTRAINT ck_audit_records_outcome CHECK (outcome IN ('success', 'failure')),
    CONSTRAINT ck_audit_records_status_code CHECK (status_code BETWEEN 100 AND 599)
);
CREATE INDEX ix_audit_records_created_at ON audit_records (created_at DESC, id DESC);
CREATE INDEX ix_audit_records_actor_created_at ON audit_records (actor_user_id, created_at DESC, id DESC);
CREATE INDEX ix_audit_records_request_id ON audit_records (request_id);

-- +goose Down

LOCK TABLE
    roles,
    users,
    user_roles,
    menus,
    menu_buttons,
    role_menus,
    role_menu_buttons,
    i18n_texts,
    audit_records
IN ACCESS EXCLUSIVE MODE;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM roles LIMIT 1)
        OR EXISTS (SELECT 1 FROM users LIMIT 1)
        OR EXISTS (SELECT 1 FROM user_roles LIMIT 1)
        OR EXISTS (SELECT 1 FROM menus LIMIT 1)
        OR EXISTS (SELECT 1 FROM menu_buttons LIMIT 1)
        OR EXISTS (SELECT 1 FROM role_menus LIMIT 1)
        OR EXISTS (SELECT 1 FROM role_menu_buttons LIMIT 1)
        OR EXISTS (SELECT 1 FROM i18n_texts LIMIT 1)
        OR EXISTS (SELECT 1 FROM audit_records LIMIT 1)
    THEN
        RAISE EXCEPTION 'refusing to roll back a non-empty V2 baseline';
    END IF;
END
$$;
-- +goose StatementEnd

DROP TABLE audit_records;
DROP TABLE i18n_texts;
DROP TABLE role_menu_buttons;
DROP TABLE role_menus;
DROP TABLE menu_buttons;
DROP TABLE menus;
DROP TABLE user_roles;
DROP TABLE users;
DROP TABLE roles;
