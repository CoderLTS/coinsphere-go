package service

import (
	"strconv"
	"strings"
	"unicode"

	"coinsphere/backend/internal/db"
)

var startNodeTypes = map[string]bool{
	"start.manual": true, "start.schedule": true, "start.event": true, "start.webhook": true,
}

// ---------- 图校验 ----------

// validateWorkflowGraph 落库与执行前的图结构校验。
func validateWorkflowGraph(graph M) error {
	nodesAny, _ := graph["nodes"].([]any)
	edgesAny, edgesOK := graph["edges"].([]any)
	if graph["edges"] != nil && !edgesOK {
		return bizErr("Workflow edges must be a list")
	}
	if len(nodesAny) == 0 {
		return bizErr("Workflow must contain at least one node")
	}

	nodes := make([]M, 0, len(nodesAny))
	nodeMap := map[string]M{}
	nodeIDs := make([]string, 0, len(nodesAny))
	for _, nodeAny := range nodesAny {
		node, _ := nodeAny.(map[string]any)
		nodeID := strings.TrimSpace(asString(node["id"]))
		if nodeID == "" {
			return bizErr("Workflow node id cannot be empty")
		}
		if _, exists := nodeMap[nodeID]; exists {
			return bizErr("Workflow node id must be unique")
		}
		nodes = append(nodes, node)
		nodeMap[nodeID] = node
		nodeIDs = append(nodeIDs, nodeID)
	}

	startNodes := []string{}
	endNodes := []string{}
	for _, nodeID := range nodeIDs {
		nodeType := asString(nodeMap[nodeID]["type"])
		if startNodeTypes[nodeType] {
			startNodes = append(startNodes, nodeID)
		}
		if nodeType == "end" {
			endNodes = append(endNodes, nodeID)
		}
	}
	if len(startNodes) == 0 {
		return bizErr("Workflow must contain at least one start node")
	}
	if err := assertStartEntries(nodeMap, startNodes); err != nil {
		return err
	}
	if len(endNodes) == 0 {
		return bizErr("Workflow must contain at least one end node")
	}

	adjacency := map[string][]M{}
	incoming := map[string]int{}
	outgoing := map[string]int{}
	edges := make([]M, 0, len(edgesAny))
	for _, edgeAny := range edgesAny {
		edge, _ := edgeAny.(map[string]any)
		source := strings.TrimSpace(asString(edge["source"]))
		target := strings.TrimSpace(asString(edge["target"]))
		if nodeMap[source] == nil || nodeMap[target] == nil {
			return bizErr("Workflow contains an invalid edge")
		}
		if source == target {
			return bizErr("Workflow does not allow self loops")
		}
		adjacency[source] = append(adjacency[source], edge)
		incoming[target]++
		outgoing[source]++
		edges = append(edges, edge)
	}

	startSet := map[string]bool{}
	for _, startNodeID := range startNodes {
		startSet[startNodeID] = true
		if incoming[startNodeID] != 0 {
			return bizErr("Start node cannot have incoming edges")
		}
		if outgoing[startNodeID] == 0 {
			return bizErr("Start node must have at least one outgoing edge")
		}
	}

	for _, nodeID := range nodeIDs {
		count := incoming[nodeID]
		if !startSet[nodeID] && count == 0 {
			return bizErr("Every non-start node must be reachable")
		}
		if count > 1 && !allowMultiIncomingFromStarts(nodeID, edges, nodeMap) {
			return bizErr("Current workflow model only allows multi-incoming when all sources are start nodes")
		}
	}

	for _, nodeID := range nodeIDs {
		nodeType := asString(nodeMap[nodeID]["type"])
		edgesFromNode := adjacency[nodeID]
		if nodeType == "end" && len(edgesFromNode) > 0 {
			return bizErr("End node cannot have outgoing edges")
		}
		if nodeType == "condition.branch" {
			branchKeys := map[string]bool{}
			for _, edge := range edgesFromNode {
				branchKeys[strings.TrimSpace(edgeBranchKey(edge))] = true
			}
			if len(edgesFromNode) < 2 || !branchKeys["true"] || !branchKeys["false"] || len(branchKeys) != 2 {
				return bizErr("Condition node must contain true and false branches")
			}
		}
		if nodeType == "foreach" {
			if len(edgesFromNode) != 1 {
				return bizErr("Foreach node currently supports exactly one successor")
			}
			if err := assertForeachBranchValid(nodeID, nodeMap, adjacency); err != nil {
				return err
			}
		}
	}

	if err := assertNoEventPublishFeedback(nodeMap, adjacency, startNodes); err != nil {
		return err
	}
	if err := assertAcyclic(adjacency, nodeIDs); err != nil {
		return err
	}
	visited := dfsReachable(adjacency, startNodes)
	if len(visited) != len(nodeIDs) {
		return bizErr("Workflow contains unreachable nodes")
	}
	return nil
}

func assertStartEntries(nodeMap map[string]M, startNodes []string) error {
	entryKeys := map[string]bool{}
	for _, nodeID := range startNodes {
		config, _ := nodeMap[nodeID]["config"].(map[string]any)
		entryKey := strings.TrimSpace(asString(config["entryKey"]))
		if entryKey == "" {
			return bizErr("Each start node must define entryKey")
		}
		if len(entryKey) > 64 {
			return bizErr("entryKey length must be <= 64")
		}
		for _, ch := range entryKey {
			if !unicode.IsLower(ch) && !unicode.IsDigit(ch) && ch != '.' && ch != '_' && ch != '-' {
				return bizErr("entryKey only allows lowercase letters, digits, dot, underscore, and hyphen")
			}
		}
		if entryKeys[entryKey] {
			return bizErr("entryKey must be unique within one workflow definition")
		}
		entryKeys[entryKey] = true
	}
	return nil
}

func allowMultiIncomingFromStarts(nodeID string, edges []M, nodeMap map[string]M) bool {
	sources := []string{}
	for _, edge := range edges {
		if asString(edge["target"]) == nodeID {
			sources = append(sources, asString(edge["source"]))
		}
	}
	if len(sources) == 0 {
		return false
	}
	for _, source := range sources {
		node := nodeMap[source]
		if node == nil || !startNodeTypes[asString(node["type"])] {
			return false
		}
	}
	return true
}

func assertForeachBranchValid(foreachNodeID string, nodeMap map[string]M, adjacency map[string][]M) error {
	target := asString(adjacency[foreachNodeID][0]["target"])
	stack := []string{target}
	visited := map[string]bool{}
	for len(stack) > 0 {
		nodeID := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[nodeID] {
			continue
		}
		visited[nodeID] = true
		nodeType := asString(nodeMap[nodeID]["type"])
		if nodeType == "foreach" {
			return bizErr("Nested foreach is not supported")
		}
		nextEdges := adjacency[nodeID]
		if len(nextEdges) == 0 && nodeType != "end" {
			return bizErr("Foreach branch must end with an end node")
		}
		for _, edge := range nextEdges {
			stack = append(stack, asString(edge["target"]))
		}
	}
	return nil
}

func assertNoEventPublishFeedback(nodeMap map[string]M, adjacency map[string][]M, startNodes []string) error {
	for _, startNodeID := range startNodes {
		startNode := nodeMap[startNodeID]
		if asString(startNode["type"]) != "start.event" {
			continue
		}
		config, _ := startNode["config"].(map[string]any)
		eventType := strings.TrimSpace(asString(config["eventType"]))
		if eventType == "" {
			return bizErr("start.event node must define eventType")
		}
		stack := []string{}
		for _, edge := range adjacency[startNodeID] {
			stack = append(stack, asString(edge["target"]))
		}
		visited := map[string]bool{}
		for len(stack) > 0 {
			nodeID := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if nodeID == "" || visited[nodeID] {
				continue
			}
			visited[nodeID] = true
			node := nodeMap[nodeID]
			if asString(node["type"]) == "event.publish" {
				nodeConfig, _ := node["config"].(map[string]any)
				if strings.TrimSpace(asString(nodeConfig["eventType"])) == eventType {
					return bizErr("start.event downstream graph cannot publish the same event type")
				}
			}
			for _, edge := range adjacency[nodeID] {
				stack = append(stack, asString(edge["target"]))
			}
		}
	}
	return nil
}

func assertAcyclic(adjacency map[string][]M, nodeIDs []string) error {
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var dfs func(nodeID string) error
	dfs = func(nodeID string) error {
		if visiting[nodeID] {
			return bizErr("Current workflow model only supports DAG graphs")
		}
		if visited[nodeID] {
			return nil
		}
		visiting[nodeID] = true
		for _, edge := range adjacency[nodeID] {
			if err := dfs(asString(edge["target"])); err != nil {
				return err
			}
		}
		delete(visiting, nodeID)
		visited[nodeID] = true
		return nil
	}
	for _, nodeID := range nodeIDs {
		if !visited[nodeID] {
			if err := dfs(nodeID); err != nil {
				return err
			}
		}
	}
	return nil
}

func dfsReachable(adjacency map[string][]M, startIDs []string) map[string]bool {
	visited := map[string]bool{}
	stack := append([]string{}, startIDs...)
	for len(stack) > 0 {
		nodeID := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[nodeID] {
			continue
		}
		visited[nodeID] = true
		for _, edge := range adjacency[nodeID] {
			stack = append(stack, asString(edge["target"]))
		}
	}
	return visited
}

func edgeBranchKey(edge M) string {
	branch := asString(edge["branch"])
	if branch == "" {
		branch = asString(edge["label"])
	}
	return branch
}

// ---------- 定义服务 ----------

const workflowCodeMaxLength = 120

// WorkflowDefinitionUpsertPayload 定义创建/编辑载荷。
type WorkflowDefinitionUpsertPayload struct {
	Code        string `json:"code"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Graph       M      `json:"graph"`
}

// ValidateWorkflowDefinition 仅校验不落库。
func (a *App) ValidateWorkflowDefinition(payload WorkflowDefinitionUpsertPayload) M {
	graph := payload.Graph
	if graph == nil {
		graph = M{}
	}
	issues := []M{}
	if err := validateWorkflowGraph(graph); err != nil {
		issues = append(issues, M{"scope": "graph", "level": "error", "message": err.Error()})
	}
	return M{"valid": len(issues) == 0, "issues": issues}
}

// ListWorkflowDefinitions 每个 code 最新版本摘要。
func (a *App) ListWorkflowDefinitions() ([]M, error) {
	all := a.listAllDefinitions()
	versionMap := map[int64]int{}
	for _, item := range all {
		versionMap[item.ID] = item.Version
	}
	seenCodes := map[string]bool{}
	definitions := make([]db.WorkflowDefinition, 0)
	for _, item := range all {
		if seenCodes[item.Code] {
			continue
		}
		seenCodes[item.Code] = true
		definitions = append(definitions, item)
	}
	stateMap := a.runtimeStateMap()
	executionCounts := a.countExecutionsByDefinitionIDs(collectIDs(all, func(d db.WorkflowDefinition) int64 { return d.ID }))

	result := make([]M, 0, len(definitions))
	for i := range definitions {
		definition := definitions[i]
		state := stateMap[definition.Code]
		executionCount := int64(0)
		if state != nil && state.ActiveWorkflowDefinitionID != nil {
			executionCount = executionCounts[*state.ActiveWorkflowDefinitionID]
		}
		result = append(result, a.serializeDefinition(&definition, state, executionCount, resolveActiveVersion(state, versionMap)))
	}
	return result, nil
}

// GetWorkflowDefinition 定义详情 + 版本列表。
func (a *App) GetWorkflowDefinition(definitionID int64) (M, error) {
	definition, err := a.requireDefinition(definitionID)
	if err != nil {
		return nil, err
	}
	state := a.getRuntimeStateByCode(definition.Code)
	var versions []db.WorkflowDefinition
	a.DB.Where("code = ?", definition.Code).Order("version DESC, id DESC").Find(&versions)
	versionMap := map[int64]int{}
	for _, item := range versions {
		versionMap[item.ID] = item.Version
	}
	versionCounts := a.countExecutionsByDefinitionIDs(collectIDs(versions, func(d db.WorkflowDefinition) int64 { return d.ID }))

	var executionCount int64
	a.DB.Model(&db.WorkflowExecution{}).Where("workflow_definition_id = ?", definitionID).Count(&executionCount)
	data := a.serializeDefinition(definition, state, executionCount, resolveActiveVersion(state, versionMap))
	versionItems := make([]M, 0, len(versions))
	for i := range versions {
		version := versions[i]
		isActive := state != nil && state.ActiveWorkflowDefinitionID != nil && *state.ActiveWorkflowDefinitionID == version.ID
		versionItems = append(versionItems, M{
			"id": version.ID, "version": version.Version, "displayName": version.DisplayName,
			"isLatest": a.isLatestVersion(&version), "isBuiltin": version.IsBuiltin, "isActive": isActive,
			"executionCount": versionCounts[version.ID],
			"createdBy":      nilOrValue(version.CreatedBy), "createdAt": fmtTimeV(version.CreatedAt),
		})
	}
	data["versions"] = versionItems
	return data, nil
}

// CreateWorkflowDefinition 创建 v1。
func (a *App) CreateWorkflowDefinition(payload WorkflowDefinitionUpsertPayload, operatorUserID int64) (M, error) {
	displayName := strings.TrimSpace(payload.DisplayName)
	if displayName == "" {
		return nil, bizErr("Workflow display name is required")
	}
	code := a.generateWorkflowCode(displayName)
	return a.createVersionRow(code, 1, payload, false, operatorUserID)
}

// UpdateWorkflowDefinition 编辑生成新版本。
func (a *App) UpdateWorkflowDefinition(definitionID int64, payload WorkflowDefinitionUpsertPayload, operatorUserID int64) (M, error) {
	definition, err := a.requireDefinition(definitionID)
	if err != nil {
		return nil, err
	}
	var versionCount int64
	a.DB.Model(&db.WorkflowDefinition{}).Where("code = ?", definition.Code).Count(&versionCount)

	merged := payload
	merged.Code = definition.Code
	if strings.TrimSpace(merged.DisplayName) == "" {
		merged.DisplayName = definition.DisplayName
	}
	if merged.Graph == nil || len(merged.Graph) == 0 {
		merged.Graph = loadJSONObject(definition.GraphJSON)
	}
	return a.createVersionRow(definition.Code, int(versionCount)+1, merged, definition.IsBuiltin, operatorUserID)
}

// DeleteWorkflowDefinition 删除未激活且无执行历史的版本。
func (a *App) DeleteWorkflowDefinition(definitionID int64) error {
	definition, err := a.requireDefinition(definitionID)
	if err != nil {
		return err
	}
	state := a.getRuntimeStateByCode(definition.Code)
	if state != nil && state.ActiveWorkflowDefinitionID != nil && *state.ActiveWorkflowDefinitionID == definitionID {
		return bizErr("Active workflow definition cannot be deleted")
	}
	var executionCount int64
	a.DB.Model(&db.WorkflowExecution{}).Where("workflow_definition_id = ?", definitionID).Count(&executionCount)
	if executionCount > 0 {
		return bizErr("Workflow definition with execution history cannot be deleted")
	}
	return a.DB.Delete(definition).Error
}

func (a *App) createVersionRow(code string, version int, payload WorkflowDefinitionUpsertPayload, isBuiltin bool, operatorUserID int64) (M, error) {
	displayName := strings.TrimSpace(payload.DisplayName)
	if displayName == "" {
		return nil, bizErr("Workflow display name is required")
	}
	validation := a.ValidateWorkflowDefinition(payload)
	if valid, _ := validation["valid"].(bool); !valid {
		issues := validation["issues"].([]M)
		return nil, bizErr("%s", asString(issues[0]["message"]))
	}
	graph := payload.Graph
	if graph == nil {
		graph = M{}
	}
	definition := db.WorkflowDefinition{
		Code: code, Version: version, DisplayName: displayName,
		Description: strings.TrimSpace(payload.Description),
		GraphJSON:   dumpJSON(graph), IsBuiltin: isBuiltin,
		CreatedBy: &operatorUserID, CreatedAt: timeNow(),
	}
	if err := a.DB.Create(&definition).Error; err != nil {
		return nil, err
	}
	return a.GetWorkflowDefinition(definition.ID)
}

// ---------- 内部 ----------

func (a *App) listAllDefinitions() []db.WorkflowDefinition {
	var all []db.WorkflowDefinition
	a.DB.Order("code ASC, version DESC, id DESC").Find(&all)
	return all
}

func (a *App) listLatestDefinitions() []db.WorkflowDefinition {
	all := a.listAllDefinitions()
	seen := map[string]bool{}
	result := make([]db.WorkflowDefinition, 0)
	for _, item := range all {
		if seen[item.Code] {
			continue
		}
		seen[item.Code] = true
		result = append(result, item)
	}
	return result
}

func (a *App) requireDefinition(definitionID int64) (*db.WorkflowDefinition, error) {
	var definition db.WorkflowDefinition
	if err := a.DB.First(&definition, definitionID).Error; err != nil {
		return nil, bizErr("Workflow definition does not exist")
	}
	return &definition, nil
}

func (a *App) getRuntimeStateByCode(workflowCode string) *db.WorkflowRuntimeState {
	var state db.WorkflowRuntimeState
	if err := a.DB.Where("workflow_code = ?", workflowCode).First(&state).Error; err != nil {
		return nil
	}
	return &state
}

func (a *App) runtimeStateMap() map[string]*db.WorkflowRuntimeState {
	var states []db.WorkflowRuntimeState
	a.DB.Find(&states)
	result := map[string]*db.WorkflowRuntimeState{}
	for i := range states {
		result[states[i].WorkflowCode] = &states[i]
	}
	return result
}

func (a *App) countExecutionsByDefinitionIDs(definitionIDs []int64) map[int64]int64 {
	result := map[int64]int64{}
	if len(definitionIDs) == 0 {
		return result
	}
	var rows []struct {
		WorkflowDefinitionID int64
		Count                int64
	}
	a.DB.Model(&db.WorkflowExecution{}).
		Select("workflow_definition_id, COUNT(id) AS count").
		Where("workflow_definition_id IN ?", definitionIDs).
		Group("workflow_definition_id").Scan(&rows)
	for _, row := range rows {
		result[row.WorkflowDefinitionID] = row.Count
	}
	return result
}

func (a *App) isLatestVersion(definition *db.WorkflowDefinition) bool {
	var latest db.WorkflowDefinition
	if err := a.DB.Where("code = ?", definition.Code).Order("version DESC, id DESC").First(&latest).Error; err != nil {
		return false
	}
	return latest.ID == definition.ID
}

func (a *App) serializeDefinition(definition *db.WorkflowDefinition, state *db.WorkflowRuntimeState, executionCount int64, activeVersion any) M {
	var activeDefinitionID any
	isActive := false
	isWorkflowActive := false
	if state != nil && state.ActiveWorkflowDefinitionID != nil {
		activeDefinitionID = *state.ActiveWorkflowDefinitionID
		isWorkflowActive = true
		isActive = *state.ActiveWorkflowDefinitionID == definition.ID
	}
	return M{
		"id": definition.ID, "code": definition.Code, "version": definition.Version,
		"displayName": definition.DisplayName, "description": definition.Description,
		"graph":    loadJSONObject(definition.GraphJSON),
		"isLatest": a.isLatestVersion(definition), "isBuiltin": definition.IsBuiltin,
		"isActive": isActive, "isWorkflowActive": isWorkflowActive,
		"activeDefinitionId": activeDefinitionID, "activeVersion": activeVersion,
		"executionCount": executionCount,
		"createdBy":      nilOrValue(definition.CreatedBy), "createdAt": fmtTimeV(definition.CreatedAt),
	}
}

func resolveActiveVersion(state *db.WorkflowRuntimeState, versionMap map[int64]int) any {
	if state == nil || state.ActiveWorkflowDefinitionID == nil {
		return nil
	}
	if version, ok := versionMap[*state.ActiveWorkflowDefinitionID]; ok {
		return version
	}
	return nil
}

func (a *App) generateWorkflowCode(displayName string) string {
	base := buildWorkflowCodeBase(displayName)
	candidate := base
	index := 2
	for {
		var count int64
		a.DB.Model(&db.WorkflowDefinition{}).Where("code = ?", candidate).Count(&count)
		if count == 0 {
			return candidate
		}
		suffix := "-" + itoa(index)
		available := workflowCodeMaxLength - len(suffix)
		truncated := strings.TrimRight(truncateBytes(base, available), "-._")
		if truncated == "" {
			truncated = "workflow"
		}
		candidate = truncated + suffix
		index++
	}
}

func buildWorkflowCodeBase(displayName string) string {
	var builder strings.Builder
	lastIsSeparator := true
	for _, char := range strings.TrimSpace(displayName) {
		if isWorkflowCodeChar(char) {
			builder.WriteRune(unicode.ToLower(char))
			lastIsSeparator = false
			continue
		}
		if !lastIsSeparator {
			builder.WriteRune('-')
			lastIsSeparator = true
		}
	}
	value := builder.String()
	for strings.Contains(value, "--") {
		value = strings.ReplaceAll(value, "--", "-")
	}
	value = strings.Trim(value, "-._")
	value = strings.Trim(truncateBytes(value, workflowCodeMaxLength), "-._")
	if value == "" {
		return "workflow"
	}
	return value
}

func isWorkflowCodeChar(char rune) bool {
	if char < 128 {
		return unicode.IsLetter(char) || unicode.IsDigit(char)
	}
	return unicode.IsLetter(char) || unicode.IsNumber(char)
}

func truncateBytes(value string, max int) string {
	if len(value) <= max {
		return value
	}
	runes := []rune(value)
	for len(string(runes)) > max && len(runes) > 0 {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}

func itoa(value int) string { return strconv.Itoa(value) }

func nilOrValue(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
