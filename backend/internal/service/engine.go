// engine.go —— 工作流"图执行引擎"(并发 DAG 版)。
//
// 一个工作流被画成一张"有向无环图(DAG)":一堆节点(node)用边(edge)连起来。
// 本文件负责从起点出发把这张图"跑"一遍,每到一个节点就调用它对应的处理函数(登记在 nodes.go 里)。
//
// 执行模型是"就绪队列",不是简单的深度递归:
//   - 每个节点记一个"未决入边计数",入口节点入度为 0 先跑;
//   - 节点跑完点亮出边(分支节点只点中选分支,其余出边算"跳过");一条边有结论就给目标减一次计数;
//   - 计数归零时:有任一入边被点亮就运行该节点,否则(全被跳过)该节点也跳过、并把跳过继续向下传播;
//   - 多入边的"汇聚(join)"节点因此天然只跑一次、且等所有活跃分支到齐;
//   - 相互独立的就绪节点用 errgroup 真并发执行,任一节点出错就取消整组、抛出首个错误。
//
// 循环节点(foreach)的循环体是一段"封闭"的自包含子图:外部进不来、内部出不去(校验器保证),
// 按元素串行跑一遍,子图内部仍可并发;循环跑完再点亮 branch="next" 的出边继续主流程。
//
// 跑图时会做三件"记账":节点执行日志、边流转日志、终态后发布标准领域事件。
// 本文件只管"把图跑完、并把错误往上抛";失败是否重试、execution 终态状态机如何推进,由调用方负责。

package service

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"coinsphere/backend/internal/db"
)

// runResult 一次跑图结束后的上下文。
//
// 字段含义:Execution 本次执行、Definition 工作流定义、RuntimeEntry 运行时入口(均为指针);
// TriggerCtx 触发上下文;SharedState 跑图全程共享的"变量表",节点之间靠它传数据。
// 注意:跑图期间对 SharedState 的并发读写都经 runState(见 nodes.go)加锁;跑完后(已无并发)可直接读它。
type runResult struct {
	Execution    *db.WorkflowExecution
	Definition   *db.WorkflowDefinition
	RuntimeEntry *db.WorkflowRuntimeEntry
	TriggerCtx   M
	SharedState  M
	StartedAt    time.Time
	FinishedAt   time.Time
}

// runExecutionGraph 只负责跑图,不推进 execution 终态状态机。
func (a *App) runExecutionGraph(ctx context.Context, executionID int64) (*runResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	execution, err := a.getExecutionByID(executionID)
	if err != nil {
		return nil, err
	}
	definition := execution.WorkflowDefinition
	if definition == nil {
		return nil, bizErr("Workflow definition does not exist")
	}
	graph := loadJSONObject(definition.GraphJSON)
	startedAt := firstTime(execution.StartedAt, execution.ClaimedAt, &execution.QueuedAt)
	triggerCtx := extractTriggerContext(execution.ContextSnapshotJSON)
	triggerType := execution.TriggerType
	if triggerType == "" {
		triggerType = asString(triggerCtx["triggerType"])
	}
	inputs := loadJSONObject(execution.InputSnapshotJSON)

	runtimeEntry := a.getRuntimeEntryByDefinitionAndKey(definition.ID, execution.StartEntryKey)
	// sharedState 是跑图全程的"共享变量表":inputs 是初始输入,trigger 是触发信息,
	// taskResult/nodeOutputs 会在节点执行时被填进去,供下游节点引用。
	sharedState := M{
		"inputs": inputs,
		"trigger": M{
			"type":            triggerType,
			"payload":         orEmptyMap(triggerCtx["payload"]),
			"triggerKey":      derefString(execution.TriggerKey),
			"triggerOutboxId": nilOrValue(execution.TriggerOutboxID),
		},
		"definition": M{
			"id": definition.ID, "code": definition.Code,
			"version": definition.Version, "displayName": definition.DisplayName,
		},
		"runtimeEntry": serializeRuntimeEntryLite(runtimeEntry),
		"taskResult":   M{},
		"nodeOutputs":  M{},
		// 父工作流传下来的调用链(workflow.call 节点用它检测循环调用);顶层执行时是空数组。
		workflowCallChainKey: loadWorkflowCallChain(triggerCtx),
	}

	result := &runResult{
		Execution: execution, Definition: definition, RuntimeEntry: runtimeEntry,
		TriggerCtx: triggerCtx, SharedState: sharedState, StartedAt: startedAt,
	}
	// state 把 sharedState 包成并发安全的读写表:真并发下多个节点 goroutine 会同时读写它。
	// result.SharedState 仍指向同一张底层 map,跑完图后(已无并发)调用方直接读它即可。
	state := newRunState(sharedState)

	// 跑图前先做两道校验:图本身是否合法、能否找到起始节点。任一失败都记好结束时间再把错误抛出去。
	if err := validateWorkflowGraph(graph); err != nil {
		result.FinishedAt = time.Now()
		return result, err
	}
	startNode := findStartNode(graph, execution.StartNodeID, execution.StartEntryKey)
	if startNode == nil {
		result.FinishedAt = time.Now()
		return result, bizErr("Start entry does not exist in workflow definition")
	}

	log.Printf(
		"[engine] execution started: execution_id=%d workflow_code=%s entry=%s trigger=%s",
		execution.ID, definition.Code, execution.StartEntryKey, triggerType,
	)
	// 核心:从起始节点开始真正"跑图"。失败就记结束时间、打日志、把错误抛给调用方。
	if err := a.runGraph(ctx, result, state, graph, asString(startNode["id"])); err != nil {
		result.FinishedAt = time.Now()
		log.Printf("[engine] execution failed: execution_id=%d workflow_code=%s err=%v", execution.ID, definition.Code, err)
		return result, err
	}
	result.FinishedAt = time.Now()
	log.Printf("[engine] execution finished: execution_id=%d workflow_code=%s status=success", execution.ID, definition.Code)
	return result, nil
}

// ---------- 图索引 ----------

// graphEdge 一条连线的结构化形式。
//
// 图存在库里是 JSON,取出来每条边都是一张 map[string]any,想拿 target 得写 asString(edge["target"])。
// 建索引时统一转成这个结构体,后面全是字段访问,引擎里就不再散落类型断言了。
type graphEdge struct {
	ID     string
	Source string
	Target string
	Branch string
}

// graphIndex 一张图的只读索引:节点按 id 查,边按源/目标两个方向各建一张表。
type graphIndex struct {
	nodes    map[string]M
	outEdges map[string][]*graphEdge // 源节点 id → 出边
	inEdges  map[string][]*graphEdge // 目标节点 id → 入边
}

// buildGraphIndex 把 JSON 形态的图整理成索引。出边按 edge id 排序,让调度顺序稳定可复现
// (并发下最终完成顺序仍可能交错,但"谁先被调度"是确定的)。
func buildGraphIndex(graph M) *graphIndex {
	index := &graphIndex{
		nodes:    map[string]M{},
		outEdges: map[string][]*graphEdge{},
		inEdges:  map[string][]*graphEdge{},
	}
	nodesAny, _ := graph["nodes"].([]any)
	for _, nodeAny := range nodesAny {
		if node, ok := nodeAny.(map[string]any); ok {
			index.nodes[asString(node["id"])] = node
		}
	}
	edgesAny, _ := graph["edges"].([]any)
	for _, edgeAny := range edgesAny {
		raw, ok := edgeAny.(map[string]any)
		if !ok {
			continue
		}
		edge := &graphEdge{
			ID:     asString(raw["id"]),
			Source: asString(raw["source"]),
			Target: asString(raw["target"]),
			Branch: edgeBranchKey(raw),
		}
		index.outEdges[edge.Source] = append(index.outEdges[edge.Source], edge)
		index.inEdges[edge.Target] = append(index.inEdges[edge.Target], edge)
	}
	for _, edgeList := range index.outEdges {
		sort.Slice(edgeList, func(i, j int) bool { return edgeList[i].ID < edgeList[j].ID })
	}
	return index
}

// nodeType 查某节点的类型编码;节点不存在时返回空串。
func (g *graphIndex) nodeType(nodeID string) string { return asString(g.nodes[nodeID]["type"]) }

// reachable 从给定起点集合出发,返回所有可达节点的集合(用 map 当集合)。
func (g *graphIndex) reachable(startIDs ...string) map[string]bool {
	visited := map[string]bool{}
	stack := append([]string{}, startIDs...)
	for len(stack) > 0 {
		nodeID := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if nodeID == "" || visited[nodeID] {
			continue
		}
		visited[nodeID] = true
		for _, edge := range g.outEdges[nodeID] {
			stack = append(stack, edge.Target)
		}
	}
	return visited
}

// splitLoopEdges 把循环节点的出边分成"循环体入口"与"循环后继"两类。
// branch=="next" 的是循环跑完之后才走的后继边;其余(通常只有一条、且没有 branch)是循环体入口。
func (g *graphIndex) splitLoopEdges(loopNodeID string) (body *graphEdge, next []*graphEdge) {
	for _, edge := range g.outEdges[loopNodeID] {
		if edge.Branch == loopNextBranch {
			next = append(next, edge)
			continue
		}
		if body == nil {
			body = edge
		}
	}
	return body, next
}

// loopBodySet 算出一个循环节点的"循环体"节点集合:从体入口可达,再减去从循环后继可达的部分。
// 相减是为了应对"循环体与循环后继最终汇聚到同一个收尾节点"——那个汇聚点归主流程,
// 由循环后继边推进,循环体内跑到它就自然停住,不会被每个元素重复触发。
func (g *graphIndex) loopBodySet(loopNodeID string) map[string]bool {
	body, next := g.splitLoopEdges(loopNodeID)
	if body == nil {
		return map[string]bool{}
	}
	bodySet := g.reachable(body.Target)
	if len(next) > 0 {
		nextTargets := make([]string, 0, len(next))
		for _, edge := range next {
			nextTargets = append(nextTargets, edge.Target)
		}
		for nodeID := range g.reachable(nextTargets...) {
			delete(bodySet, nodeID)
		}
	}
	return bodySet
}

// ---------- 并发 DAG 执行器 ----------

// resolution 一条入边的"结论":目标节点 + 这条边是被点亮(fired)还是被跳过。
type resolution struct {
	node  string
	fired bool
}

// graphRun 打包一次跑图全程共享的东西(索引、循环体划分等),供主流程与各循环体复用。
// 索引类字段建好后只读;traversalIndex 是跨分支共享的流转序号,用 transitionMu 保护自增。
type graphRun struct {
	app    *App
	result *runResult
	state  *runState
	graph  M
	*graphIndex
	loopBody map[string]map[string]bool // 循环节点 id → 其循环体节点集合

	// slots 是"同时真正在执行的节点数"上限,用带缓冲 channel 当令牌桶:
	// 拿到令牌才执行节点,执行完立刻还回去。
	//
	// 为什么不用 errgroup.SetLimit:节点是在别的节点的 goroutine 里被调度出来的
	// (advance → scheduleRun → group.Go)。SetLimit 会让 Go() 在槽位满时阻塞,
	// 而调用它的那个父节点自己正占着一个槽 —— 大家互相等着对方先还槽,直接死锁。
	// 令牌只在"执行节点"这段持有、跑循环体前就归还,所以不会出现"持token等token"。
	slots chan struct{}

	transitionMu   sync.Mutex
	traversalIndex int
}

// acquireSlot 取一个执行令牌;整图被取消时立刻放弃。
func (r *graphRun) acquireSlot(ctx context.Context) error {
	select {
	case r.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *graphRun) releaseSlot() { <-r.slots }

// runGraph 建索引、划分循环体,再从起点跑整张 DAG。
func (a *App) runGraph(ctx context.Context, result *runResult, state *runState, graph M, startNodeID string) error {
	index := buildGraphIndex(graph)

	// 只有"从本次执行起点可达"的节点才会被跑(多入口图里只跑选中入口那一支)。
	reachable := index.reachable(startNodeID)
	// 把每个循环节点的循环体子图切出去:它们由各自的循环节点驱动,不进主流程调度。
	loopBody := map[string]map[string]bool{}
	bodyNodes := map[string]bool{}
	for nodeID := range reachable {
		if nodeKindOf(index.nodeType(nodeID)) != nodeKindLoop {
			continue
		}
		body := index.loopBodySet(nodeID)
		loopBody[nodeID] = body
		for id := range body {
			bodyNodes[id] = true
		}
	}
	// 主流程集合 = 可达集合 − 所有循环体节点。
	mainSet := map[string]bool{}
	for nodeID := range reachable {
		if !bodyNodes[nodeID] {
			mainSet[nodeID] = true
		}
	}

	slotCount := a.Cfg.Workflow.GraphNodeConcurrency
	if slotCount < 1 {
		slotCount = 8
	}
	run := &graphRun{
		app: a, result: result, state: state, graph: graph,
		graphIndex: index, loopBody: loopBody,
		slots: make(chan struct{}, slotCount),
	}
	return run.runDAG(ctx, startNodeID, mainSet)
}

// runDAG 在给定节点集合内跑一段 DAG:startNodeID 是入口(集合内入度为 0)。
// 就绪的独立节点用 errgroup 并发执行;返回首个错误(或 nil)。循环体也复用它(串行调用、各跑一遍)。
func (r *graphRun) runDAG(parentCtx context.Context, startNodeID string, nodeSet map[string]bool) error {
	if !nodeSet[startNodeID] {
		return nil
	}
	// pending[n] = n 的入边里 source 也在本集合内的条数(跨集合入边不等待)。
	pending := map[string]int{}
	for nodeID := range nodeSet {
		count := 0
		for _, edge := range r.inEdges[nodeID] {
			if nodeSet[edge.Source] {
				count++
			}
		}
		pending[nodeID] = count
	}

	// mu 保护下面这几张调度状态表;errgroup 提供并发与"出错即取消"的 ctx。
	var mu sync.Mutex
	scheduled := map[string]bool{}
	firedIn := map[string]bool{}
	skipped := map[string]bool{}
	group, ctx := errgroup.WithContext(parentCtx)
	// 注意:这里刻意不调 group.SetLimit —— 并发上限由 graphRun.slots 令牌桶控制,原因见那里的注释。

	var scheduleRun func(nodeID string)

	// advance:锁内处理若干"入边结论",把"跳过"沿图向下传播(纯计数,不跑节点);
	// 算出哪些节点该运行后,脱锁再逐个 scheduleRun(避免持锁 spawn 造成死锁)。
	advance := func(seed []resolution) {
		mu.Lock()
		toRun := []string{}
		queue := append([]resolution{}, seed...)
		for len(queue) > 0 {
			item := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			target := item.node
			if !nodeSet[target] {
				continue
			}
			if item.fired {
				firedIn[target] = true
			}
			pending[target]--
			if pending[target] > 0 {
				continue
			}
			// 该节点所有集合内入边都结清了:有点亮的就跑,全跳过就连它一起跳并继续向下传播。
			if firedIn[target] {
				if !scheduled[target] {
					scheduled[target] = true
					toRun = append(toRun, target)
				}
			} else if !skipped[target] {
				skipped[target] = true
				for _, edge := range r.outEdges[target] {
					queue = append(queue, resolution{node: edge.Target, fired: false})
				}
			}
		}
		mu.Unlock()
		for _, nodeID := range toRun {
			scheduleRun(nodeID)
		}
	}

	scheduleRun = func(nodeID string) {
		group.Go(func() error {
			return r.processNode(ctx, nodeID, advance)
		})
	}

	// 点火:入口节点直接运行(集合内入度为 0)。
	mu.Lock()
	scheduled[startNodeID] = true
	mu.Unlock()
	scheduleRun(startNodeID)
	return group.Wait()
}

// processNode 运行一个节点:落"运行中"日志 → 查处理器执行 → 落成功/失败日志 → 点亮出边交给 advance。
func (r *graphRun) processNode(ctx context.Context, nodeID string, advance func([]resolution)) error {
	// 整组已被取消(别的分支失败/超时)就别再跑。
	if err := ctx.Err(); err != nil {
		return err
	}
	node := r.nodes[nodeID]
	if node == nil {
		return bizErr("Workflow node does not exist: %s", nodeID)
	}
	nodeLog, err := r.openNodeLog(nodeID, asString(node["type"]))
	if err != nil {
		return err
	}

	nodeResult, execErr := r.runNodeBody(ctx, node, nodeLog)
	if execErr != nil {
		r.closeNodeLog(ctx, nodeLog, nil, execErr)
		return execErr
	}
	r.closeNodeLog(ctx, nodeLog, nodeResult, nil)

	// 循环节点:对数组逐个跑一遍循环体子图(元素间串行,子图内部仍可并发),跑完再推进循环后继。
	// 注意此时执行令牌已经归还 —— 循环体里的节点要自己去取令牌,父节点不能占着不放。
	if nodeResult.ForeachItems != nil {
		return r.runForeach(ctx, nodeID, nodeResult, advance)
	}

	// 普通/分支节点:逐条出边判定"点亮/跳过"(分支节点只点中选分支),点亮的记流转日志,再交给 advance 推进下游。
	advance(r.fireEdges(nodeID, r.outEdges[nodeID], nodeResult))
	return nil
}

// runNodeBody 在"执行令牌"的保护下跑一个节点的处理器:令牌限制同时干活的节点数,
// 出了这个函数就立刻归还,所以后面跑循环体、推进下游都不占用配额。
func (r *graphRun) runNodeBody(ctx context.Context, node M, nodeLog *db.WorkflowExecutionNode) (*nodeExecResult, error) {
	if err := r.acquireSlot(ctx); err != nil {
		return nil, err
	}
	defer r.releaseSlot()
	return r.executeNode(ctx, node, nodeLog)
}

// openNodeLog 进入节点时写一条"运行中"日志,并按配置决定要不要留一份当下共享状态的输入快照。
// 输入快照是全量状态序列化,大图 / 多元素循环时成本可观,可用 disable_node_input_snapshot 关掉。
func (r *graphRun) openNodeLog(nodeID, nodeType string) (*db.WorkflowExecutionNode, error) {
	nodeLog := &db.WorkflowExecutionNode{
		WorkflowExecutionID: r.result.Execution.ID, NodeID: nodeID, NodeType: nodeType,
		Status: "running", StartedAt: time.Now(),
	}
	if !r.app.Cfg.Workflow.DisableNodeInputSnapshot {
		nodeLog.InputSnapshotJSON = r.state.snapshotJSON(r.app.Cfg.Workflow.MaxOutputSnapshotBytes)
	}
	if err := r.app.DB.Create(nodeLog).Error; err != nil {
		return nil, err
	}
	return nodeLog, nil
}

// closeNodeLog 收尾一条节点日志:成功写输出快照,失败写错误信息。
func (r *graphRun) closeNodeLog(ctx context.Context, nodeLog *db.WorkflowExecutionNode, nodeResult *nodeExecResult, execErr error) {
	finishedAt := time.Now()
	updates := map[string]any{
		"finished_at": finishedAt,
		"duration_ms": finishedAt.Sub(nodeLog.StartedAt).Milliseconds(),
	}
	if execErr != nil {
		updates["status"] = "failed"
		updates["error_message"] = execErr.Error()
		log.Printf("[engine] node failed: execution_id=%d node_id=%s err=%v",
			r.result.Execution.ID, nodeLog.NodeID, execErr)
	} else {
		updates["status"] = "success"
		updates["output_snapshot_json"] = serializeSnapshot(nodeResult.Output, r.app.Cfg.Workflow.MaxOutputSnapshotBytes)
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cleanupCancel()
	if err := r.app.dbWithContext(cleanupCtx).Model(nodeLog).Updates(updates).Error; err != nil {
		log.Printf("[engine] close node log failed: execution_id=%d node_id=%s err=%v",
			r.result.Execution.ID, nodeLog.NodeID, err)
	}
}

// executeNode 按节点类型查处理器并执行。查不到处理器就直接把错误返回(交给失败分支处理)。
func (r *graphRun) executeNode(ctx context.Context, node M, nodeLog *db.WorkflowExecutionNode) (*nodeExecResult, error) {
	definition, err := getNodeDefinition(asString(node["type"]))
	if err != nil {
		return nil, err
	}
	return definition.Execute(&nodeExecContext{
		Ctx: ctx, App: r.app, Definition: r.result.Definition, RuntimeEntry: r.result.RuntimeEntry,
		Execution: r.result.Execution, NodeLog: nodeLog, Node: node, Graph: r.graph,
		State: r.state, TriggerCtx: r.result.TriggerCtx,
		PublishEvent: r.eventPublisher(nodeLog),
	})
}

// eventPublisher 造一个注入给节点的发事件回调:节点只管给 payload/metadata,
// 公共字段(执行 id、定义信息、触发类型)由这里补齐。
func (r *graphRun) eventPublisher(nodeLog *db.WorkflowExecutionNode) func(string, string, M, M) (int64, error) {
	execution := r.result.Execution
	return func(eventType, aggregateType string, payload, metadata M) (int64, error) {
		mergedPayload := r.app.buildEventBasePayload(r.result.Definition, r.result.RuntimeEntry, execution.ID, r.result.TriggerCtx)
		for key, value := range payload {
			mergedPayload[key] = value
		}
		mergedMetadata := M{
			"triggerType":  orString(asString(r.result.TriggerCtx["triggerType"]), execution.TriggerType),
			"workflowCode": r.result.Definition.Code,
		}
		for key, value := range metadata {
			mergedMetadata[key] = value
		}
		return r.app.publishDomainEvent(
			eventType, aggregateType, fmt.Sprint(execution.ID),
			mergedPayload, mergedMetadata, &execution.ID, &nodeLog.ID,
		)
	}
}

// fireEdges 逐条判定出边"点亮还是跳过":分支节点只点亮 branchKey 与所选分支相符的那条,
// 其余节点全部点亮。点亮的边顺带记一条流转日志。返回给 advance 的入边结论列表。
func (r *graphRun) fireEdges(nodeID string, edges []*graphEdge, nodeResult *nodeExecResult) []resolution {
	resolutions := make([]resolution, 0, len(edges))
	for _, edge := range edges {
		fired := nodeResult.SelectedBranch == nil || edge.Branch == *nodeResult.SelectedBranch
		if fired {
			payload := M{"output": nodeResult.Output}
			branchKey := edge.Branch
			if nodeResult.SelectedBranch != nil {
				payload["selectedBranch"] = *nodeResult.SelectedBranch
				branchKey = *nodeResult.SelectedBranch
			}
			r.logTransition(edge, payload, nil, branchKey)
		}
		resolutions = append(resolutions, resolution{node: edge.Target, fired: fired})
	}
	return resolutions
}

// runForeach 对循环节点的数组逐个元素跑一遍循环体子图(元素间串行);循环变量写进共享状态,跑完还原。
// 全部元素跑完后再点亮 branch="next" 的出边,让主流程继续往下走(没有这类边就到此为止)。
func (r *graphRun) runForeach(ctx context.Context, foreachID string, nodeResult *nodeExecResult, advance func([]resolution)) error {
	bodyEdge, nextEdges := r.splitLoopEdges(foreachID)
	if bodyEdge == nil {
		return nil
	}
	bodySet := r.loopBody[foreachID]
	itemKey := orString(nodeResult.ItemKey, "currentItem")
	indexKey := orString(nodeResult.IndexKey, "currentIndex")
	// 先记下循环变量的旧值,循环收尾时还原,避免污染外层状态。
	// ponytail: 循环变量存共享状态里;并行的多个 foreach 若用同名 itemKey/indexKey 会相互覆盖 ——
	//           需要并行跑多个 foreach 时给它们配不同变量名。彻底隔离需把循环变量改成每次执行的独立作用域。
	previousItem, hadItem := r.state.load(itemKey)
	previousIndex, hadIndex := r.state.load(indexKey)
	defer r.state.restore(itemKey, previousItem, hadItem)
	defer r.state.restore(indexKey, previousIndex, hadIndex)
	for index, item := range nodeResult.ForeachItems {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.state.set(itemKey, item)
		r.state.set(indexKey, index)
		iteration := index
		r.logTransition(bodyEdge, M{
			"output": nodeResult.Output, "loopItem": item, "loopIndex": index,
			"itemKey": itemKey, "indexKey": indexKey,
		}, &iteration, bodyEdge.Branch)
		if err := r.runDAG(ctx, bodyEdge.Target, bodySet); err != nil {
			return err
		}
	}
	// 循环体是封闭的,不会自己接回主流程;要在遍历之后继续,靠的是这些 branch="next" 的出边。
	if len(nextEdges) > 0 {
		advance(r.fireEdges(foreachID, nextEdges, &nodeExecResult{Output: nodeResult.Output}))
	}
	return nil
}

// logTransition 每点亮一条边就落一条流转日志。traversalIndex 在锁内自增,保证并发下序号单调递增。
func (r *graphRun) logTransition(edge *graphEdge, payload M, iterationIndex *int, branchKey string) {
	r.transitionMu.Lock()
	r.traversalIndex++
	index := r.traversalIndex
	r.transitionMu.Unlock()
	r.app.DB.Create(&db.WorkflowExecutionTransition{
		WorkflowExecutionID: r.result.Execution.ID,
		EdgeID:              edge.ID,
		SourceNodeID:        edge.Source,
		TargetNodeID:        edge.Target,
		TraversalIndex:      index,
		IterationIndex:      iterationIndex,
		BranchKey:           truncateRunes(branchKey, 32),
		PayloadSnapshotJSON: serializeSnapshot(payload, r.app.Cfg.Workflow.MaxOutputSnapshotBytes),
		CreatedAt:           time.Now(),
	})
}

// ---------- 标准事件发布 ----------
//
// 下面几个函数负责在执行"到达终态"后对外广播领域事件:成功走 succeeded、失败走 failed,
// 另有一条 recovered 路径专门处理"僵尸执行被回收"的情况;每种终态都会再补一条 finished 事件。

// buildEventBasePayload 拼出所有工作流事件都带的公共字段(执行 id、定义信息、触发类型等)。
func (a *App) buildEventBasePayload(definition *db.WorkflowDefinition, runtimeEntry *db.WorkflowRuntimeEntry, executionID int64, triggerCtx M) M {
	startEntryKey, startNodeType := "", ""
	if runtimeEntry != nil {
		startEntryKey = runtimeEntry.EntryKey
		startNodeType = runtimeEntry.StartType
	}
	return M{
		"workflowExecutionId":       executionID,
		"workflowDefinitionId":      definition.ID,
		"workflowDefinitionCode":    definition.Code,
		"workflowDefinitionVersion": definition.Version,
		"workflowDefinitionName":    definition.DisplayName,
		"startEntryKey":             startEntryKey,
		"startNodeType":             startNodeType,
		"triggerType":               asString(triggerCtx["triggerType"]),
	}
}

// publishStandardEvent 在公共字段基础上合并本次 payload,发布一条标准工作流事件;失败只记日志不中断。
func (a *App) publishStandardEvent(
	ctx context.Context,
	definition *db.WorkflowDefinition, runtimeEntry *db.WorkflowRuntimeEntry,
	executionID int64, nodeID *int64, eventType string, triggerCtx, payload M,
) {
	mergedPayload := a.buildEventBasePayload(definition, runtimeEntry, executionID, triggerCtx)
	for key, value := range payload {
		mergedPayload[key] = value
	}
	metadata := M{"workflowCode": definition.Code, "nodeId": nilOrValue(nodeID)}
	if _, err := a.publishDomainEventContext(
		ctx,
		eventType, "workflow_execution", fmt.Sprint(executionID),
		mergedPayload, metadata, &executionID, nodeID,
	); err != nil {
		log.Printf("[engine] publish standard event failed: execution_id=%d event=%s err=%v", executionID, eventType, err)
	}
}

// publishExecutionSucceeded success 终态后的标准事件。
func (a *App) publishExecutionSucceeded(ctx context.Context, result *runResult) {
	taskResult := orEmptyMap(result.SharedState["taskResult"])
	payload := M{"status": "success"}
	for key, value := range taskResult {
		payload[key] = value
	}
	a.publishStandardEvent(ctx, result.Definition, result.RuntimeEntry, result.Execution.ID, nil, "workflow.execution.succeeded", result.TriggerCtx, payload)
	a.publishStandardEvent(ctx, result.Definition, result.RuntimeEntry, result.Execution.ID, nil, "workflow.execution.finished", result.TriggerCtx, payload)
	if result.RuntimeEntry != nil {
		a.dbWithContext(ctx).Model(&db.WorkflowRuntimeEntry{}).Where("id = ?", result.RuntimeEntry.ID).Updates(map[string]any{
			"last_triggered_at": result.FinishedAt, "last_error_message": "", "updated_at": time.Now(),
		})
	}
}

// publishExecutionFailed failed 终态后的标准事件。
func (a *App) publishExecutionFailed(ctx context.Context, result *runResult, errorMessage string) {
	a.publishFailureEvents(ctx, result.Definition, result.RuntimeEntry, result.Execution.ID, result.TriggerCtx, errorMessage, result.StartedAt, result.FinishedAt)
	if result.RuntimeEntry != nil {
		a.dbWithContext(ctx).Model(&db.WorkflowRuntimeEntry{}).Where("id = ?", result.RuntimeEntry.ID).Updates(map[string]any{
			"last_triggered_at": result.FinishedAt, "last_error_message": errorMessage, "updated_at": time.Now(),
		})
	}
}

// publishRecoveredFailureEvents stale 恢复后的失败事件。
func (a *App) publishRecoveredFailureEvents(ctx context.Context, executionID int64, errorMessage string) {
	var execution db.WorkflowExecution
	if err := a.dbWithContext(ctx).Preload("WorkflowDefinition").First(&execution, executionID).Error; err != nil || execution.WorkflowDefinition == nil {
		return
	}
	var runtimeEntry db.WorkflowRuntimeEntry
	runtimeEntryPtr := &runtimeEntry
	if err := a.dbWithContext(ctx).Where("workflow_definition_id = ? AND entry_key = ?", execution.WorkflowDefinition.ID, execution.StartEntryKey).First(&runtimeEntry).Error; err != nil {
		runtimeEntryPtr = nil
	}
	triggerCtx := extractTriggerContext(execution.ContextSnapshotJSON)
	startedAt := firstTime(execution.StartedAt, execution.ClaimedAt, &execution.QueuedAt)
	finishedAt := time.Now()
	if execution.FinishedAt != nil {
		finishedAt = *execution.FinishedAt
	}
	a.publishFailureEvents(ctx, execution.WorkflowDefinition, runtimeEntryPtr, executionID, triggerCtx, errorMessage, startedAt, finishedAt)
}

func (a *App) publishFailureEvents(
	ctx context.Context,
	definition *db.WorkflowDefinition, runtimeEntry *db.WorkflowRuntimeEntry,
	executionID int64, triggerCtx M, errorMessage string, startedAt, finishedAt time.Time,
) {
	a.publishStandardEvent(ctx, definition, runtimeEntry, executionID, nil, "workflow.execution.failed", triggerCtx, M{
		"status": "failed", "errorMessage": errorMessage,
		"startedAt": fmtTimeV(startedAt), "finishedAt": fmtTimeV(finishedAt),
	})
	a.publishStandardEvent(ctx, definition, runtimeEntry, executionID, nil, "workflow.execution.finished", triggerCtx, M{
		"status": "failed", "errorMessage": errorMessage,
	})
}

// ---------- 小工具 ----------

func extractTriggerContext(payloadJSON string) M {
	value := loadJSONObject(payloadJSON)
	if _, hasType := value["triggerType"]; hasType {
		return value
	}
	if _, hasPayload := value["payload"]; hasPayload {
		return value
	}
	trigger, _ := value["trigger"].(map[string]any)
	if trigger == nil {
		return M{}
	}
	return M{
		"triggerType":     trigger["type"],
		"payload":         orEmptyMap(trigger["payload"]),
		"triggerKey":      trigger["triggerKey"],
		"triggerOutboxId": trigger["triggerOutboxId"],
	}
}

func findStartNode(graph M, startNodeID, startEntryKey string) M {
	nodes, _ := graph["nodes"].([]any)
	for _, nodeAny := range nodes {
		if node, ok := nodeAny.(map[string]any); ok && asString(node["id"]) == startNodeID && startNodeID != "" {
			return node
		}
	}
	return findStartNodeByEntryKey(graph, startEntryKey, "")
}

func serializeRuntimeEntryLite(runtimeEntry *db.WorkflowRuntimeEntry) M {
	if runtimeEntry == nil {
		return M{}
	}
	return M{
		"id": runtimeEntry.ID, "entryKey": runtimeEntry.EntryKey,
		"startType": runtimeEntry.StartType, "isEnabled": runtimeEntry.IsEnabled,
	}
}

func orEmptyMap(value any) M {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return M{}
}

func orString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

// derefString 安全地"解引用"一个 *string 指针:为 nil 就返回空串,否则用 *value 取出里面的字符串。
func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// firstTime 返回第一个"有效"的时间。参数 ...*time.Time 是"变长参数":调用时可传任意多个 *time.Time,
// 函数内部把它们当成一个切片来 range 遍历,取第一个非 nil 且非零值的时间。
func firstTime(values ...*time.Time) time.Time {
	for _, value := range values {
		if value != nil && !value.IsZero() {
			return *value
		}
	}
	return time.Now()
}

func int64Text(value int64) string { return fmt.Sprint(value) }
