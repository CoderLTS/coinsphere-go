package migration

// One-time workflow transfer format for the Core4/Graph3 cutover. It uses
// database/sql and never reads workflow runs, logs, artifacts, or secrets.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const WorkflowTransferSchemaVersion = 1

type WorkflowTransfer struct {
	SchemaVersion int                `json:"schemaVersion"`
	ExportedAt    string             `json:"exportedAt"`
	Groups        []TransferGroup    `json:"groups"`
	Profiles      []TransferProfile  `json:"profiles"`
	Workflows     []TransferWorkflow `json:"workflows"`
}

type TransferGroup struct {
	SourceID  int64  `json:"sourceId"`
	Name      string `json:"name"`
	SortOrder int    `json:"sortOrder"`
}

type TransferRuntime struct {
	MaxConcurrentRuns int `json:"maxConcurrentRuns"`
	BacklogLimit      int `json:"backlogLimit"`
}

type TransferProfile struct {
	SourceID     string          `json:"sourceId"`
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	Version      int64           `json:"version"`
	Enabled      bool            `json:"enabled"`
	Config       json.RawMessage `json:"config"`
	SecretFields json.RawMessage `json:"secretFields"`
}

type TransferRevision struct {
	SourceID       int64           `json:"sourceId"`
	RevisionNumber int64           `json:"revisionNumber"`
	Graph          json.RawMessage `json:"graph"`
	NodeVersions   json.RawMessage `json:"nodeVersions"`
	CreatedBy      int64           `json:"createdBy"`
	CreatedAt      string          `json:"createdAt"`
}

type TransferWorkflow struct {
	SourceID       int64              `json:"sourceId"`
	Name           string             `json:"name"`
	Description    string             `json:"description"`
	GroupSourceID  *int64             `json:"groupSourceId,omitempty"`
	MainTriggerID  string             `json:"mainTriggerId,omitempty"`
	Status         string             `json:"status"`
	RetentionDays  int                `json:"retentionDays"`
	CreatedBy      int64              `json:"createdBy"`
	CreatedAt      string             `json:"createdAt"`
	UpdatedAt      string             `json:"updatedAt"`
	ActiveSourceID *int64             `json:"activeSourceId,omitempty"`
	Runtime        TransferRuntime    `json:"runtime"`
	Revisions      []TransferRevision `json:"revisions"`
}

type TransferReport struct {
	SchemaVersion int            `json:"schemaVersion"`
	Imported      []TransferItem `json:"imported"`
	Warnings      []TransferItem `json:"warnings"`
	Skipped       []TransferItem `json:"skipped"`
	Errors        []TransferItem `json:"errors"`
}

type TransferItem struct {
	SourceID int64  `json:"sourceId"`
	Name     string `json:"name"`
	Reason   string `json:"reason"`
}

type TransferOptions struct {
	// ValidateGraph is called for every revision before any import write.
	ValidateGraph func(json.RawMessage) error
	// ResolveCreatedBy maps a source user id to a current user id. Returning
	// zero falls back to the first active super user.
	ResolveCreatedBy func(context.Context, *sql.Tx, int64) (int64, error)
}

// ExportWorkflowTransfer reads definitions, revisions, groups and runtime
// limits. It does not query runs, logs, artifacts, or secret bindings.
func ExportWorkflowTransfer(ctx context.Context, db *sql.DB, out io.Writer) error {
	if db == nil || out == nil {
		return errors.New("database and output are required")
	}
	transfer := WorkflowTransfer{SchemaVersion: WorkflowTransferSchemaVersion, ExportedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	groups, err := db.QueryContext(ctx, "SELECT to_jsonb(g) FROM workflow_groups g ORDER BY id")
	if err != nil {
		return fmt.Errorf("read workflow groups: %w", err)
	}
	for groups.Next() {
		var raw []byte
		if err := groups.Scan(&raw); err != nil {
			_ = groups.Close()
			return fmt.Errorf("read workflow group row: %w", err)
		}
		group, err := decodeTransferGroup(raw)
		if err != nil {
			_ = groups.Close()
			return err
		}
		transfer.Groups = append(transfer.Groups, group)
	}
	if err := groups.Err(); err != nil {
		_ = groups.Close()
		return fmt.Errorf("iterate workflow groups: %w", err)
	}
	_ = groups.Close()
	transfer.Profiles, err = exportWorkflowProfiles(ctx, db)
	if err != nil {
		return err
	}

	workflows, err := db.QueryContext(ctx, "SELECT to_jsonb(w) FROM workflows w ORDER BY id")
	if err != nil {
		return fmt.Errorf("read workflows: %w", err)
	}
	for workflows.Next() {
		var raw []byte
		if err := workflows.Scan(&raw); err != nil {
			_ = workflows.Close()
			return fmt.Errorf("read workflow row: %w", err)
		}
		workflow, err := decodeTransferWorkflow(raw)
		if err != nil {
			_ = workflows.Close()
			return err
		}
		workflow.Revisions, err = exportWorkflowRevisions(ctx, db, workflow.SourceID)
		if err != nil {
			_ = workflows.Close()
			return err
		}
		if runtime, runtimeErr := exportWorkflowRuntime(ctx, db, workflow.SourceID); runtimeErr == nil {
			workflow.Runtime = runtime
		} else if !isMissingRelation(runtimeErr) {
			_ = workflows.Close()
			return runtimeErr
		}
		transfer.Workflows = append(transfer.Workflows, workflow)
	}
	if err := workflows.Err(); err != nil {
		_ = workflows.Close()
		return fmt.Errorf("iterate workflows: %w", err)
	}
	_ = workflows.Close()
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(transfer)
}

func exportWorkflowProfiles(ctx context.Context, db *sql.DB) ([]TransferProfile, error) {
	for _, table := range []string{"workflow_profile_snapshots", "workflow_connections"} {
		rows, err := db.QueryContext(ctx, "SELECT to_jsonb(p) FROM "+table+" p ORDER BY id")
		if err != nil {
			if isMissingRelation(err) {
				continue
			}
			return nil, fmt.Errorf("read workflow profiles: %w", err)
		}
		profiles := make([]TransferProfile, 0)
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("read workflow profile row: %w", err)
			}
			profile, err := decodeTransferProfile(raw)
			if err != nil {
				_ = rows.Close()
				return nil, err
			}
			profiles = append(profiles, profile)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate workflow profiles: %w", err)
		}
		_ = rows.Close()
		return profiles, nil
	}
	return []TransferProfile{}, nil
}

func exportWorkflowRevisions(ctx context.Context, db *sql.DB, workflowID int64) ([]TransferRevision, error) {
	rows, err := db.QueryContext(ctx, "SELECT to_jsonb(r) FROM workflow_revisions r WHERE workflow_id = $1 ORDER BY revision_number, id", workflowID)
	if err != nil {
		return nil, fmt.Errorf("read revisions for workflow %d: %w", workflowID, err)
	}
	defer rows.Close()
	result := make([]TransferRevision, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("read revision for workflow %d: %w", workflowID, err)
		}
		revision, err := decodeTransferRevision(raw)
		if err != nil {
			return nil, err
		}
		result = append(result, revision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate revisions for workflow %d: %w", workflowID, err)
	}
	return result, nil
}

func exportWorkflowRuntime(ctx context.Context, db *sql.DB, workflowID int64) (TransferRuntime, error) {
	var runtime TransferRuntime
	err := db.QueryRowContext(ctx, "SELECT max_concurrent_runs, backlog_limit FROM workflow_runtimes WHERE workflow_id = $1", workflowID).Scan(&runtime.MaxConcurrentRuns, &runtime.BacklogLimit)
	return runtime, err
}

// ImportWorkflowTransfer validates all revisions before opening any write
// transaction. The transaction then either imports every definition or none.
func ImportWorkflowTransfer(ctx context.Context, db *sql.DB, in io.Reader, options TransferOptions) (TransferReport, error) {
	var transfer WorkflowTransfer
	if err := json.NewDecoder(in).Decode(&transfer); err != nil {
		return TransferReport{}, fmt.Errorf("decode workflow transfer: %w", err)
	}
	if transfer.SchemaVersion != WorkflowTransferSchemaVersion {
		return TransferReport{}, fmt.Errorf("unsupported workflow transfer schemaVersion %d", transfer.SchemaVersion)
	}
	report := TransferReport{SchemaVersion: WorkflowTransferSchemaVersion}
	for _, profile := range transfer.Profiles {
		if strings.TrimSpace(profile.SourceID) == "" || strings.TrimSpace(profile.Type) == "" || strings.TrimSpace(profile.Name) == "" {
			report.Errors = append(report.Errors, TransferItem{Name: profile.Name, Reason: "profile identity is incomplete"})
		}
	}
	for _, workflow := range transfer.Workflows {
		if strings.TrimSpace(workflow.Name) == "" {
			report.Errors = append(report.Errors, TransferItem{SourceID: workflow.SourceID, Reason: "workflow name is empty"})
			continue
		}
		if len(workflow.Revisions) == 0 {
			report.Errors = append(report.Errors, TransferItem{SourceID: workflow.SourceID, Name: workflow.Name, Reason: "workflow has no revisions"})
			continue
		}
		for index := range workflow.Revisions {
			revision := &workflow.Revisions[index]
			normalized, err := normalizeWorkflowGraph(revision.Graph)
			if err != nil {
				report.Errors = append(report.Errors, TransferItem{SourceID: workflow.SourceID, Name: workflow.Name, Reason: fmt.Sprintf("revision %d: %v", revision.RevisionNumber, err)})
				break
			}
			revision.Graph = normalized
			if options.ValidateGraph != nil {
				if err := options.ValidateGraph(revision.Graph); err != nil {
					report.Errors = append(report.Errors, TransferItem{SourceID: workflow.SourceID, Name: workflow.Name, Reason: fmt.Sprintf("revision %d: %v", revision.RevisionNumber, err)})
					break
				}
			}
		}
	}
	if len(report.Errors) > 0 {
		sortTransferReport(&report)
		return report, errors.New("workflow transfer validation failed")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return report, fmt.Errorf("begin workflow transfer: %w", err)
	}
	defer tx.Rollback()
	groupIDs := make(map[int64]int64, len(transfer.Groups))
	profilesByID := make(map[string]TransferProfile, len(transfer.Profiles))
	for _, group := range transfer.Groups {
		var id int64
		if err := tx.QueryRowContext(ctx, "INSERT INTO workflow_groups (name, sort_order, created_at, updated_at) VALUES ($1, $2, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id", group.Name, group.SortOrder).Scan(&id); err != nil {
			return report, fmt.Errorf("import group %d: %w", group.SourceID, err)
		}
		groupIDs[group.SourceID] = id
	}
	for _, profile := range transfer.Profiles {
		profilesByID[profile.SourceID] = profile
		config := profile.Config
		if len(config) == 0 || string(config) == "null" {
			config = json.RawMessage("{}")
		}
		secretFields := profile.SecretFields
		if len(secretFields) == 0 || string(secretFields) == "null" {
			secretFields = json.RawMessage("[]")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workflow_profile_snapshots (id, name, type, version, enabled, config_json, secrets_ciphertext, secret_fields_json, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, '', $7::jsonb, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, profile.SourceID, profile.Name, profile.Type, positiveInt64OrDefault(profile.Version, 1), profile.Enabled, config, secretFields); err != nil {
			return report, fmt.Errorf("import profile %s: %w", profile.SourceID, err)
		}
	}
	for _, workflow := range transfer.Workflows {
		createdBy, err := resolveTransferUser(ctx, tx, workflow.CreatedBy, options.ResolveCreatedBy)
		if err != nil {
			return report, fmt.Errorf("resolve owner for workflow %d: %w", workflow.SourceID, err)
		}
		var groupID any
		if workflow.GroupSourceID != nil {
			mapped, ok := groupIDs[*workflow.GroupSourceID]
			if !ok {
				return report, fmt.Errorf("workflow %d references unknown group %d", workflow.SourceID, *workflow.GroupSourceID)
			}
			groupID = mapped
		}
		status := workflow.Status
		if status != "active" {
			status = "inactive"
		}
		credentialsRequired := false
		for _, revision := range workflow.Revisions {
			if graphNeedsCredentials(revision.Graph, profilesByID) {
				credentialsRequired = true
				break
			}
		}
		if credentialsRequired {
			status = "inactive"
		}
		var workflowID int64
		if err := tx.QueryRowContext(ctx, "INSERT INTO workflows (name, description, group_id, status, retention_days, created_by, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP) RETURNING id", workflow.Name, workflow.Description, groupID, status, positiveOrDefault(workflow.RetentionDays, 30), createdBy).Scan(&workflowID); err != nil {
			return report, fmt.Errorf("import workflow %d: %w", workflow.SourceID, err)
		}
		revisionIDs := make(map[int64]int64, len(workflow.Revisions))
		for _, revision := range workflow.Revisions {
			var revisionID int64
			nodeVersions := revision.NodeVersions
			if len(nodeVersions) == 0 {
				nodeVersions = json.RawMessage("{}")
			}
			if err := tx.QueryRowContext(ctx, "INSERT INTO workflow_revisions (workflow_id, revision_number, graph_json, node_versions, created_by, created_at) VALUES ($1, $2, $3::jsonb, $4::jsonb, $5, CURRENT_TIMESTAMP) RETURNING id", workflowID, revision.RevisionNumber, revision.Graph, nodeVersions, createdBy).Scan(&revisionID); err != nil {
				return report, fmt.Errorf("import revision %d for workflow %d: %w", revision.RevisionNumber, workflow.SourceID, err)
			}
			revisionIDs[revision.SourceID] = revisionID
		}
		if workflow.ActiveSourceID != nil {
			activeID, ok := revisionIDs[*workflow.ActiveSourceID]
			if !ok {
				return report, fmt.Errorf("workflow %d references unknown active revision %d", workflow.SourceID, *workflow.ActiveSourceID)
			}
			if _, err := tx.ExecContext(ctx, "UPDATE workflows SET active_revision_id = $1 WHERE id = $2", activeID, workflowID); err != nil {
				return report, fmt.Errorf("activate workflow %d: %w", workflow.SourceID, err)
			}
		}
		if workflow.Runtime.MaxConcurrentRuns > 0 || workflow.Runtime.BacklogLimit > 0 {
			if _, err := tx.ExecContext(ctx, "INSERT INTO workflow_runtimes (workflow_id, max_concurrent_runs, backlog_limit, updated_at) VALUES ($1, $2, $3, CURRENT_TIMESTAMP)", workflowID, positiveOrDefault(workflow.Runtime.MaxConcurrentRuns, 2), positiveOrDefault(workflow.Runtime.BacklogLimit, 100)); err != nil {
				return report, fmt.Errorf("import runtime for workflow %d: %w", workflow.SourceID, err)
			}
		}
		reason := "imported"
		if credentialsRequired {
			reason = "imported; credentials required before activation"
			report.Warnings = append(report.Warnings, TransferItem{SourceID: workflow.SourceID, Name: workflow.Name, Reason: "profile credentials were intentionally excluded; workflow remains inactive"})
		}
		report.Imported = append(report.Imported, TransferItem{SourceID: workflow.SourceID, Name: workflow.Name, Reason: reason})
	}
	if err := tx.Commit(); err != nil {
		return report, fmt.Errorf("commit workflow transfer: %w", err)
	}
	sortTransferReport(&report)
	return report, nil
}

// normalizeWorkflowGraph converts only legacy shapes whose semantics are explicit.
// Ambiguous bindings and CEL expressions are rejected so migration never guesses.
func normalizeWorkflowGraph(raw json.RawMessage) (json.RawMessage, error) {
	var document map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil || document == nil {
		return nil, errors.New("graph is not valid JSON")
	}
	version, ok := legacyNumber(document["schemaVersion"])
	if !ok {
		return nil, errors.New("graph schemaVersion is missing")
	}
	if version == 3 {
		return append(json.RawMessage(nil), raw...), validateGraph3(raw)
	}
	if version != 1 && version != 2 {
		return nil, fmt.Errorf("unsupported graph schemaVersion %d", version)
	}
	nodes, ok := document["nodes"].([]any)
	if !ok || len(nodes) == 0 {
		return nil, errors.New("legacy graph must contain nodes")
	}
	for index, rawNode := range nodes {
		node, ok := rawNode.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("legacy node %d is not an object", index)
		}
		id, _ := node["nodeInstanceId"].(string)
		typ, _ := node["nodeType"].(string)
		ver, _ := node["nodeVersion"].(string)
		if strings.TrimSpace(id) == "" || strings.TrimSpace(typ) == "" || strings.TrimSpace(ver) == "" {
			return nil, fmt.Errorf("legacy node %d has incomplete identity", index)
		}
		if _, ok := node["config"].(map[string]any); !ok {
			return nil, fmt.Errorf("legacy node %q config must be an object", id)
		}
		if _, ok := node["position"].(map[string]any); !ok {
			return nil, fmt.Errorf("legacy node %q position is required", id)
		}
		if connectionID, exists := node["connectionId"].(string); exists {
			if profileID, alreadySet := node["profileId"].(string); alreadySet && profileID != connectionID {
				return nil, fmt.Errorf("legacy node %q has conflicting connectionId and profileId", id)
			}
			node["profileId"] = connectionID
			delete(node, "connectionId")
		}
		delete(node, "connectionType")
		delete(node, "connectionFields")
		if bindings, exists := node["inputBindings"]; exists {
			bindingMap, ok := bindings.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("legacy node %q inputBindings must be an object", id)
			}
			for field, rawBinding := range bindingMap {
				binding, ok := rawBinding.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("legacy node %q input binding %q is invalid", id, field)
				}
				kind, _ := binding["kind"].(string)
				switch kind {
				case "field":
					binding["kind"] = "node"
					if source, _ := binding["nodeInstanceId"].(string); strings.TrimSpace(source) == "" {
						return nil, fmt.Errorf("legacy node %q input binding %q has no source node", id, field)
					}
				case "node", "event", "profile", "literal":
				case "condition_subject", "condition_message", "connection":
					return nil, fmt.Errorf("legacy node %q input binding %q uses unsupported kind %q", id, field, kind)
				default:
					return nil, fmt.Errorf("legacy node %q input binding %q uses unsupported kind %q", id, field, kind)
				}
			}
		}
	}
	edges, ok := document["edges"].([]any)
	if !ok {
		return nil, errors.New("legacy graph must contain edges")
	}
	convertedEdges := make([]any, 0, len(edges)*2)
	for index, rawEdge := range edges {
		edge, ok := rawEdge.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("legacy edge %d is not an object", index)
		}
		condition, hasCondition := edge["condition"]
		if !hasCondition || condition == nil || strings.TrimSpace(fmt.Sprint(condition)) == "" {
			delete(edge, "condition")
			convertedEdges = append(convertedEdges, edge)
			continue
		}
		expression, ok := condition.(string)
		if !ok {
			return nil, fmt.Errorf("legacy edge %d condition must be a string", index)
		}
		rules, bindings, err := parseLegacyCondition(expression, edge)
		if err != nil {
			return nil, fmt.Errorf("legacy edge %d condition: %w", index, err)
		}
		edgeID, _ := edge["edgeId"].(string)
		source, _ := edge["sourceNodeInstanceId"].(string)
		target, _ := edge["targetNodeInstanceId"].(string)
		sourcePort, _ := edge["sourcePort"].(string)
		targetPort, _ := edge["targetPort"].(string)
		conditionID := edgeID + "__condition"
		if strings.TrimSpace(edgeID) == "" || strings.TrimSpace(source) == "" || strings.TrimSpace(target) == "" || len(conditionID) > 128 {
			return nil, fmt.Errorf("legacy edge %d has an invalid identity", index)
		}
		conditionNode := map[string]any{
			"nodeInstanceId": conditionID, "nodeType": "core.condition", "nodeVersion": "1.0.0",
			"config": map[string]any{"match": "all", "rules": rules}, "inputBindings": bindings,
			"position": map[string]any{"x": json.Number("0"), "y": json.Number("0")},
		}
		convertedEdges = append(convertedEdges,
			map[string]any{"edgeId": edgeID + "__input", "sourceNodeInstanceId": source, "sourcePort": sourcePort, "targetNodeInstanceId": conditionID, "targetPort": "in"},
			map[string]any{"edgeId": edgeID, "sourceNodeInstanceId": conditionID, "sourcePort": "true", "targetNodeInstanceId": target, "targetPort": targetPort},
		)
		nodes = append(nodes, conditionNode)
	}
	document["nodes"] = nodes
	document["edges"] = convertedEdges
	document["schemaVersion"] = 3
	converted, err := json.Marshal(document)
	if err != nil {
		return nil, errors.New("encode converted graph failed")
	}
	if err := validateGraph3(converted); err != nil {
		return nil, err
	}
	return converted, nil
}

func parseLegacyCondition(expression string, edge map[string]any) ([]map[string]any, map[string]any, error) {
	parts := strings.Split(expression, "&&")
	rules := make([]map[string]any, 0, len(parts))
	bindings := make(map[string]any, len(parts))
	for index, part := range parts {
		part = strings.TrimSpace(part)
		fields := strings.Fields(part)
		if len(fields) < 3 {
			return nil, nil, errors.New("unsupported condition expression; only simple comparisons joined by && can be converted")
		}
		left, operator := fields[0], fields[1]
		right := strings.TrimSpace(strings.TrimPrefix(part, fields[0]))
		right = strings.TrimSpace(strings.TrimPrefix(right, fields[1]))
		if !strings.HasPrefix(left, "input.") && !strings.HasPrefix(left, "event.") {
			return nil, nil, errors.New("condition must reference input or event")
		}
		path := strings.Split(strings.TrimPrefix(strings.TrimPrefix(left, "input."), "event."), ".")
		if len(path) == 0 || path[0] == "" {
			return nil, nil, errors.New("condition field path is invalid")
		}
		mapped := map[string]string{"==": "eq", "!=": "ne", ">": "gt", ">=": "gte", "<": "lt", "<=": "lte", "contains": "contains"}[operator]
		if mapped == "" {
			return nil, nil, fmt.Errorf("condition operator %q is unsupported", operator)
		}
		var value any
		decoder := json.NewDecoder(strings.NewReader(right))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return nil, nil, errors.New("condition value is not a JSON literal")
		}
		key := fmt.Sprintf("value%d", index)
		rules = append(rules, map[string]any{"fieldPath": []string{key}, "operator": mapped, "value": value})
		if strings.HasPrefix(left, "event.") {
			bindings[key] = map[string]any{"kind": "event", "fieldPath": path}
		} else {
			source, _ := edge["sourceNodeInstanceId"].(string)
			if source == "" {
				return nil, nil, errors.New("input condition has no source node")
			}
			bindings[key] = map[string]any{"kind": "node", "nodeInstanceId": source, "fieldPath": path}
		}
	}
	return rules, bindings, nil
}

func legacyNumber(value any) (int, bool) {
	switch value := value.(type) {
	case json.Number:
		parsed, err := strconv.Atoi(string(value))
		return parsed, err == nil
	case float64:
		return int(value), value == float64(int(value))
	default:
		return 0, false
	}
}

func validateGraph3(raw json.RawMessage) error {
	var graph struct {
		SchemaVersion int               `json:"schemaVersion"`
		Nodes         []json.RawMessage `json:"nodes"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &graph) != nil {
		return errors.New("graph is not valid JSON")
	}
	if graph.SchemaVersion != 3 {
		return fmt.Errorf("graph schemaVersion must be 3 (got %d); legacy conversion is not guessable", graph.SchemaVersion)
	}
	if len(graph.Nodes) == 0 {
		return errors.New("graph must contain at least one node")
	}
	return nil
}

func resolveTransferUser(ctx context.Context, tx *sql.Tx, sourceID int64, resolver func(context.Context, *sql.Tx, int64) (int64, error)) (int64, error) {
	if resolver != nil {
		id, err := resolver(ctx, tx, sourceID)
		if err != nil {
			return 0, err
		}
		if id > 0 {
			return id, nil
		}
	}
	var id int64
	if err := tx.QueryRowContext(ctx, "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id = u.id JOIN roles r ON r.id = ur.role_id WHERE u.is_active = TRUE AND r.code = 'R_SUPER' ORDER BY u.id LIMIT 1").Scan(&id); err != nil {
		return 0, errors.New("no active super administrator is available")
	}
	return id, nil
}

func decodeTransferGroup(raw []byte) (TransferGroup, error) {
	var row map[string]json.RawMessage
	if err := json.Unmarshal(raw, &row); err != nil {
		return TransferGroup{}, fmt.Errorf("decode workflow group: %w", err)
	}
	return TransferGroup{SourceID: rawInt64(row, "id"), Name: rawString(row, "name"), SortOrder: int(rawInt64(row, "sort_order"))}, nil
}

func decodeTransferWorkflow(raw []byte) (TransferWorkflow, error) {
	var row map[string]json.RawMessage
	if err := json.Unmarshal(raw, &row); err != nil {
		return TransferWorkflow{}, fmt.Errorf("decode workflow: %w", err)
	}
	var groupID *int64
	if value := rawInt64(row, "group_id"); value > 0 {
		groupID = &value
	}
	var activeID *int64
	if value := rawInt64(row, "active_revision_id"); value > 0 {
		activeID = &value
	}
	triggerID := rawString(row, "main_trigger_node_id")
	if triggerID == "" {
		triggerID = rawString(row, "main_trigger_id")
	}
	return TransferWorkflow{SourceID: rawInt64(row, "id"), Name: rawString(row, "name"), Description: rawString(row, "description"), GroupSourceID: groupID, MainTriggerID: triggerID, Status: rawString(row, "status"), RetentionDays: int(rawInt64(row, "retention_days")), CreatedBy: rawInt64(row, "created_by"), CreatedAt: rawString(row, "created_at"), UpdatedAt: rawString(row, "updated_at"), ActiveSourceID: activeID}, nil
}

func decodeTransferProfile(raw []byte) (TransferProfile, error) {
	var row map[string]json.RawMessage
	if err := json.Unmarshal(raw, &row); err != nil {
		return TransferProfile{}, fmt.Errorf("decode workflow profile: %w", err)
	}
	config := rawValue(row, "config_json")
	if len(config) == 0 {
		config = rawValue(row, "config")
	}
	secretFields := rawValue(row, "secret_fields_json")
	if len(secretFields) == 0 {
		secretFields = json.RawMessage("[]")
	}
	return TransferProfile{SourceID: rawString(row, "id"), Name: rawString(row, "name"), Type: rawString(row, "type"), Version: rawInt64(row, "version"), Enabled: rawBool(row, "enabled"), Config: config, SecretFields: secretFields}, nil
}

func decodeTransferRevision(raw []byte) (TransferRevision, error) {
	var row map[string]json.RawMessage
	if err := json.Unmarshal(raw, &row); err != nil {
		return TransferRevision{}, fmt.Errorf("decode workflow revision: %w", err)
	}
	graph := rawValue(row, "graph_json")
	if len(graph) == 0 {
		graph = rawValue(row, "graph")
	}
	nodeVersions := rawValue(row, "node_versions")
	if len(nodeVersions) == 0 {
		nodeVersions = json.RawMessage("{}")
	}
	return TransferRevision{SourceID: rawInt64(row, "id"), RevisionNumber: rawInt64(row, "revision_number"), Graph: graph, NodeVersions: nodeVersions, CreatedBy: rawInt64(row, "created_by"), CreatedAt: rawString(row, "created_at")}, nil
}

func rawString(row map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(row[key], &value)
	return value
}

func rawInt64(row map[string]json.RawMessage, key string) int64 {
	var value int64
	if json.Unmarshal(row[key], &value) == nil {
		return value
	}
	var text string
	if json.Unmarshal(row[key], &text) == nil {
		value, _ = strconv.ParseInt(text, 10, 64)
	}
	return value
}

func rawBool(row map[string]json.RawMessage, key string) bool {
	var value bool
	_ = json.Unmarshal(row[key], &value)
	return value
}

func rawValue(row map[string]json.RawMessage, key string) json.RawMessage {
	if value := row[key]; len(value) > 0 && string(value) != "null" {
		return append(json.RawMessage(nil), value...)
	}
	return nil
}

func positiveOrDefault(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func positiveInt64OrDefault(value, fallback int64) int64 {
	if value > 0 {
		return value
	}
	return fallback
}

func isMissingRelation(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "does not exist") || strings.Contains(text, "undefined table")
}

// graphNeedsCredentials reports the deliberate post-cutover state: exported
// profile secrets never travel in the transfer file, so any legacy profile
// reference must leave the workflow inactive until the operator re-enters it.
func graphNeedsCredentials(raw json.RawMessage, profiles map[string]TransferProfile) bool {
	var document any
	if json.Unmarshal(raw, &document) != nil {
		return true
	}
	var visit func(any) bool
	visit = func(value any) bool {
		switch value := value.(type) {
		case []any:
			for _, item := range value {
				if visit(item) {
					return true
				}
			}
		case map[string]any:
			if profileID, ok := value["profileId"].(string); ok && strings.TrimSpace(profileID) != "" {
				profile, exists := profiles[profileID]
				if !exists || profileHasSecrets(profile.SecretFields) {
					return true
				}
			}
			if profileRefs, ok := value["profileRefs"].([]any); ok {
				for _, item := range profileRefs {
					ref, ok := item.(map[string]any)
					if !ok {
						return true
					}
					profileID, _ := ref["profileId"].(string)
					if profileID != "" {
						if profile, exists := profiles[profileID]; exists && profileHasSecrets(profile.SecretFields) {
							return true
						}
					}
				}
			}
			for _, item := range value {
				if visit(item) {
					return true
				}
			}
		}
		return false
	}
	return visit(document)
}

func profileHasSecrets(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var fields map[string]bool
	if json.Unmarshal(raw, &fields) == nil {
		for _, present := range fields {
			if present {
				return true
			}
		}
		return false
	}
	var names []string
	if json.Unmarshal(raw, &names) == nil {
		return len(names) > 0
	}
	return true
}

func sortTransferReport(report *TransferReport) {
	sort.Slice(report.Imported, func(i, j int) bool { return report.Imported[i].SourceID < report.Imported[j].SourceID })
	sort.Slice(report.Warnings, func(i, j int) bool {
		if report.Warnings[i].SourceID != report.Warnings[j].SourceID {
			return report.Warnings[i].SourceID < report.Warnings[j].SourceID
		}
		return report.Warnings[i].Reason < report.Warnings[j].Reason
	})
	sort.Slice(report.Skipped, func(i, j int) bool { return report.Skipped[i].SourceID < report.Skipped[j].SourceID })
	sort.Slice(report.Errors, func(i, j int) bool {
		if report.Errors[i].SourceID != report.Errors[j].SourceID {
			return report.Errors[i].SourceID < report.Errors[j].SourceID
		}
		return report.Errors[i].Reason < report.Errors[j].Reason
	})
}

func WriteTransferReport(path string, report TransferReport) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create transfer report: %w", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
