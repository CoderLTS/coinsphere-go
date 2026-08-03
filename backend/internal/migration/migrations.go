// Package migration owns the versioned SQL migration runner used by release tooling.
package migration

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

const versionTable = "schema_migrations"

//go:embed sql/*.sql
var embeddedSQL embed.FS

// Runner applies the immutable SQL migrations bundled into the backend binary.
type Runner struct {
	provider *goose.Provider
	db       *sql.DB
}

// Result describes one migration applied by an up or down operation.
type Result struct {
	Version   int64
	Direction string
	Duration  time.Duration
}

// Status describes whether one known migration is applied or pending.
type Status struct {
	Version   int64
	Name      string
	State     string
	AppliedAt time.Time
}

// New creates a runner for the bundled production migrations.
func New(db *sql.DB) (*Runner, error) {
	f, err := fs.Sub(embeddedSQL, "sql")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}
	return newWithFS(db, f)
}

func newWithFS(db *sql.DB, migrations fs.FS) (*Runner, error) {
	options := []goose.ProviderOption{
		goose.WithTableName(versionTable),
		goose.WithDisableGlobalRegistry(true),
		goose.WithLogger(goose.NopLogger()),
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("create postgres migration lock: %w", err)
	}
	options = append(options, goose.WithSessionLocker(locker))

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations, options...)
	if err != nil {
		return nil, fmt.Errorf("create migration provider: %w", err)
	}
	return &Runner{provider: provider, db: db}, nil
}

// Up applies all pending migrations, or stops at target when target is greater than zero.
func (r *Runner) Up(ctx context.Context, target int64) ([]Result, error) {
	if target < 0 {
		return nil, errors.New("target version must be zero or greater")
	}
	current, latest, err := r.ensureDatabaseNotAhead(ctx)
	if err != nil {
		return nil, err
	}
	if target > 0 {
		if target < current {
			return nil, fmt.Errorf("target version %d is below current version %d; use a down migration", target, current)
		}
		if target > latest {
			return nil, fmt.Errorf("target version %d exceeds latest bundled version %d", target, latest)
		}
	}

	var applied []*goose.MigrationResult
	if target == 0 {
		applied, err = r.provider.Up(ctx)
	} else {
		applied, err = r.provider.UpTo(ctx, target)
	}
	results := migrationResults(applied)
	if err != nil {
		var partial *goose.PartialError
		if errors.As(err, &partial) {
			results = migrationResults(partial.Applied)
		}
		return results, fmt.Errorf("apply up migrations after %d successful steps: %w", len(results), err)
	}
	return results, nil
}

// Down attempts to roll back exactly steps applied migrations in one provider operation.
func (r *Runner) Down(ctx context.Context, steps int) ([]Result, error) {
	if steps < 1 {
		return nil, errors.New("down steps must be at least one")
	}
	if _, _, err := r.ensureDatabaseNotAhead(ctx); err != nil {
		return nil, err
	}
	statuses, err := r.provider.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("read migration status before rollback: %w", err)
	}
	appliedVersions := make([]int64, 0, len(statuses))
	for _, status := range statuses {
		if status.State == goose.StateApplied {
			appliedVersions = append(appliedVersions, status.Source.Version)
		}
	}
	appliedCount := len(appliedVersions)
	if appliedCount < steps {
		return nil, fmt.Errorf("cannot roll back %d migrations: only %d are applied", steps, appliedCount)
	}

	target := int64(0)
	if appliedCount > steps {
		target = appliedVersions[appliedCount-steps-1]
	}
	rolledBack, err := r.provider.DownTo(ctx, target)
	results := migrationResults(rolledBack)
	if err != nil {
		var partial *goose.PartialError
		if errors.As(err, &partial) {
			results = migrationResults(partial.Applied)
		}
		return results, fmt.Errorf("apply down migrations after %d of %d successful steps: %w", len(results), steps, err)
	}
	if len(results) != steps {
		return results, fmt.Errorf("migration state changed during rollback: requested %d steps but completed %d", steps, len(results))
	}
	return results, nil
}

// Versions returns the current database version and latest bundled version.
func (r *Runner) Versions(ctx context.Context) (current int64, latest int64, err error) {
	current, latest, err = r.provider.GetVersions(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("read migration versions: %w", err)
	}
	return current, latest, nil
}

// ValidateCurrent 只读校验数据库最后一条 migration 记录，服务启动不会创建版本表或执行 DDL。
func (r *Runner) ValidateCurrent(ctx context.Context) error {
	var version int64
	var applied bool
	query := fmt.Sprintf("SELECT version_id, is_applied FROM %s ORDER BY id DESC LIMIT 1", versionTable)
	if err := r.db.QueryRowContext(ctx, query).Scan(&version, &applied); err != nil {
		return fmt.Errorf("read application migration version: %w", err)
	}
	sources := r.provider.ListSources()
	if len(sources) == 0 {
		return errors.New("migration bundle is empty")
	}
	latest := sources[len(sources)-1].Version
	if !applied || version != latest {
		return fmt.Errorf(
			"database migration is not current: latest record version=%d applied=%t, binary latest=%d; run coinsphere-migrate -direction up",
			version,
			applied,
			latest,
		)
	}
	return nil
}

// Status returns every bundled migration and its database state.
func (r *Runner) Status(ctx context.Context) ([]Status, error) {
	if _, _, err := r.ensureDatabaseNotAhead(ctx); err != nil {
		return nil, err
	}
	items, err := r.provider.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("read migration status: %w", err)
	}

	statuses := make([]Status, 0, len(items))
	for _, item := range items {
		statuses = append(statuses, Status{
			Version:   item.Source.Version,
			Name:      item.Source.Path,
			State:     string(item.State),
			AppliedAt: item.AppliedAt,
		})
	}
	return statuses, nil
}

func (r *Runner) ensureDatabaseNotAhead(ctx context.Context) (current int64, latest int64, err error) {
	current, latest, err = r.Versions(ctx)
	if err != nil {
		return 0, 0, err
	}
	if current > latest {
		return 0, 0, fmt.Errorf("database migration version %d is newer than this binary's latest version %d", current, latest)
	}
	return current, latest, nil
}

func migrationResults(results []*goose.MigrationResult) []Result {
	converted := make([]Result, 0, len(results))
	for _, result := range results {
		if result.Error != nil {
			continue
		}
		converted = append(converted, migrationResult(result))
	}
	return converted
}

func migrationResult(result *goose.MigrationResult) Result {
	return Result{
		Version:   result.Source.Version,
		Direction: result.Direction,
		Duration:  result.Duration,
	}
}
