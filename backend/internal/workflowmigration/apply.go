package workflowmigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type RebindSecret func(string) (string, error)
type Applied struct {
	PlanHash       string `json:"planHash"`
	Workflows      int    `json:"workflows"`
	AlreadyApplied bool   `json:"alreadyApplied"`
	Verified       bool   `json:"verified"`
}

var targetAssetTables = []struct{ name, order string }{
	{"roles", "id"}, {"users", "id"}, {"user_roles", "id"}, {"workflow_groups", "id"},
	{"workflows", "id"}, {"workflow_revisions", "id"}, {"workflow_runtimes", "workflow_id"},
	{"workflow_secret_bindings", "revision_id,node_instance_id,field_name"}, {"permissions", "code"}, {"role_permissions", "role_id,permission_code"},
	{"ai_model_configs", "id"}, {"outbound_proxies", "id"}, {"plugin_installations", "plugin_id"}, {"plugin_references", "id"},
	{"result_views", "id"}, {"result_view_user_grants", "view_id,user_id"}, {"result_view_role_grants", "view_id,role_id"},
}
var transientTables = []string{"workflow_runs", "workflow_event_records", "workflow_event_deliveries", "workflow_event_outbox", "workflow_human_tasks", "workflow_node_states", "notification_inbox"}

func Apply(ctx context.Context, target *sql.DB, source Snapshot, plan Plan, catalog Catalog, rebind RebindSecret) (Applied, error) {
	result := Applied{PlanHash: plan.Hash, Workflows: len(plan.Workflows)}
	if err := plan.Validate(); err != nil {
		return result, err
	}
	if source.Identity != plan.SourceIdentity || source.Fingerprint != plan.SourceFingerprint || catalog.Hash() != plan.CatalogHash {
		return result, errors.New("source or compiled catalog changed; create a new plan")
	}
	identity, err := TargetIdentity(ctx, target)
	if err != nil || identity != plan.TargetIdentity {
		return result, errors.New("target identity differs from the reviewed plan")
	}
	actual := BuildPlan(source, identity, catalog, plan.Mappings)
	if actual.Hash != plan.Hash {
		return result, errors.New("recomputed plan differs from the reviewed plan")
	}
	tx, err := target.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return result, errors.New("cannot open target transaction")
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(7420204)"); err != nil {
		return result, errors.New("cannot lock migration batch")
	}
	var manifest []byte
	err = tx.QueryRowContext(ctx, "SELECT manifest FROM workflow_migration_batches WHERE plan_hash=$1", plan.Hash).Scan(&manifest)
	if err == nil {
		if err := verifyTx(ctx, tx, plan, manifest); err != nil {
			return result, err
		}
		result.AlreadyApplied, result.Verified = true, true
		return result, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, errors.New("cannot read migration ledger")
	}
	// The importer owns the target until commit. A live candidate application
	// cannot insert assets between the emptiness check and the atomic import.
	for _, table := range targetAssetTables {
		if _, err := tx.ExecContext(ctx, "LOCK TABLE "+table.name+" IN EXCLUSIVE MODE"); err != nil {
			return result, errors.New("cannot lock target assets")
		}
	}
	for _, table := range []string{"users", "roles", "workflows", "workflow_groups", "result_views", "ai_model_configs", "outbound_proxies"} {
		if err := requireEmpty(ctx, tx, table); err != nil {
			return result, err
		}
	}
	for _, table := range transientTables {
		if err := requireEmpty(ctx, tx, table); err != nil {
			return result, err
		}
	}
	for _, table := range []string{"roles", "users", "user_roles", "workflow_groups"} {
		for _, r := range source.tables[table] {
			if err := insertRow(ctx, tx, table, r); err != nil {
				return result, err
			}
		}
	}
	for code, permission := range catalog.Permissions {
		if _, err := tx.ExecContext(ctx, "INSERT INTO permissions(code,title,plugin_id,protected) VALUES($1,$2,$3,$4) ON CONFLICT(code) DO NOTHING", code, permission.Title, permissionPlugin(catalog, code), permission.Protected); err != nil {
			return result, errors.New("cannot import capability catalog")
		}
	}
	for _, r := range source.tables["role_permissions"] {
		code := textField(r, "permission_code")
		permission, ok := catalog.Permissions[code]
		// Old workflow and configuration management endpoints were super-only.
		// Inert menu buttons must not acquire new effective management authority.
		if !ok || permission.Protected || strings.HasPrefix(code, "workflows.") || strings.HasPrefix(code, "human_tasks.") || strings.HasPrefix(code, "plugins.") || code == "result_views.manage" || code == "config.ai.manage" || code == "system.users.assign_roles" {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO role_permissions(role_id,permission_code) VALUES($1,$2)", intField(r, "role_id"), code); err != nil {
			return result, errors.New("cannot import effective role capability")
		}
	}
	for _, table := range []string{"ai_model_configs", "outbound_proxies"} {
		for _, original := range source.tables[table] {
			r := cloneRow(original)
			field := "api_key_ciphertext"
			if table == "outbound_proxies" {
				field = "password_ciphertext"
				put(r, "last_check_status", "unchecked")
				put(r, "last_checked_at", nil)
				put(r, "last_latency_ms", nil)
			}
			ciphertext := textField(r, field)
			if ciphertext != "" {
				if rebind == nil {
					return result, errors.New("server-side non-trading secret rebind is required")
				}
				value, err := rebind(ciphertext)
				if err != nil || value == "" {
					return result, errors.New("cannot rebind non-trading configuration secret")
				}
				put(r, field, value)
			}
			put(r, "is_enabled", false)
			if err := insertRow(ctx, tx, table, r); err != nil {
				return result, err
			}
		}
	}
	wfs := rowsByID(source.tables["workflows"])
	revisions := rowsByID(source.tables["workflow_revisions"])
	catalog.Converter.Mappings = plan.Mappings
	for _, item := range plan.Workflows {
		old := wfs[item.WorkflowID]
		revision := revisions[item.SourceRevisionID]
		converted, err := catalog.Converter.Convert(item.WorkflowID, revision["graph_json"])
		if err != nil || Digest(converted.Graph) != item.GraphHash {
			return result, errors.New("graph changed while applying plan")
		}
		w := cloneRow(old)
		delete(w, "active_revision_id")
		put(w, "owner_user_id", item.OwnerUserID)
		put(w, "status", "inactive")
		put(w, "draft_revision_id", nil)
		put(w, "published_revision_id", nil)
		put(w, "main_trigger_node_id", converted.Graph.EntryPoints["main"])
		if err := insertRow(ctx, tx, "workflows", w); err != nil {
			return result, err
		}
		r := cloneRow(revision)
		put(r, "id", item.TargetRevisionID)
		put(r, "revision_number", 1)
		put(r, "graph_json", converted.Graph)
		put(r, "main_trigger_node_id", converted.Graph.EntryPoints["main"])
		versions := map[string]any{}
		var addNodes func([]byte, string) error
		addNodes = func(raw []byte, prefix string) error {
			var g struct {
				Nodes []struct {
					ID      string          `json:"nodeInstanceId"`
					Type    string          `json:"nodeType"`
					Version string          `json:"nodeVersion"`
					Config  json.RawMessage `json:"config"`
				} `json:"nodes"`
			}
			if json.Unmarshal(raw, &g) != nil {
				return errors.New("invalid converted node catalog")
			}
			for _, n := range g.Nodes {
				versions[prefix+n.ID] = map[string]string{"nodeType": n.Type, "nodeVersion": n.Version}
				if n.Type == "core.loop" {
					var loop struct {
						Body json.RawMessage `json:"body"`
					}
					if json.Unmarshal(n.Config, &loop) != nil {
						return errors.New("invalid converted Loop")
					}
					if err := addNodes(loop.Body, prefix+n.ID+"."); err != nil {
						return err
					}
				}
			}
			return nil
		}
		graphRaw, _ := json.Marshal(converted.Graph)
		if err := addNodes(graphRaw, ""); err != nil {
			return result, err
		}
		put(r, "node_versions", versions)
		if err := insertRow(ctx, tx, "workflow_revisions", r); err != nil {
			return result, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE workflows SET draft_revision_id=$1,published_revision_id=$1 WHERE id=$2", item.TargetRevisionID, item.WorkflowID); err != nil {
			return result, errors.New("cannot bind imported revision pointers")
		}
		runtime := row{}
		put(runtime, "workflow_id", item.WorkflowID)
		put(runtime, "max_concurrent_runs", 2)
		put(runtime, "backlog_limit", 100)
		put(runtime, "next_scheduled_at", nil)
		put(runtime, "last_scheduled_at", nil)
		put(runtime, "trigger_lease_token", nil)
		put(runtime, "trigger_lease_expires_at", nil)
		put(runtime, "updated_at", time.Now().UTC())
		for _, oldRuntime := range source.tables["workflow_runtimes"] {
			if intField(oldRuntime, "workflow_id") == item.WorkflowID {
				runtime["max_concurrent_runs"], runtime["backlog_limit"] = oldRuntime["max_concurrent_runs"], oldRuntime["backlog_limit"]
			}
		}
		if err := insertRow(ctx, tx, "workflow_runtimes", runtime); err != nil {
			return result, err
		}
		for _, oldSecret := range source.tables["workflow_secret_bindings"] {
			if intField(oldSecret, "revision_id") != item.SourceRevisionID || boolField(oldSecret, "manual_rebind") {
				continue
			}
			node, field := textField(oldSecret, "node_instance_id"), textField(oldSecret, "field_name")
			targetField := item.SecretFields[node][field]
			if targetField == "" || item.NodeIDs[node] == "" || rebind == nil {
				return result, errors.New("source secret mapping is incomplete")
			}
			value, err := rebind(textField(oldSecret, "encrypted_value"))
			if err != nil || value == "" {
				return result, errors.New("cannot rebind workflow secret")
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO workflow_secret_bindings(revision_id,workflow_id,node_instance_id,field_name,encrypted_value) VALUES($1,$2,$3,$4,$5)", item.TargetRevisionID, item.WorkflowID, item.NodeIDs[node], targetField, value); err != nil {
				return result, errors.New("cannot import workflow secret binding")
			}
		}
		for _, pluginID := range item.Dependencies {
			if err := insertReference(ctx, tx, pluginID, "revision", fmt.Sprint(item.TargetRevisionID)); err != nil {
				return result, err
			}
		}
	}
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: tx}), &gorm.Config{SkipDefaultTransaction: true, DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return result, errors.New("cannot validate scoped plugin assets")
	}
	activeViews := map[int64]bool{}
	for _, original := range source.tables["result_views"] {
		if textField(original, "status") != "active" {
			continue
		}
		view := mappedResult(original, plan.Mappings)
		id := intField(view, "id")
		activeViews[id] = true
		page := catalog.Pages[textField(view, "plugin_id")+"/"+textField(view, "page_key")]
		if page.ValidateScope == nil || page.ValidateScope(ctx, gdb, view["scope_json"]) != nil {
			return result, errors.New("imported result scope failed plugin validation")
		}
		put(view, "status", "inactive")
		put(view, "revoked_at", nil)
		if err := insertRow(ctx, tx, "result_views", view); err != nil {
			return result, err
		}
		if err := insertReference(ctx, tx, textField(view, "plugin_id"), "result_view", fmt.Sprint(id)); err != nil {
			return result, err
		}
	}
	for _, table := range []string{"result_view_user_grants", "result_view_role_grants"} {
		for _, r := range source.tables[table] {
			if activeViews[intField(r, "view_id")] {
				if err := insertRow(ctx, tx, table, r); err != nil {
					return result, err
				}
			}
		}
	}
	for _, table := range []string{"users", "roles", "user_roles", "workflow_groups", "workflows", "workflow_revisions", "ai_model_configs", "outbound_proxies", "result_views", "plugin_references"} {
		// ALTER SEQUENCE is transactional; setval would survive a failed import.
		var next int64
		if err := tx.QueryRowContext(ctx, "SELECT coalesce(max(id),0)+1 FROM "+table).Scan(&next); err != nil {
			return result, errors.New("cannot compute imported sequence")
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("ALTER SEQUENCE %s_id_seq RESTART WITH %d", table, next)); err != nil {
			return result, errors.New("cannot advance imported sequence")
		}
	}
	digest, err := assetDigest(ctx, tx)
	if err != nil {
		return result, err
	}
	manifest, _ = json.Marshal(map[string]any{"plan": plan, "dataHash": digest})
	if _, err := tx.ExecContext(ctx, "INSERT INTO workflow_migration_batches(plan_hash,source_fingerprint,target_identity,manifest) VALUES($1,$2,$3,$4)", plan.Hash, plan.SourceFingerprint, plan.TargetIdentity, string(manifest)); err != nil {
		return result, errors.New("cannot commit migration batch ledger")
	}
	for _, item := range plan.Workflows {
		if _, err := tx.ExecContext(ctx, "INSERT INTO workflow_migration_items(plan_hash,kind,source_id,target_id,digest) VALUES($1,'workflow',$2,$3,$4)", plan.Hash, fmt.Sprint(item.WorkflowID), fmt.Sprint(item.WorkflowID), item.GraphHash); err != nil {
			return result, errors.New("cannot commit migration mapping ledger")
		}
	}
	if err := verifyTx(ctx, tx, plan, manifest); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, errors.New("target transaction failed; no import was committed")
	}
	result.Verified = true
	return result, nil
}

func cloneRow(original row) row {
	copy := row{}
	for key, value := range original {
		copy[key] = value
	}
	return copy
}
func insertRow(ctx context.Context, tx *sql.Tx, table string, r row) error {
	raw, _ := json.Marshal(r)
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+" SELECT * FROM jsonb_populate_record(NULL::"+table+",$1::jsonb)", string(raw)); err != nil {
		return fmt.Errorf("cannot import %s; target transaction rolled back", table)
	}
	return nil
}
func requireEmpty(ctx context.Context, tx *sql.Tx, table string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+table+")").Scan(&exists); err != nil {
		return errors.New("cannot inspect target emptiness")
	}
	if exists {
		return fmt.Errorf("target %s must be empty before import", table)
	}
	return nil
}
func permissionPlugin(c Catalog, code string) string {
	for _, plugin := range c.Plugins {
		for _, p := range plugin.Permissions {
			if p.Code == code {
				return plugin.ID
			}
		}
	}
	return ""
}
func insertReference(ctx context.Context, tx *sql.Tx, pluginID, kind, id string) error {
	var status string
	if err := tx.QueryRowContext(ctx, "SELECT status FROM plugin_installations WHERE plugin_id=$1 FOR UPDATE", pluginID).Scan(&status); err != nil || status != "installed" {
		return errors.New("a required plugin is not installed in the target")
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO plugin_references(plugin_id,reference_type,reference_id,active) VALUES($1,$2,$3,true) ON CONFLICT(plugin_id,reference_type,reference_id) DO NOTHING", pluginID, kind, id); err != nil {
		return errors.New("cannot import plugin reference")
	}
	return nil
}
func assetDigest(ctx context.Context, tx *sql.Tx) (string, error) {
	facts := map[string]json.RawMessage{}
	for _, table := range targetAssetTables {
		var raw []byte
		query := fmt.Sprintf("SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY %s),'[]'::jsonb) FROM %s t", table.order, table.name)
		if err := tx.QueryRowContext(ctx, query).Scan(&raw); err != nil {
			return "", errors.New("cannot verify imported asset facts")
		}
		facts[table.name] = raw
	}
	return Digest(facts), nil
}
func verifyTx(ctx context.Context, tx *sql.Tx, plan Plan, manifest []byte) error {
	var stored struct {
		Plan     Plan   `json:"plan"`
		DataHash string `json:"dataHash"`
	}
	if json.Unmarshal(manifest, &stored) != nil || stored.Plan.Hash != plan.Hash {
		return errors.New("migration ledger does not match plan")
	}
	digest, err := assetDigest(ctx, tx)
	if err != nil {
		return err
	}
	if digest != stored.DataHash {
		return errors.New("imported target assets changed; refusing to overwrite")
	}
	for _, table := range transientTables {
		if err := requireEmpty(ctx, tx, table); err != nil {
			return err
		}
	}
	var enabled bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM workflows WHERE status<>'inactive') OR EXISTS(SELECT 1 FROM result_views WHERE status<>'inactive')").Scan(&enabled); err != nil || enabled {
		return errors.New("imported workflows and result views must remain inactive during verification")
	}
	return nil
}
func Verify(ctx context.Context, target *sql.DB, plan Plan) (Applied, error) {
	result := Applied{PlanHash: plan.Hash, Workflows: len(plan.Workflows), AlreadyApplied: true}
	if err := plan.Validate(); err != nil {
		return result, err
	}
	identity, err := TargetIdentity(ctx, target)
	if err != nil || identity != plan.TargetIdentity {
		return result, errors.New("wrong target identity")
	}
	tx, err := target.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return result, errors.New("cannot open read-only target verification")
	}
	defer tx.Rollback()
	var manifest []byte
	if err := tx.QueryRowContext(ctx, "SELECT manifest FROM workflow_migration_batches WHERE plan_hash=$1", plan.Hash).Scan(&manifest); err != nil {
		return result, errors.New("migration batch is not applied")
	}
	if err := verifyTx(ctx, tx, plan, manifest); err != nil {
		return result, err
	}
	result.Verified = true
	return result, tx.Commit()
}
