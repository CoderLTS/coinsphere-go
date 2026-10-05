package migration

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
)

func ValidateGeneration(ctx context.Context, db *sql.DB) error {
	var generation, server int
	if err := db.QueryRowContext(ctx, "SELECT generation, current_setting('server_version_num')::integer / 10000 FROM public.schema_generation WHERE id").Scan(&generation, &server); err != nil {
		return errors.New("generation 4 database baseline is required")
	}
	if generation != Generation || server != 16 {
		return errors.New("generation 4 requires PostgreSQL 16")
	}
	return nil
}

func NewPluginBaseline(db *sql.DB, files fs.FS, schema string) (*Runner, error) {
	return newWithFSAndTable(db, files, schema+".schema_migrations_g4")
}
