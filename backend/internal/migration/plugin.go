package migration

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pressly/goose/v3"
)

var pluginSchemaPart = regexp.MustCompile(`^[a-z][a-z0-9_]{0,61}[a-z0-9]$`)

// PluginSchemaName 把插件 ID 收敛为可安全拼入 DDL 的确定性数据库标识符。
func PluginSchemaName(pluginID string) (string, error) {
	name := "plugin_" + strings.NewReplacer(".", "_", "-", "_").Replace(strings.ToLower(strings.TrimSpace(pluginID)))
	if len(name) > 63 || !pluginSchemaPart.MatchString(name) {
		return "", fmt.Errorf("invalid plugin schema name for %q", pluginID)
	}
	return name, nil
}

// ValidatePluginDirectory 在接触数据库前校验 migration 版本与可回滚结构。
func ValidatePluginDirectory(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read plugin migrations: %w", err)
	}
	hasSQL := false
	versions := make(map[int64]string)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			hasSQL = true
			version, err := goose.NumericComponent(entry.Name())
			if err != nil {
				return fmt.Errorf("validate plugin migration %s: %w", entry.Name(), err)
			}
			if previous := versions[version]; previous != "" {
				return fmt.Errorf("duplicate plugin migration version %d in %s and %s", version, previous, entry.Name())
			}
			versions[version] = entry.Name()
			content, err := os.ReadFile(filepath.Join(directory, entry.Name()))
			if err != nil {
				return fmt.Errorf("read plugin migration %s: %w", entry.Name(), err)
			}
			if !bytes.Contains(content, []byte("-- +goose Up")) || !bytes.Contains(content, []byte("-- +goose Down")) {
				return fmt.Errorf("plugin migration %s must contain goose Up and Down sections", entry.Name())
			}
		}
	}
	if !hasSQL {
		return errors.New("plugin migrations must contain at least one versioned SQL file")
	}
	return nil
}

// WithPluginMigrations 把短生命周期连接池固定为单连接，使插件 DDL 始终落入独立 schema。
func WithPluginMigrations(ctx context.Context, db *sql.DB, pluginID, migrationDir string, fn func(*Runner) error) error {
	schema, err := PluginSchemaName(pluginID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(migrationDir) == "" {
		return errors.New("plugin migration directory is required")
	}
	if _, err := os.Stat(migrationDir); err != nil {
		return fmt.Errorf("plugin migration directory: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+quoteIdentifier(schema)); err != nil {
		return fmt.Errorf("create plugin schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, `SET search_path TO `+quoteIdentifier(schema)+`, public`); err != nil {
		return fmt.Errorf("set plugin search path: %w", err)
	}
	defer func() { _, _ = db.ExecContext(context.Background(), "RESET search_path") }()
	runner, err := newWithFSAndTable(db, os.DirFS(migrationDir), schema+`.schema_migrations`)
	if err != nil {
		return err
	}
	return fn(runner)
}

// DownTo 仅用于把失败的插件安装恢复到操作前版本。
func (r *Runner) DownTo(ctx context.Context, target int64) ([]Result, error) {
	rolledBack, err := r.provider.DownTo(ctx, target)
	results := migrationResults(rolledBack)
	if err != nil {
		return results, fmt.Errorf("roll back plugin migrations: %w", err)
	}
	return results, nil
}

func newWithFSAndTable(db *sql.DB, migrations fs.FS, table string) (*Runner, error) {
	options := []goose.ProviderOption{
		goose.WithTableName(table),
		goose.WithDisableGlobalRegistry(true),
		goose.WithLogger(goose.NopLogger()),
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations, options...)
	if err != nil {
		return nil, fmt.Errorf("create plugin migration provider: %w", err)
	}
	return &Runner{provider: provider, db: db}, nil
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

// RemovePluginSchema 仅供显式 purge-data 和测试清理；正常卸载不得调用。
func RemovePluginSchema(ctx context.Context, db *sql.DB, pluginID string) error {
	schema, err := PluginSchemaName(pluginID)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+quoteIdentifier(schema)+` CASCADE`)
	if err != nil {
		return fmt.Errorf("drop plugin schema: %w", err)
	}
	return nil
}
