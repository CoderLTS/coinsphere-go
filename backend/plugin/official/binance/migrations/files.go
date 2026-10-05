package migrations

import "embed"

//go:embed sql/*.sql
var Files embed.FS
