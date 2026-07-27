// 本文件是“工作流定义”的服务层:负责工作流定义的增删改查(CRUD)、把图结构在 JSON 字符串与
// map 之间序列化存取,以及最核心的“图校验”——在落库/执行前检查这张流程图是否合法(有起点终点、
// 无自环、无环、全连通、分支完整等)。见 GO入门笔记『方法与接收者』『框架:GORM』。

package service

import (
	"strconv"
	"strings"
	"unicode"

	"coinsphere/backend/internal/db"
)

// startNodeTypes 用 map[string]bool 当“集合”用:键是允许的起始节点类型,值恒为 true。
// 判断某类型是否是起点,只要看 startNodeTypes[t] 是否为 true(map 里不存在的键取值就是 false)。
var startNodeTypes = map[string]bool{
	"start.manual": true, "start.schedule": true, "start.event": true, "start.webhook": true,
}

// ---------- 图校验 ----------

// validateWorkflowGraph 落库与执行前的图结构校验。
func validateWorkflowGraph(graph M) error {
	// 参数类型 M 是别名 map[string]any(见 app.go),即“字符串键 → 任意值”的 JSON 对象。
	// graph["nodes"] 取出的值类型是 any,.([]any) 是“类型断言”:把它断言成 []any(any 的 slice)。
	// 双返回值形式 v, ok := x.(T) 断言失败也不会崩,只是 ok=false、v 取零值;这里用 _ 忽略了 ok。
	// := 是短变量声明,自动推断类型。见 GO入门笔记『变量、函数、错误』『struct、指针、slice、map』。
	nodesAny, _ := graph["nodes"].([]any)
	edgesAny, edgesOK := graph["edges"].([]any)
	// bizErr(...) 返回一个“业务错误”(普通 error)。校验不通过就把它 return 出去,告诉调用方原因。
	if graph["edges"] != nil && !edgesOK {
		return bizErr("Workflow edges must be a list")
	}
	if len(nodesAny) == 0 {
		return bizErr("Workflow must contain at least one node")
	}

	// make([]M, 0, n) 建一个空 slice 并预留容量 n;nodeMap 用来按 id 快速定位节点。
	nodes := make([]M, 0, len(nodesAny))
	nodeMap := map[string]M{}
	nodeIDs := make([]string, 0, len(nodesAny))
	// range 遍历 slice,_ 丢弃下标、nodeAny 是元素。见 GO入门笔记『struct、指针、slice、map』。
	for _, nodeAny := range nodesAny {
		// 把每个元素断言成 map,再用 asString(把 any 安全转成字符串)取出 id 并去掉首尾空白。
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
	// Go 惯用法:if 可先跑一句初始化再判断。这里先调用校验、把返回错误存进 err,紧接着判断
	// err != nil(非空即出错)就上抛;err 的作用域仅限这个 if。见 GO入门笔记『变量、函数、错误』。
	if err := assertStartEntries(nodeMap, startNodes); err != nil {
		return err
	}
	if len(endNodes) == 0 {
		return bizErr("Workflow must contain at least one end node")
	}

	// 下面把连线整理成便于分析的结构:adjacency 是“邻接表”(节点 id → 从它出发的边列表);
	// incoming/outgoing 分别统计每个节点的入度、出度(有多少条边指入/指出)。
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

	// 按节点类型分别校验其“出边”是否合法:end 不能再有出边;condition.branch 必须同时有
	// true 和 false 两条分支;foreach(遍历)当前只允许恰好一条后继边。
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

	// 最后三道整体检查:① 事件工作流不能把自己触发的同类事件又发出去(防死循环);
	// ② 整张图必须是 DAG(有向无环图),不能有环;③ 从起点出发必须能走到所有节点(无孤岛)。
	if err := assertNoEventPublishFeedback(nodeMap, adjacency, startNodes); err != nil {
		return err
	}
	if err := assertAcyclic(adjacency, nodeIDs); err != nil {
		return err
	}
	// dfsReachable 从所有起点做深度优先遍历,返回能到达的节点集合;数量对不上就说明有不可达节点。
	visited := dfsReachable(adjacency, startNodes)
	if len(visited) != len(nodeIDs) {
		return bizErr("Workflow contains unreachable nodes")
	}
	return nil
}

// assertStartEntries 校验每个起始节点都有唯一、合法的 entryKey(触发入口标识)。
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
		// range 一个 string 时,每轮拿到的是一个 rune(Unicode 码点,类型 int32);逐字符校验合法性。
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

// allowMultiIncomingFromStarts 判断某节点的多条入边是否“全部来自起始节点”——只有这种多入度才允许。
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

// assertForeachBranchValid 校验 foreach 分支体:内部不能再嵌套 foreach,且分支必须以 end 节点收尾。
func assertForeachBranchValid(foreachNodeID string, nodeMap map[string]M, adjacency map[string][]M) error {
	target := asString(adjacency[foreachNodeID][0]["target"])
	// 用一个 slice 当“栈”做深度优先遍历:stack[len-1] 取栈顶,stack[:len-1] 弹出栈顶(切片再切片)。
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

// assertNoEventPublishFeedback 防止“自触发死循环”:start.event 监听某事件时,其下游不能再
// 发布(event.publish)同一种事件类型,否则会无限自我触发。
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

// assertAcyclic 用深度优先搜索判断图里有没有环(有环就不是 DAG,不允许)。
func assertAcyclic(adjacency map[string][]M, nodeIDs []string) error {
	// visiting = “当前递归路径上”的节点集合,visited = “已彻底处理完”的节点集合。
	visiting := map[string]bool{}
	visited := map[string]bool{}
	// 因为要递归调用自己,先用 var 声明函数变量 dfs,再把匿名函数赋给它(匿名递归函数的标准写法)。
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

// dfsReachable 从给定起点集合出发遍历,返回所有可达节点的集合(用 map 当集合)。
func dfsReachable(adjacency map[string][]M, startIDs []string) map[string]bool {
	visited := map[string]bool{}
	// append(dst, src...) 里的 ... 表示“把 src 这个 slice 展开成一个个元素追加”,这里等于复制一份 startIDs。
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
	// 反引号里的 `json:"code"` 是 struct tag:告诉 JSON 库前端字段名与这里字段的对应关系
	// (前端 code → Code)。Graph 字段类型是 M(map),对应一段任意结构的 JSON 对象。
	Code        string `json:"code"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Graph       M      `json:"graph"`
}

// ValidateWorkflowDefinition 仅校验不落库。
func (a *App) ValidateWorkflowDefinition(payload WorkflowDefinitionUpsertPayload) M {
	// (a *App) 是“方法接收者”:说明这是挂在 *App 类型上的方法,方法内用 a 指代当前 App 实例
	// (类似别的语言的 this/self)。*App 是指针接收者,共享同一个 App、能读它的 a.DB 等字段。
	// 见 GO入门笔记『方法与接收者』。返回类型 M(map)会拼成 {valid, issues} 的结果;下面 graph
	// 为 nil(空/没有)时兜底成空 map,避免后续对 nil 取值出错。
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
	// all 已按 code 升序、version 降序排好,故每个 code 第一次遇到的就是最新版本;
	// 用 seenCodes 当集合去重,只保留每个 code 的最新一条。
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
	// 查这个 code 的所有版本:等价 SQL 是 SELECT * FROM ... WHERE code = ? ORDER BY version DESC, id DESC。
	// Find(&versions) 把多行结果回填进 versions 这个 slice。见 GO入门笔记『框架:GORM』。
	a.DB.Where("code = ?", definition.Code).Order("version DESC, id DESC").Find(&versions)
	versionMap := map[int64]int{}
	for _, item := range versions {
		versionMap[item.ID] = item.Version
	}
	versionCounts := a.countExecutionsByDefinitionIDs(collectIDs(versions, func(d db.WorkflowDefinition) int64 { return d.ID }))

	var executionCount int64
	// Count 统计条数:等价 SELECT COUNT(*) FROM workflow_execution WHERE workflow_definition_id = ?,结果写进 executionCount。
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
	// 校验必填:去掉首尾空白后名称不能为空。返回 (nil, error) 表示失败——Go 靠“值 + error”双返回来报错。
	// 通过后由 generateWorkflowCode 生成库内唯一 code,再建第 1 版(version=1)。
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
	// 编辑不是原地改,而是新建一个“版本号 +1”的新行。先数一下这个 code 已有多少个版本。
	var versionCount int64
	a.DB.Model(&db.WorkflowDefinition{}).Where("code = ?", definition.Code).Count(&versionCount)

	// merged := payload 是值拷贝(struct 直接赋值即复制一份),再把前端没填的字段用旧版本补齐。
	merged := payload
	merged.Code = definition.Code
	if strings.TrimSpace(merged.DisplayName) == "" {
		merged.DisplayName = definition.DisplayName
	}
	// 前端没传图时,用 loadJSONObject 把旧版本的 GraphJSON(JSON 字符串)反序列化回 map 沿用。
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
	// 删除前两道保护:正在激活的版本不能删;有执行历史的版本也不能删。
	// *state.ActiveWorkflowDefinitionID 里的 * 是“解引用”:取出指针指向的实际 int64 值来比较。
	if state != nil && state.ActiveWorkflowDefinitionID != nil && *state.ActiveWorkflowDefinitionID == definitionID {
		return bizErr("Active workflow definition cannot be deleted")
	}
	var executionCount int64
	a.DB.Model(&db.WorkflowExecution{}).Where("workflow_definition_id = ?", definitionID).Count(&executionCount)
	if executionCount > 0 {
		return bizErr("Workflow definition with execution history cannot be deleted")
	}
	// 通过检查后真正删除:等价 DELETE FROM workflow_definition WHERE id = ?(按主键)。
	return a.DB.Delete(definition).Error
}

// createVersionRow 组装并插入一条工作流定义记录(某个 code 的某个版本)。
// 关于“事务”:这里只有单条 INSERT,一步到位、无需事务;像 seed.go 的 Seed 那样“多步要么全成”
// 才用 db.Transaction 包起来。见 GO入门笔记『框架:GORM』。
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
	// 用“键值写法”的 struct 字面量构造要落库的行。GraphJSON 用 dumpJSON(graph) 把图(map)
	// 序列化成 JSON 字符串存进文本列;CreatedBy 是 *int64 指针字段,用 &operatorUserID 取地址填入。
	definition := db.WorkflowDefinition{
		Code: code, Version: version, DisplayName: displayName,
		Description: strings.TrimSpace(payload.Description),
		GraphJSON:   dumpJSON(graph), IsBuiltin: isBuiltin,
		CreatedBy: &operatorUserID, CreatedAt: timeNow(),
	}
	// Create(&definition):INSERT 一行;GORM 会把自增主键回填到 definition.ID(&definition 传指针正为此)。
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

// requireDefinition 按主键查一条定义,查不到就返回业务错误;返回 *db.WorkflowDefinition(指针)。
func (a *App) requireDefinition(definitionID int64) (*db.WorkflowDefinition, error) {
	var definition db.WorkflowDefinition
	// First(&x, id):按主键查一条,等价 SELECT ... WHERE id = ? LIMIT 1;查不到时 err 非 nil。
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

// runtimeStateMap 一次性把所有运行时状态查出来,做成 code → *state 的 map 便于查找。
func (a *App) runtimeStateMap() map[string]*db.WorkflowRuntimeState {
	var states []db.WorkflowRuntimeState
	// Find 不带条件 = 查全表所有行,回填进 states。
	a.DB.Find(&states)
	result := map[string]*db.WorkflowRuntimeState{}
	// range 只写一个变量时拿到的是下标 i。这里存 &states[i](元素地址)而不是循环变量的地址,
	// 才能让 map 里每个指针各自指向不同的元素。
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
	// 定义一个匿名结构体的 slice 来接住聚合查询的每一行结果(列名按字段名自动对应)。
	var rows []struct {
		WorkflowDefinitionID int64
		Count                int64
	}
	// 分组统计每个定义的执行次数,等价 SQL:
	//   SELECT workflow_definition_id, COUNT(id) AS count FROM workflow_execution
	//   WHERE workflow_definition_id IN (?) GROUP BY workflow_definition_id
	// IN ? 会把整个 slice 展开成 IN 列表;Scan 把结果扫描进 rows;链式调用可跨多行书写。
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

// serializeDefinition 把一条定义(+运行时状态)整理成前端要的 map。其中 graph 字段用
// loadJSONObject 把数据库里的 GraphJSON 字符串反序列化成 map 再返回。
func (a *App) serializeDefinition(definition *db.WorkflowDefinition, state *db.WorkflowRuntimeState, executionCount int64, activeVersion any) M {
	var activeDefinitionID any
	isActive := false
	isWorkflowActive := false
	// 先判空(state 和它的指针字段都非 nil)再用 * 解引用取值,避免对 nil 指针取值导致崩溃。
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

// generateWorkflowCode 由显示名生成一个数据库内唯一的 code(重名就加 -2、-3… 后缀)。
func (a *App) generateWorkflowCode(displayName string) string {
	base := buildWorkflowCodeBase(displayName)
	candidate := base
	index := 2
	// for 不带任何条件 = 无限循环,靠内部 return 跳出:一旦 candidate 在库里查不到重复就返回它。
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

// buildWorkflowCodeBase 把显示名转成 code 基串:字母数字转小写保留,其它字符压成单个连字符 -。
func buildWorkflowCodeBase(displayName string) string {
	// strings.Builder 是高效拼接字符串的缓冲区(比反复用 + 拼接省内存)。
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

// truncateBytes 把字符串按“字节长度”上限截断,同时不切碎多字节字符(如中文)。
func truncateBytes(value string, max int) string {
	if len(value) <= max {
		return value
	}
	// []rune(value) 把字符串按“字符(码点)”拆开;这样截断时不会把一个中文字符切成半个。
	runes := []rune(value)
	for len(string(runes)) > max && len(runes) > 0 {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}

func itoa(value int) string { return strconv.Itoa(value) }

// nilOrValue 把 *int64 指针转成可直接放进 JSON 的值:空指针→nil,否则用 *value 解引用取出整数。
func nilOrValue(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
