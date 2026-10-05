package workflowmigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"coinsphere/backend/plugin/sdk"
	"coinsphere/backend/workflow/graph"
)

type PlannedWorkflow struct {
	WorkflowID       int64                        `json:"workflowId"`
	SourceRevisionID int64                        `json:"sourceRevisionId"`
	TargetRevisionID int64                        `json:"targetRevisionId"`
	OwnerUserID      int64                        `json:"ownerUserId"`
	GraphHash        string                       `json:"graphHash"`
	NodeIDs          map[string]string            `json:"nodeIds"`
	SecretFields     map[string]map[string]string `json:"secretFields"`
	Dependencies     []string                     `json:"dependencies"`
}
type Issue struct {
	WorkflowID int64  `json:"workflowId,omitempty"`
	ResourceID int64  `json:"resourceId,omitempty"`
	Code       string `json:"code"`
	Detail     string `json:"detail"`
}
type Plan struct {
	Version           int               `json:"version"`
	SourceIdentity    string            `json:"sourceIdentity"`
	SourceFingerprint string            `json:"sourceFingerprint"`
	TargetIdentity    string            `json:"targetIdentity"`
	CatalogHash       string            `json:"catalogHash"`
	Mappings          Mappings          `json:"mappings"`
	Workflows         []PlannedWorkflow `json:"workflows"`
	Issues            []Issue           `json:"issues"`
	Dependencies      []Issue           `json:"dependencies"`
	Hash              string            `json:"hash"`
}
type Catalog struct {
	Converter   Converter
	Plugins     []sdk.PluginDescriptor
	Pages       map[string]sdk.ResultPageDescriptor
	Permissions map[string]sdk.PermissionDescriptor
}

func (c Catalog) Hash() string {
	nodes := map[string]any{}
	for id, d := range c.Converter.Catalog {
		nodes[id] = []any{d.Version, d.ConfigSchema, d.InputSchema, d.OutputSchema, d.Branches, d.State, d.SideEffect, d.ExecutionPermissions}
	}
	pages := map[string]any{}
	for id, p := range c.Pages {
		pages[id] = []any{p.ScopeSchema, p.FilterSchema, p.Actions, p.PermissionCode, p.ActionPermissions}
	}
	return Digest([]any{nodes, pages, c.Plugins, c.Permissions})
}
func TargetIdentity(ctx context.Context, database *sql.DB) (string, error) {
	var id string
	var version, generation int
	if err := database.QueryRowContext(ctx, "SELECT database_id::text,generation,current_setting('server_version_num')::integer/10000 FROM schema_generation WHERE id").Scan(&id, &generation, &version); err != nil || generation != 4 || version != 16 {
		return "", errors.New("target must have an isolated generation 4 PostgreSQL 16 baseline")
	}
	return id, nil
}
func BuildPlan(source Snapshot, targetID string, catalog Catalog, mappings Mappings) Plan {
	p := Plan{Version: 1, SourceIdentity: source.Identity, SourceFingerprint: source.Fingerprint, TargetIdentity: targetID, CatalogHash: catalog.Hash(), Mappings: mappings, Workflows: []PlannedWorkflow{}, Issues: []Issue{}, Dependencies: []Issue{}}
	catalog.Converter.Mappings = mappings
	revisions := rowsByID(source.tables["workflow_revisions"])
	users := rowsByID(source.tables["users"])
	roles := rowsByID(source.tables["roles"])
	convertedGraphs := map[int64]Converted{}
	for _, w := range source.tables["workflows"] {
		id := intField(w, "id")
		revisionID := intField(w, "active_revision_id")
		r := revisions[revisionID]
		if r == nil || intField(r, "workflow_id") != id {
			p.Issues = append(p.Issues, Issue{WorkflowID: id, Code: "missing_definition", Detail: "source has no current immutable definition"})
			continue
		}
		owner := intField(w, "created_by")
		if mapped := mappings.Owners[id]; mapped > 0 {
			owner = mapped
		}
		if users[owner] == nil {
			p.Issues = append(p.Issues, Issue{WorkflowID: id, Code: "owner_mapping", Detail: "explicit owner must identify an imported user"})
			continue
		}
		converted, err := catalog.Converter.Convert(id, r["graph_json"])
		if err != nil {
			p.Issues = append(p.Issues, Issue{WorkflowID: id, Code: "graph_mapping", Detail: err.Error()})
			continue
		}
		convertedGraphs[id] = converted
		configured := map[RequiredSecret]bool{}
		p.Workflows = append(p.Workflows, PlannedWorkflow{WorkflowID: id, SourceRevisionID: revisionID, TargetRevisionID: revisionID, OwnerUserID: owner, GraphHash: Digest(converted.Graph), NodeIDs: converted.NodeIDs, SecretFields: converted.SecretFields, Dependencies: converted.Dependencies})
		for _, secret := range source.tables["workflow_secret_bindings"] {
			if intField(secret, "revision_id") != revisionID {
				continue
			}
			node := textField(secret, "node_instance_id")
			field := textField(secret, "field_name")
			if converted.SecretFields[node][field] == "" {
				p.Issues = append(p.Issues, Issue{WorkflowID: id, Code: "secret_field_mapping", Detail: "source secret field has no declared target mapping"})
			}
			if boolField(secret, "manual_rebind") {
				p.Dependencies = append(p.Dependencies, Issue{WorkflowID: id, Code: "manual_secret_rebind", Detail: "a trading or unclassified credential must be rebound manually on the server"})
			} else if textField(secret, "encrypted_value") != "" {
				configured[RequiredSecret{converted.NodeIDs[node], converted.SecretFields[node][field]}] = true
			}
		}
		for _, required := range converted.RequiredSecrets {
			if !configured[required] {
				p.Dependencies = append(p.Dependencies, Issue{WorkflowID: id, Code: "required_secret", Detail: fmt.Sprintf("node %s requires credential field %s before publishing or enabling", required.NodeID, required.Field)})
			}
		}
		if !boolField(users[owner], "is_active") {
			p.Dependencies = append(p.Dependencies, Issue{WorkflowID: id, Code: "inactive_owner", Detail: "the imported owner is disabled"})
		}
	}
	workflows := map[int64]bool{}
	for _, w := range p.Workflows {
		workflows[w.WorkflowID] = true
	}
	for _, view := range source.tables["result_views"] {
		if textField(view, "status") != "active" {
			continue
		}
		view = mappedResult(view, mappings)
		id := intField(view, "id")
		key := textField(view, "plugin_id") + "/" + textField(view, "page_key")
		page, ok := catalog.Pages[key]
		issue := func(code string) {
			p.Issues = append(p.Issues, Issue{ResourceID: id, Code: code, Detail: "result view needs an explicit current page, scope, or action mapping"})
		}
		if !ok || page.Resources == nil {
			issue("result_page_mapping")
			continue
		}
		var scope, filters any
		if json.Unmarshal(view["scope_json"], &scope) != nil || json.Unmarshal(view["filters_json"], &filters) != nil || graph.ValidateValue(page.ScopeSchema, scope) != nil || graph.ValidateValue(page.FilterSchema, filters) != nil {
			issue("result_schema_mapping")
			continue
		}
		refs, err := page.Resources(view["scope_json"])
		if err != nil || len(refs) == 0 {
			issue("result_scope_mapping")
			continue
		}
		for _, ref := range refs {
			converted, ok := convertedGraphs[ref.WorkflowID]
			if !workflows[ref.WorkflowID] || !ok {
				issue("result_workflow_mapping")
				continue
			}
			if ref.NodeInstanceID != "" {
				mapped := converted.NodeIDs[ref.NodeInstanceID]
				found := false
				for source, target := range converted.NodeIDs {
					if target == ref.NodeInstanceID && nodePluginAt(converted.Graph, source, catalog.Converter.NodePlugins) == textField(view, "plugin_id") {
						found = true
					}
				}
				if mapped == "" || !found {
					issue("result_node_mapping")
				}
			}
		}
		if users[intField(view, "created_by")] == nil {
			issue("result_creator_mapping")
		}
		for _, grant := range source.tables["result_view_user_grants"] {
			if intField(grant, "view_id") == id && users[intField(grant, "user_id")] == nil {
				issue("result_user_grant")
			}
		}
		for _, grant := range source.tables["result_view_role_grants"] {
			if intField(grant, "view_id") == id && roles[intField(grant, "role_id")] == nil {
				issue("result_role_grant")
			}
		}
		var actions []string
		if json.Unmarshal(view["allowed_actions"], &actions) != nil {
			issue("result_actions")
			continue
		}
		for _, action := range actions {
			if page.ActionPermissions[action] == "" {
				issue("result_action_mapping")
			}
		}
	}
	sort.Slice(p.Workflows, func(i, j int) bool { return p.Workflows[i].WorkflowID < p.Workflows[j].WorkflowID })
	p.Hash = p.computeHash()
	return p
}
func nodePluginAt(definition graph.Graph, id string, plugins map[string]string) string {
	for _, node := range definition.Nodes {
		if node.NodeInstanceID == id {
			return plugins[node.NodeType]
		}
		prefix := node.NodeInstanceID + "."
		if node.NodeType == "core.loop" && strings.HasPrefix(id, prefix) {
			var config struct {
				Body graph.Graph `json:"body"`
			}
			if json.Unmarshal(node.Config, &config) == nil {
				return nodePluginAt(config.Body, strings.TrimPrefix(id, prefix), plugins)
			}
		}
	}
	return ""
}

func mappedResult(original row, mappings Mappings) row {
	result := cloneRow(original)
	if mapping, ok := mappings.Results[intField(original, "id")]; ok {
		put(result, "plugin_id", mapping.PluginID)
		put(result, "page_key", mapping.PageKey)
		result["scope_json"], result["filters_json"] = mapping.Scope, mapping.Filters
		put(result, "allowed_actions", mapping.AllowedActions)
	}
	return result
}
func (p Plan) computeHash() string { p.Hash = ""; return Digest(p) }
func (p Plan) Validate() error {
	if p.Version != 1 || p.Hash == "" || p.Hash != p.computeHash() || p.TargetIdentity == "" || p.SourceIdentity == "" {
		return errors.New("plan identity or hash is invalid")
	}
	if len(p.Issues) > 0 {
		return fmt.Errorf("plan has %d unresolved conversion issues", len(p.Issues))
	}
	return nil
}
