// 本文件是“工作流定义”的服务层:负责工作流定义的增删改查(CRUD)、把图结构在 JSON 字符串与
// map 之间序列化存取,以及最核心的“图校验”——在落库/执行前检查这张流程图是否合法(有起点终点、
// 无自环、无环、全连通、分支完整等)。见 GO入门笔记『方法与接收者』『框架:GORM』。

package service

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	"coinsphere/backend/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// startNodeTypes 已删除:哪些类型算"起始节点"由节点注册表(nodes.go 的 Kind)说了算,
// 不再在这里另维护一份名单,否则加一种开始节点就要改两处。

// ---------- 图校验 ----------

// validateWorkflowGraph 落库与执行前的图结构校验。
//
// 校验分两层:
//   - 通用图规则:节点 id 唯一、边两端存在、无自环、无环、全连通、非起始节点必须可达;
//   - 按节点"图语义"(Kind,见 nodes.go)分派的规则:起始节点要 entryKey、终止节点不能有出边、
//     分支节点必须覆盖声明的全部分支、循环节点的循环体必须封闭。
//
// 关键点是第二层查的是注册表,不是写死的类型名。新增一种节点类型只要在 registerNode 里声明
// Kind / Branches,这里自动适配,不用改本文件。
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
	nodeMap := map[string]M{}
	nodeIDs := make([]string, 0, len(nodesAny))
	// nodeDefs 缓存每个节点查到的注册表登记项,后面按 Kind 分派规则时直接用。
	nodeDefs := map[string]*workflowNodeDefinition{}
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
		// 类型必须是注册表里登记过的,否则这张图存下来也跑不了 —— 与其跑到一半才炸,不如现在拦住。
		definition, err := getNodeDefinition(asString(node["type"]))
		if err != nil {
			return err
		}
		// 配置里 schema 声明为必填的项也在这里查,同样是"提前发现"而不是运行期才发现。
		if err := assertNodeConfig(nodeID, node, definition); err != nil {
			return err
		}
		nodeMap[nodeID] = node
		nodeDefs[nodeID] = definition
		nodeIDs = append(nodeIDs, nodeID)
	}

	startNodes := []string{}
	terminalCount := 0
	for _, nodeID := range nodeIDs {
		switch nodeDefs[nodeID].kind() {
		case nodeKindStart:
			startNodes = append(startNodes, nodeID)
		case nodeKindTerminal:
			terminalCount++
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
	if terminalCount == 0 {
		return bizErr("Workflow must contain at least one end node")
	}

	// 下面把连线整理成便于分析的结构:adjacency 是“邻接表”(节点 id → 从它出发的边列表);
	// incoming/outgoing 分别统计每个节点的入度、出度(有多少条边指入/指出)。
	adjacency := map[string][]M{}
	incoming := map[string]int{}
	outgoing := map[string]int{}
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

	// 真 DAG 支持“汇聚(join)”:任意节点都可有多条入边(引擎会等所有活跃分支到齐再跑它一次)。
	// 这里只需保证非起始节点至少有一条入边(不是孤岛);多入边不再受限。
	for _, nodeID := range nodeIDs {
		if !startSet[nodeID] && incoming[nodeID] == 0 {
			return bizErr("Every non-start node must be reachable")
		}
	}

	// 按节点的图语义分类分派出边规则。
	for _, nodeID := range nodeIDs {
		definition := nodeDefs[nodeID]
		edgesFromNode := adjacency[nodeID]
		switch definition.kind() {
		case nodeKindTerminal:
			if len(edgesFromNode) > 0 {
				return bizErr("End node cannot have outgoing edges")
			}
		case nodeKindBranch:
			if err := assertBranchEdges(definition, rawNodeConfig(nodeMap[nodeID]), edgesFromNode); err != nil {
				return err
			}
		case nodeKindLoop:
			if err := assertLoopEdges(nodeID, nodeDefs, adjacency); err != nil {
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

// assertNodeConfig 校验节点配置里 schema 声明为 required 的项都填了。
// 只查"有没有填",值本身的类型/范围校验留给节点自己运行时判断(schema 在这里只当必填清单用)。
func assertNodeConfig(nodeID string, node M, definition *workflowNodeDefinition) error {
	required := schemaRequiredKeys(definition.ConfigSchema)
	if len(required) == 0 {
		return nil
	}
	config := rawNodeConfig(node)
	for _, key := range required {
		value, exists := config[key]
		if !exists || value == nil {
			return bizErr("Node config is missing required field: %s.%s (node %s)", definition.TypeCode, key, nodeID)
		}
		// 字符串类必填项还要求非空白,否则"填了个空格"也能蒙混过关。
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			return bizErr("Node config is missing required field: %s.%s (node %s)", definition.TypeCode, key, nodeID)
		}
	}
	return nil
}

// schemaRequiredKeys 从 JSON Schema 里取 required 清单。schema 可能是代码里直接写的 []string,
// 也可能是从 JSON 反序列化来的 []any,两种都认。
func schemaRequiredKeys(schema M) []string {
	switch typed := schema["required"].(type) {
	case []string:
		return typed
	case []any:
		keys := make([]string, 0, len(typed))
		for _, item := range typed {
			if key := asString(item); key != "" {
				keys = append(keys, key)
			}
		}
		return keys
	default:
		return nil
	}
}

// assertBranchEdges 校验分支节点的出边:必须恰好覆盖它声明的那些分支,不缺也不多。
//
// 分支清单可能是静态的(condition.branch 固定 true/false),也可能来自节点自己的配置
// (condition.switch 的每个 case 一条分支),都由 resolveBranches 统一算出来。
func assertBranchEdges(definition *workflowNodeDefinition, config M, edgesFromNode []M) error {
	declaredList := definition.resolveBranches(config)
	if len(declaredList) < 2 {
		return bizErr("Node %s must declare at least two branches in its config", definition.TypeCode)
	}
	declared := map[string]bool{}
	for _, branch := range declaredList {
		declared[branch] = true
	}
	seen := map[string]bool{}
	for _, edge := range edgesFromNode {
		branch := strings.TrimSpace(edgeBranchKey(edge))
		if !declared[branch] {
			return bizErr("Node %s has an edge with unknown branch: %s", definition.TypeCode, branch)
		}
		if seen[branch] {
			return bizErr("Node %s has duplicated branch edges: %s", definition.TypeCode, branch)
		}
		seen[branch] = true
	}
	if len(seen) != len(declared) {
		return bizErr("Node %s must contain all branches: %s", definition.TypeCode, strings.Join(declaredList, ", "))
	}
	return nil
}

// assertLoopEdges 校验循环节点的出边与循环体。
//
// 出边分两类:branch=="next" 的是"循环跑完再继续"的后继(可选、最多一条),
// 其余必须恰好一条,作为循环体入口。
//
// 循环体必须是"封闭"的:外面进不来、里面出不去。这不是洁癖 —— 引擎把循环体当独立子图按元素反复跑,
// 若体外的边能连进体内,汇聚(join)语义和循环语义就会打架;若体内的边能连到体外,
// 那个体外节点会被漏掉(它的入边计数永远等不到结论),整条主流程会安静地少跑一段。
func assertLoopEdges(loopNodeID string, nodeDefs map[string]*workflowNodeDefinition, adjacency map[string][]M) error {
	var bodyEdge M
	bodyEdgeCount, nextEdgeCount := 0, 0
	for _, edge := range adjacency[loopNodeID] {
		if strings.TrimSpace(edgeBranchKey(edge)) == loopNextBranch {
			nextEdgeCount++
			continue
		}
		bodyEdgeCount++
		if bodyEdge == nil {
			bodyEdge = edge
		}
	}
	if bodyEdgeCount != 1 {
		return bizErr("Foreach node requires exactly one loop body successor")
	}
	if nextEdgeCount > 1 {
		return bizErr(`Foreach node supports at most one "next" successor`)
	}

	// 循环体 = 从体入口可达 − 从 next 后继可达。相减是为了让"循环体与循环后继汇聚到同一收尾节点"
	// 的写法成立:那个汇聚点归主流程,循环体跑到它就停。
	bodySet := dfsReachable(adjacency, []string{asString(bodyEdge["target"])})
	if nextEdgeCount > 0 {
		nextTargets := []string{}
		for _, edge := range adjacency[loopNodeID] {
			if strings.TrimSpace(edgeBranchKey(edge)) == loopNextBranch {
				nextTargets = append(nextTargets, asString(edge["target"]))
			}
		}
		for nodeID := range dfsReachable(adjacency, nextTargets) {
			delete(bodySet, nodeID)
		}
	}
	if len(bodySet) == 0 {
		return bizErr("Foreach loop body cannot be empty")
	}

	for nodeID := range bodySet {
		if nodeDefs[nodeID] != nil && nodeDefs[nodeID].kind() == nodeKindLoop {
			return bizErr("Nested foreach is not supported")
		}
	}
	// 双向封闭性:体外 → 体内、体内 → 体外都不允许(循环节点自己的那条体入口边除外)。
	for source, edgeList := range adjacency {
		sourceInBody := bodySet[source]
		for _, edge := range edgeList {
			targetInBody := bodySet[asString(edge["target"])]
			if !sourceInBody && targetInBody && source != loopNodeID {
				return bizErr("Foreach body cannot be entered from outside the loop")
			}
			if sourceInBody && !targetInBody {
				return bizErr(`Foreach body cannot exit the loop; use a "next" edge on the foreach node instead`)
			}
		}
	}
	return nil
}

// assertStartEntries 校验每个起始节点都有唯一、合法的 entryKey(触发入口标识)。
func assertStartEntries(nodeMap map[string]M, startNodes []string) error {
	entryKeys := map[string]bool{}
	for _, nodeID := range startNodes {
		config := rawNodeConfig(nodeMap[nodeID])
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

// assertNoEventPublishFeedback 防止“自触发死循环”:start.event 监听某事件时,其下游不能再
// 发布(event.publish)同一种事件类型,否则会无限自我触发。
func assertNoEventPublishFeedback(nodeMap map[string]M, adjacency map[string][]M, startNodes []string) error {
	for _, startNodeID := range startNodes {
		startNode := nodeMap[startNodeID]
		if asString(startNode["type"]) != "start.event" {
			continue
		}
		config := rawNodeConfig(startNode)
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
				if strings.TrimSpace(asString(rawNodeConfig(node)["eventType"])) == eventType {
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

var errWorkflowCodeTaken = errors.New("workflow code is already in use")

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
	displayName := strings.TrimSpace(payload.DisplayName)
	if displayName == "" {
		return nil, bizErr("Workflow display name is required")
	}
	if validation := a.ValidateWorkflowDefinition(payload); validation["valid"] != true {
		issues := validation["issues"].([]M)
		return nil, bizErr("%s", asString(issues[0]["message"]))
	}
	baseCode := buildWorkflowCodeBase(displayName)
	var definitionID int64
	// (code, version) 唯一约束仲裁同名并发创建；插入后再在同一事务确认 family 只有
	// 当前 v1，兼容历史上删除 v1 但仍保留高版本的工作流，不把新工作流误并入旧 family。
	for index := 1; ; index++ {
		code := workflowCodeCandidate(baseCode, index)
		err := a.DB.Transaction(func(tx *gorm.DB) error {
			definition := buildWorkflowDefinition(code, 1, payload, false, operatorUserID)
			created := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "code"}, {Name: "version"}},
				DoNothing: true,
			}).Create(&definition)
			if created.Error != nil {
				return created.Error
			}
			if created.RowsAffected == 0 {
				return errWorkflowCodeTaken
			}
			var familySize int64
			if err := tx.Model(&db.WorkflowDefinition{}).Where("code = ?", code).Count(&familySize).Error; err != nil {
				return err
			}
			if familySize != 1 {
				return errWorkflowCodeTaken
			}
			definitionID = definition.ID
			return nil
		})
		if err == nil {
			break
		}
		if !errors.Is(err, errWorkflowCodeTaken) {
			return nil, err
		}
	}
	return a.GetWorkflowDefinition(definitionID)
}

// UpdateWorkflowDefinition 编辑生成新版本。
func (a *App) UpdateWorkflowDefinition(definitionID int64, payload WorkflowDefinitionUpsertPayload, operatorUserID int64) (M, error) {
	definition, err := a.requireDefinition(definitionID)
	if err != nil {
		return nil, err
	}

	// merged := payload 是值拷贝(struct 直接赋值即复制一份),再把前端没填的字段用旧版本补齐。
	merged := payload
	merged.Code = definition.Code
	if strings.TrimSpace(merged.DisplayName) == "" {
		merged.DisplayName = definition.DisplayName
	}
	// 前端没传图时,用 loadJSONObject 把旧版本的 GraphJSON(JSON 字符串)反序列化回 map 沿用。
	if len(merged.Graph) == 0 {
		merged.Graph = loadJSONObject(definition.GraphJSON)
	}
	if validation := a.ValidateWorkflowDefinition(merged); validation["valid"] != true {
		issues := validation["issues"].([]M)
		return nil, bizErr("%s", asString(issues[0]["message"]))
	}

	var createdID int64
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		locked, err := lockWorkflowDefinitionFamily(tx, definitionID)
		if err != nil {
			return err
		}
		var maxVersion int
		if err := tx.Model(&db.WorkflowDefinition{}).
			Where("code = ?", locked.Code).
			Select("COALESCE(MAX(version), 0)").
			Scan(&maxVersion).Error; err != nil {
			return err
		}
		created := buildWorkflowDefinition(locked.Code, maxVersion+1, merged, locked.IsBuiltin, operatorUserID)
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		createdID = created.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return a.GetWorkflowDefinition(createdID)
}

// DeleteWorkflowDefinition 删除未激活且无执行历史的版本。
func (a *App) DeleteWorkflowDefinition(definitionID int64) error {
	return a.DB.Transaction(func(tx *gorm.DB) error {
		definition, err := lockWorkflowDefinitionFamily(tx, definitionID)
		if err != nil {
			return err
		}
		var state db.WorkflowRuntimeState
		err = tx.Where("workflow_code = ?", definition.Code).First(&state).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && state.ActiveWorkflowDefinitionID != nil && *state.ActiveWorkflowDefinitionID == definitionID {
			return bizErr("Active workflow definition cannot be deleted")
		}
		var executionCount int64
		if err := tx.Model(&db.WorkflowExecution{}).Where("workflow_definition_id = ?", definitionID).Count(&executionCount).Error; err != nil {
			return err
		}
		if executionCount > 0 {
			return bizErr("Workflow definition with execution history cannot be deleted")
		}
		return tx.Delete(definition).Error
	})
}

func buildWorkflowDefinition(code string, version int, payload WorkflowDefinitionUpsertPayload, isBuiltin bool, operatorUserID int64) db.WorkflowDefinition {
	displayName := strings.TrimSpace(payload.DisplayName)
	graph := payload.Graph
	if graph == nil {
		graph = M{}
	}
	return db.WorkflowDefinition{
		Code: code, Version: version, DisplayName: displayName,
		Description: strings.TrimSpace(payload.Description),
		GraphJSON:   dumpJSON(graph), IsBuiltin: isBuiltin,
		CreatedBy: &operatorUserID, CreatedAt: time.Now(),
	}
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
	return requireDefinitionWithDB(a.DB, definitionID)
}

func requireDefinitionWithDB(database *gorm.DB, definitionID int64) (*db.WorkflowDefinition, error) {
	var definition db.WorkflowDefinition
	err := database.First(&definition, definitionID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, bizErr("Workflow definition does not exist")
	}
	if err != nil {
		return nil, err
	}
	return &definition, nil
}

// lockWorkflowDefinitionFamily 用一条无值变化的 UPDATE 获取同一 code 的数据库写锁。
// PostgreSQL 会锁住 family 的定义行，SQLite 会在事务首写时取得 WAL writer lock；
// 后续必须只使用同一个 tx，才能让版本分配、激活和删除在多进程下串行。
func lockWorkflowDefinitionFamily(tx *gorm.DB, definitionID int64) (*db.WorkflowDefinition, error) {
	result := tx.Model(&db.WorkflowDefinition{}).
		Where("code = (?)", tx.Model(&db.WorkflowDefinition{}).Select("code").Where("id = ?", definitionID)).
		UpdateColumn("version", gorm.Expr("version"))
	if result.Error != nil {
		return nil, result.Error
	}
	return requireDefinitionWithDB(tx, definitionID)
}

func (a *App) getRuntimeStateByCode(workflowCode string) *db.WorkflowRuntimeState {
	state, _ := findRuntimeStateByCodeWithDB(a.DB, workflowCode)
	return state
}

func findRuntimeStateByCodeWithDB(database *gorm.DB, workflowCode string) (*db.WorkflowRuntimeState, error) {
	var state db.WorkflowRuntimeState
	err := database.Where("workflow_code = ?", workflowCode).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
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

func workflowCodeCandidate(base string, index int) string {
	if index <= 1 {
		return base
	}
	suffix := "-" + itoa(index)
	available := workflowCodeMaxLength - len(suffix)
	truncated := strings.TrimRight(truncateBytes(base, available), "-._")
	if truncated == "" {
		truncated = "workflow"
	}
	return truncated + suffix
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
