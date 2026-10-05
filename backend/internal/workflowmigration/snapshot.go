package workflowmigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type row map[string]json.RawMessage
type Snapshot struct {
	Identity    string
	Fingerprint string
	Version     int64
	tables      map[string][]row
}

var sourceTables = []struct{ name, order string }{
	{"roles", "id"}, {"users", "id"}, {"user_roles", "id"}, {"workflow_groups", "id"},
	{"workflows", "id"}, {"workflow_revisions", "id"}, {"workflow_runtimes", "workflow_id"},
	{"ai_model_configs", "id"}, {"outbound_proxies", "id"}, {"plugin_installations", "plugin_id"},
	{"result_views", "id"}, {"result_view_user_grants", "view_id,user_id"}, {"result_view_role_grants", "view_id,role_id"},
}

// Snapshot is never serialized. It holds identity/configuration facts and opaque
// non-trading ciphertext only inside the server-side command's address space.
func ReadSource(ctx context.Context, database *sql.DB, sourceLabel string) (Snapshot, error) {
	if sourceLabel == "" {
		return Snapshot{}, errors.New("an explicit source identity label is required")
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return Snapshot{}, errors.New("cannot open a read-only source snapshot")
	}
	defer tx.Rollback()
	s := Snapshot{tables: map[string][]row{}}
	var databaseName string
	var oid int64
	var generation *string
	if err := tx.QueryRowContext(ctx, "SELECT current_database(),oid,to_regclass('public.schema_generation')::text FROM pg_database WHERE datname=current_database()").Scan(&databaseName, &oid, &generation); err != nil {
		return s, errors.New("cannot identify source database")
	}
	if generation != nil {
		return s, errors.New("source must use the old schema ledger")
	}
	s.Identity = Digest([]any{sourceLabel, databaseName, oid})
	if err := tx.QueryRowContext(ctx, "SELECT version_id FROM public.schema_migrations WHERE is_applied ORDER BY id DESC LIMIT 1").Scan(&s.Version); err != nil || s.Version != 23 {
		return s, errors.New("source must be the reviewed generation 3 migration version 23")
	}
	for _, table := range sourceTables {
		var raw []byte
		query := fmt.Sprintf("SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY %s),'[]'::jsonb) FROM public.%s t", table.order, table.name)
		var records []row
		if err := tx.QueryRowContext(ctx, query).Scan(&raw); err != nil || json.Unmarshal(raw, &records) != nil {
			return s, fmt.Errorf("cannot read source table %s", table.name)
		}
		s.tables[table.name] = records
	}
	// Never select Binance credentials into the process. Their existence is
	// reported as a manual rebind dependency; Live release state is not imported.
	var secretRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT coalesce(jsonb_agg(jsonb_build_object(
 'revision_id',b.revision_id,'workflow_id',b.workflow_id,'node_instance_id',b.node_instance_id,'field_name',b.field_name,
 'encrypted_value',CASE WHEN coalesce(r.node_versions->b.node_instance_id->>'nodeType','') ~ '^official[.](ai|connector|notification|qq)[.]' THEN b.encrypted_value ELSE '' END,
 'manual_rebind',NOT (coalesce(r.node_versions->b.node_instance_id->>'nodeType','') ~ '^official[.](ai|connector|notification|qq)[.]')
 ) ORDER BY b.revision_id,b.node_instance_id,b.field_name),'[]'::jsonb)
 FROM workflow_secret_bindings b JOIN workflow_revisions r ON r.id=b.revision_id`).Scan(&secretRaw)
	if err != nil {
		return s, errors.New("cannot read opaque source secret bindings")
	}
	var secrets []row
	if json.Unmarshal(secretRaw, &secrets) != nil {
		return s, errors.New("invalid source secret metadata")
	}
	s.tables["workflow_secret_bindings"] = secrets
	var effective []byte
	err = tx.QueryRowContext(ctx, `SELECT coalesce(jsonb_agg(to_jsonb(p) ORDER BY role_id,permission_code),'[]'::jsonb) FROM (
 SELECT DISTINCT rm.role_id,m.permission_code FROM role_menus rm JOIN menus m ON m.id=rm.menu_id JOIN roles r ON r.id=rm.role_id WHERE r.is_enabled AND m.permission_code IS NOT NULL
 UNION SELECT DISTINCT rb.role_id,b.permission_code FROM role_menu_buttons rb JOIN menu_buttons b ON b.id=rb.button_id JOIN roles r ON r.id=rb.role_id WHERE r.is_enabled
 ) p`).Scan(&effective)
	if err != nil {
		return s, errors.New("cannot read effective source capabilities")
	}
	var permissions []row
	if json.Unmarshal(effective, &permissions) != nil {
		return s, errors.New("invalid source capabilities")
	}
	s.tables["role_permissions"] = permissions
	s.Fingerprint = Digest(struct {
		Identity string
		Version  int64
		Tables   map[string][]row
	}{s.Identity, s.Version, s.tables})
	if err := tx.Commit(); err != nil {
		return s, errors.New("source snapshot closed unsuccessfully")
	}
	return s, nil
}

func textField(r row, key string) string {
	var value string
	_ = json.Unmarshal(r[key], &value)
	return value
}
func intField(r row, key string) int64 {
	var value int64
	_ = json.Unmarshal(r[key], &value)
	return value
}
func boolField(r row, key string) bool {
	var value bool
	_ = json.Unmarshal(r[key], &value)
	return value
}
func put(r row, key string, value any) { r[key], _ = json.Marshal(value) }
func rowsByID(rows []row) map[int64]row {
	result := map[int64]row{}
	for _, r := range rows {
		result[intField(r, "id")] = r
	}
	return result
}

type Inspection struct {
	SourceIdentity    string               `json:"sourceIdentity"`
	SourceFingerprint string               `json:"sourceFingerprint"`
	SourceVersion     int64                `json:"sourceVersion"`
	Counts            map[string]int       `json:"counts"`
	Workflows         []InspectionWorkflow `json:"workflows"`
}
type InspectionWorkflow struct {
	WorkflowID    int64  `json:"workflowId"`
	RevisionID    int64  `json:"revisionId"`
	GraphHash     string `json:"graphHash"`
	HasDefinition bool   `json:"hasDefinition"`
}

func (s Snapshot) Inspect() Inspection {
	i := Inspection{SourceIdentity: s.Identity, SourceFingerprint: s.Fingerprint, SourceVersion: s.Version, Counts: map[string]int{}, Workflows: []InspectionWorkflow{}}
	for name, rows := range s.tables {
		i.Counts[name] = len(rows)
	}
	revisions := rowsByID(s.tables["workflow_revisions"])
	for _, w := range s.tables["workflows"] {
		id := intField(w, "active_revision_id")
		r := revisions[id]
		i.Workflows = append(i.Workflows, InspectionWorkflow{WorkflowID: intField(w, "id"), RevisionID: id, GraphHash: Digest(r["graph_json"]), HasDefinition: r != nil})
	}
	return i
}
