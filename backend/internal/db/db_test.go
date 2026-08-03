package db

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

const postgresMigrationDSNEnv = "COINSPHERE_TEST_POSTGRES_DSN"

// TestRedactingGORMLogger 固定两层日志边界：SQL 参数必须保持占位，驱动错误和直接 Error 调用
// 都只能输出统一分类。这样 Outbox payload 与异常正文不会绕过服务层的脱敏日志重新出现在 stderr。
func TestRedactingGORMLogger(t *testing.T) {
	var output bytes.Buffer
	wrapped := redactingGORMLogger{Interface: logger.New(
		log.New(&output, "", 0),
		logger.Config{LogLevel: logger.Warn, ParameterizedQueries: true},
	)}

	query, params := wrapped.ParamsFilter(context.Background(), "INSERT INTO events(payload) VALUES (?)", "payload-secret-marker")
	if query != "INSERT INTO events(payload) VALUES (?)" || len(params) != 0 {
		t.Fatalf("database logger did not retain placeholders: query=%q params=%v", query, params)
	}
	wrapped.Trace(context.Background(), time.Now(), func() (string, int64) { return query, 0 }, errors.New("driver-secret-marker"))
	wrapped.Error(context.Background(), "direct-secret-marker: %s", "secret")

	logs := output.String()
	for _, marker := range []string{"payload-secret-marker", "driver-secret-marker", "direct-secret-marker"} {
		if strings.Contains(logs, marker) {
			t.Fatalf("database logger leaked marker %q: %s", marker, logs)
		}
	}
	if strings.Count(logs, errDatabaseOperation.Error()) != 2 || !strings.Contains(logs, "VALUES (?)") {
		t.Fatalf("database logger lost fixed classification or parameterized SQL: %s", logs)
	}
}

func TestOpenPreservesAutoMigrateBehavior(t *testing.T) {
	gdb, err := Open(context.Background(), config.DatabaseConfig{
		Driver: "sqlite",
		Path:   filepath.Join(t.TempDir(), "application.db"),
	})
	if err != nil {
		t.Fatalf("open application database: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("get sql database: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close application database: %v", err)
		}
	})

	if !gdb.Migrator().HasTable(&SystemUser{}) {
		t.Fatal("db.Open no longer applies the existing GORM AutoMigrate schema")
	}
}

// TestOpenPreservesVersionedOutboxSchema 验证 00003 之后的启动仍 AutoMigrate 其他模型，
// 但不会重建 Outbox 或删除 migration 独占的触发器、索引和默认值。
func TestOpenPreservesVersionedOutboxSchema(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "versioned-outbox.db")
	seed, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open versioned Outbox fixture database: %v", err)
	}
	runner, err := migration.New(seed, "sqlite")
	if err != nil {
		_ = seed.Close()
		t.Fatalf("create SQLite migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		_ = seed.Close()
		t.Fatalf("apply SQLite Outbox migration: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close versioned Outbox fixture database: %v", err)
	}

	gdb, err := Open(context.Background(), config.DatabaseConfig{Driver: "sqlite", Path: databasePath})
	if err != nil {
		t.Fatalf("open application with versioned Outbox schema: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("get versioned application database: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close versioned application database: %v", err)
		}
	})

	for _, object := range []string{
		"ck_event_outbox_contract_insert",
		"ck_event_outbox_contract_update",
		"idx_domain_event_outbox_event_type",
		"ix_event_outbox_pending",
		"ix_event_outbox_recovery",
		"ux_event_outbox_lease_id",
		"ix_event_outbox_dead_letter_alert",
		"ix_event_outbox_terminal_retention",
	} {
		var found string
		if err := sqlDB.QueryRow(`
SELECT name FROM sqlite_master WHERE name = ? AND type IN ('index', 'trigger')
`, object).Scan(&found); err != nil {
			t.Fatalf("versioned Outbox object %s was removed by startup AutoMigrate: %v", object, err)
		}
	}
	for _, constraint := range []string{
		"fk_domain_event_outbox_workflow_execution",
		"fk_domain_event_outbox_workflow_execution_node",
	} {
		if !gdb.Migrator().HasConstraint(&DomainEventOutbox{}, constraint) {
			t.Fatalf("startup AutoMigrate removed versioned Outbox constraint %s", constraint)
		}
	}
	if _, err := sqlDB.Exec(`
INSERT INTO domain_event_outbox (event_type, status, attempt_count, available_at)
VALUES ('contract.startup', 'pending', 0, CURRENT_TIMESTAMP)
`); err != nil {
		t.Fatalf("insert event after startup AutoMigrate: %v", err)
	}
	var maxAttempts int
	if err := sqlDB.QueryRow(`
SELECT max_attempts FROM domain_event_outbox WHERE event_type = 'contract.startup'
`).Scan(&maxAttempts); err != nil || maxAttempts != 3 {
		t.Fatalf("startup AutoMigrate changed versioned default: value=%d err=%v", maxAttempts, err)
	}
	if _, err := sqlDB.Exec(`
INSERT INTO domain_event_outbox (status, attempt_count, available_at)
VALUES ('unknown', 0, CURRENT_TIMESTAMP)
`); err == nil || !strings.Contains(err.Error(), "contract violation") {
		t.Fatalf("startup AutoMigrate removed versioned Outbox trigger: %v", err)
	}
	if !gdb.Migrator().HasTable(&SystemUser{}) {
		t.Fatal("skipping versioned Outbox also skipped unrelated AutoMigrate models")
	}

	// 版本化 Outbox 只能隔离单表 DDL，不能通过全局关闭关系迁移来规避 GORM 依赖排序。
	// 精确验证通知表的全部五条外键，覆盖 Outbox 入站引用及同表的其他关联约束。
	rows, err := sqlDB.Query(`
SELECT "table", "from", "to", on_delete
FROM pragma_foreign_key_list('notification_deliveries')
`)
	if err != nil {
		t.Fatalf("inspect notification delivery foreign keys: %v", err)
	}
	defer rows.Close()
	foreignKeys := make(map[string]string)
	for rows.Next() {
		var table, from, to, onDelete string
		if err := rows.Scan(&table, &from, &to, &onDelete); err != nil {
			t.Fatalf("scan notification delivery foreign key: %v", err)
		}
		foreignKeys[from] = table + "." + to + ":" + onDelete
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate notification delivery foreign keys: %v", err)
	}
	expectedForeignKeys := map[string]string{
		"workflow_execution_id":      "workflow_executions.id:SET NULL",
		"workflow_execution_node_id": "workflow_execution_nodes.id:SET NULL",
		"outbox_event_id":            "domain_event_outbox.id:SET NULL",
		"recipient_user_id":          "users.id:SET NULL",
		"channel_id":                 "notification_channels.id:SET NULL",
	}
	if len(foreignKeys) != len(expectedForeignKeys) {
		t.Fatalf("unexpected notification delivery foreign key count: got=%v want=%v", foreignKeys, expectedForeignKeys)
	}
	for column, expected := range expectedForeignKeys {
		if foreignKeys[column] != expected {
			t.Fatalf("unexpected foreign key for %s: got=%q want=%q", column, foreignKeys[column], expected)
		}
	}
}

// TestOpenCompletesPostgresOutboxRelationsAfterVersionedMigration 覆盖 PostgreSQL 的空库启动顺序：
// 00003 因父表尚不存在只能先建立无外键 Outbox，随后现有 AutoMigrate 必须补齐两条出站外键，
// 同时不得改写版本化 CHECK、唯一租约或认领索引。SQLite 的同等路径由上一个测试覆盖。
func TestOpenCompletesPostgresOutboxRelationsAfterVersionedMigration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv(postgresMigrationDSNEnv))
	if dsn == "" {
		t.Skipf("%s is not configured", postgresMigrationDSNEnv)
	}
	adminConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse PostgreSQL migration DSN: %v", err)
	}
	adminDB := stdlib.OpenDB(*adminConfig)
	if err := adminDB.Ping(); err != nil {
		_ = adminDB.Close()
		t.Fatalf("ping PostgreSQL migration database: %v", err)
	}
	schemaName := fmt.Sprintf("db_open_outbox_%d_%d", os.Getpid(), time.Now().UnixNano())
	quotedSchema := `"` + strings.ReplaceAll(schemaName, `"`, `""`) + `"`
	if _, err := adminDB.Exec("CREATE SCHEMA " + quotedSchema); err != nil {
		_ = adminDB.Close()
		t.Fatalf("create PostgreSQL test schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminDB.Exec("DROP SCHEMA " + quotedSchema + " CASCADE"); err != nil {
			t.Errorf("drop PostgreSQL test schema: %v", err)
		}
		if err := adminDB.Close(); err != nil {
			t.Errorf("close PostgreSQL admin database: %v", err)
		}
	})

	testConfig := adminConfig.Copy()
	if testConfig.RuntimeParams == nil {
		testConfig.RuntimeParams = make(map[string]string)
	}
	testConfig.RuntimeParams["search_path"] = schemaName
	migrationDB := stdlib.OpenDB(*testConfig)
	if err := migrationDB.Ping(); err != nil {
		_ = migrationDB.Close()
		t.Fatalf("ping PostgreSQL test schema: %v", err)
	}
	runner, err := migration.New(migrationDB, "postgres")
	if err != nil {
		_ = migrationDB.Close()
		t.Fatalf("create PostgreSQL migration runner: %v", err)
	}
	if _, err := runner.Up(context.Background(), 0); err != nil {
		_ = migrationDB.Close()
		t.Fatalf("apply PostgreSQL Outbox migration: %v", err)
	}
	if err := migrationDB.Close(); err != nil {
		t.Fatalf("close PostgreSQL migration connection: %v", err)
	}

	cfg := config.DatabaseConfig{
		Driver:   "postgres",
		Host:     adminConfig.Host,
		Port:     int(adminConfig.Port),
		User:     adminConfig.User,
		Password: adminConfig.Password,
		Database: adminConfig.Database,
		Schema:   schemaName,
		Params:   "sslmode=disable",
	}
	gdb, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open application after PostgreSQL Outbox migration: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("get PostgreSQL application database: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close PostgreSQL application database: %v", err)
		}
	})

	for _, object := range []string{
		"ck_event_outbox_status",
		"ck_event_outbox_attempts",
		"ck_event_outbox_available_at",
		"ck_event_outbox_state_fields",
		"fk_domain_event_outbox_workflow_execution",
		"fk_domain_event_outbox_workflow_execution_node",
	} {
		if !gdb.Migrator().HasConstraint(&DomainEventOutbox{}, object) {
			t.Fatalf("PostgreSQL startup lost or failed to create Outbox constraint %s", object)
		}
	}
	for _, index := range []string{
		"idx_domain_event_outbox_event_type",
		"ix_event_outbox_pending",
		"ix_event_outbox_recovery",
		"ux_event_outbox_lease_id",
		"ix_event_outbox_dead_letter_alert",
		"ix_event_outbox_terminal_retention",
	} {
		if !gdb.Migrator().HasIndex(&DomainEventOutbox{}, index) {
			t.Fatalf("PostgreSQL startup lost Outbox index %s", index)
		}
	}
}

// TestDomainEventOutboxMigrationFieldPermissions 固定版本化 migration 与 GORM 的所有权边界。
// 新字段必须可读、可由后续 dispatcher 更新，但 producer Create 和当前 AutoMigrate 均不得引用它们，
// 否则旧库会因缺列失败，或 00003 的双方言约束/索引会被启动路径抢先改写。
func TestDomainEventOutboxMigrationFieldPermissions(t *testing.T) {
	parsed, err := schema.Parse(&DomainEventOutbox{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse outbox GORM schema: %v", err)
	}

	for _, name := range []string{
		"MaxAttempts",
		"LeaseID",
		"WorkerID",
		"LeaseExpiresAt",
		"ClaimedAt",
		"LastErrorCategory",
		"DeadLetteredAt",
		"AlertedAt",
	} {
		field := parsed.LookUpField(name)
		if field == nil {
			t.Fatalf("missing outbox field %s", name)
		}
		if field.Creatable || !field.Updatable || !field.Readable || !field.IgnoreMigration {
			t.Fatalf(
				"unexpected permissions for %s: creatable=%v updatable=%v readable=%v ignoreMigration=%v",
				name,
				field.Creatable,
				field.Updatable,
				field.Readable,
				field.IgnoreMigration,
			)
		}
		if field.HasDefaultValue || field.NotNull || field.Unique || field.UniqueIndex != "" {
			t.Fatalf("field %s leaked DDL ownership back to GORM", name)
		}
		for _, forbidden := range []string{"INDEX", "UNIQUEINDEX", "CHECK", "NOT NULL"} {
			if _, ok := field.TagSettings[forbidden]; ok {
				t.Fatalf("field %s contains forbidden GORM DDL tag %s", name, forbidden)
			}
		}
	}
}

// TestDomainEventOutboxProducerCreateSupportsLegacySchema 使用未含 00003 字段的真实 SQLite 表验证兼容性。
// 这覆盖 rolling upgrade 边界：代码先更新但 migration 尚未执行时，现有 producer 仍只能写旧列。
func TestDomainEventOutboxProducerCreateSupportsLegacySchema(t *testing.T) {
	gdb, err := Connect(context.Background(), config.DatabaseConfig{
		Driver: "sqlite",
		Path:   filepath.Join(t.TempDir(), "legacy-outbox.db"),
	})
	if err != nil {
		t.Fatalf("connect legacy application database: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("get legacy sql database: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close legacy sql database: %v", err)
		}
	})

	if err := gdb.AutoMigrate(&legacyDomainEventOutbox{}); err != nil {
		t.Fatalf("create legacy outbox schema: %v", err)
	}
	now := time.Now().UTC()
	record := DomainEventOutbox{
		EventType:     "contract.legacy-create",
		AggregateType: "contract",
		AggregateID:   "legacy",
		PayloadJSON:   "{}",
		MetadataJSON:  "{}",
		Status:        "pending",
		AvailableAt:   now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := gdb.Create(&record).Error; err != nil {
		t.Fatalf("create outbox event through new model on legacy schema: %v", err)
	}
	if record.ID == 0 {
		t.Fatal("legacy-compatible producer did not receive generated outbox id")
	}
	var migratedColumnCount int
	if err := sqlDB.QueryRow(`
SELECT COUNT(*)
FROM pragma_table_info('domain_event_outbox')
WHERE name IN (
    'max_attempts', 'lease_id', 'worker_id', 'lease_expires_at',
    'claimed_at', 'last_error_category', 'dead_lettered_at', 'alerted_at'
)
`).Scan(&migratedColumnCount); err != nil {
		t.Fatalf("inspect legacy outbox columns: %v", err)
	}
	if migratedColumnCount != 0 {
		t.Fatalf("legacy schema unexpectedly contains %d fields owned by 00003", migratedColumnCount)
	}
}

// legacyDomainEventOutbox 只用于重现 00003 前由 GORM 管理的精确列集合；它不参与生产模型或 migration。
type legacyDomainEventOutbox struct {
	ID                      int64 `gorm:"primaryKey;autoIncrement"`
	EventType               string
	AggregateType           string
	AggregateID             string `gorm:"column:aggregate_id"`
	WorkflowExecutionID     *int64
	WorkflowExecutionNodeID *int64
	PayloadJSON             string `gorm:"column:payload_json"`
	MetadataJSON            string `gorm:"column:metadata_json"`
	Status                  string `gorm:"default:pending"`
	AttemptCount            int    `gorm:"default:0"`
	AvailableAt             time.Time
	ProcessedAt             *time.Time
	LastErrorMessage        string
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

func (legacyDomainEventOutbox) TableName() string { return "domain_event_outbox" }
