// engine.go —— 工作流"图执行引擎"。
//
// 一个工作流被画成一张"有向图":一堆节点(node)用边(edge)连起来,像流程图。
// 本文件负责:从起始节点出发、沿着边把这张图"跑"一遍,每到一个节点就调用它对应的
// 处理函数(处理函数登记在 nodes.go 里)。节点的先后顺序由边的方向决定 —— 顺着箭头往下走。
//
// 跑图时会做三件"记账":
//   1. 给每个节点记一条执行日志(WorkflowExecutionNode),含"输入快照"和"输出快照";
//   2. 给每条走过的边记一条流转日志(WorkflowExecutionTransition);
//   3. 结束后按成功/失败发布标准领域事件(succeeded / failed / finished)。
//
// 注意:本文件只管"把图跑完、并把错误往上抛";至于失败要不要重试、execution 的终态
// 状态机怎么推进,由调用方负责。

package service

import (
	"fmt"
	"log"
	"sort"
	"time"

	"coinsphere/backend/internal/db"
)

// runResult 一次跑图结束后的上下文。
//
// struct(结构体)就是把一组相关字段打包成一个整体,类似别的语言的"对象/记录"。见 GO入门笔记『复合类型』。
// 字段类型前的 * 表示"指针"(只存地址、不整块复制,也能用 nil 表示"还没有值");
// M 是本项目别名 = map[string]any,即"字符串键 → 任意值"的字典,专门装 JSON 那种动态数据。
// 字段含义:Execution 本次执行、Definition 工作流定义、RuntimeEntry 运行时入口(均为指针);
// TriggerCtx 触发上下文;SharedState 跑图全程共享的"变量表",节点之间靠它传数据。
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
//
// 函数签名里 (a *App) 是"接收者",表示这是 App 类型的一个方法,方法内用 a 指代当前 App
// (相当于别的语言的 this/self)。见 GO入门笔记『方法与接收者』。
// 返回值 (*runResult, error) 是"多返回值":Go 习惯把结果和错误一起返回。见 GO入门笔记『变量、函数、错误』。
func (a *App) runExecutionGraph(executionID int64) (*runResult, error) {
	// := 是短变量声明(自动推断类型);这里一次接住两个返回值:结果 execution 和错误 err。
	execution, err := a.getExecutionByID(executionID)
	// Go 没有 try/except,而是靠"err 是不是 nil"判断出错;出错就提前 return。见 GO入门笔记『变量、函数、错误』。
	if err != nil {
		return nil, err
	}
	definition := execution.WorkflowDefinition
	// 指针可能是 nil(空),取用前先判空;bizErr 是本项目的"业务错误"构造器,用于返回可读的报错。
	if definition == nil {
		return nil, bizErr("Workflow definition does not exist")
	}
	// GraphJSON 是存在数据库里的一段 JSON 文本;loadJSONObject 把它解析成 M(map)方便按键取值。
	graph := loadJSONObject(definition.GraphJSON)
	startedAt := firstTime(execution.StartedAt, execution.ClaimedAt, &execution.QueuedAt)
	triggerCtx := extractTriggerContext(execution.ContextSnapshotJSON)
	triggerType := execution.TriggerType
	if triggerType == "" {
		triggerType = asString(triggerCtx["triggerType"])
	}
	inputs := loadJSONObject(execution.InputSnapshotJSON)

	runtimeEntry := a.getRuntimeEntryByDefinitionAndKey(definition.ID, execution.StartEntryKey)
	// sharedState 是跑图全程的"共享变量表"(M{...} 是 map 字面量,直接列出键值对)。
	// 后续每个节点都能读写它:inputs 是初始输入,trigger 是触发信息,
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
	}

	// &runResult{...} 用 & 取地址,得到一个指向新建 runResult 的指针(*runResult)。见 GO入门笔记『复合类型』。
	result := &runResult{
		Execution: execution, Definition: definition, RuntimeEntry: runtimeEntry,
		TriggerCtx: triggerCtx, SharedState: sharedState, StartedAt: startedAt,
	}

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
	// 核心:从起始节点开始真正"跑图"(见下面的 runGraph)。失败就记结束时间、打日志、把错误抛给调用方。
	if err := a.runGraph(result, graph, asString(startNode["id"])); err != nil {
		result.FinishedAt = time.Now()
		log.Printf("[engine] execution failed: execution_id=%d workflow_code=%s err=%v", execution.ID, definition.Code, err)
		return result, err
	}
	result.FinishedAt = time.Now()
	log.Printf("[engine] execution finished: execution_id=%d workflow_code=%s status=success", execution.ID, definition.Code)
	return result, nil
}

// runGraph 深度遍历执行节点,并落节点/边日志。
//
// 这是整个执行引擎的"主循环"所在:先把图的节点和边整理成便于查找的索引,
// 再从 startNodeID 出发,沿着边"深度优先"地一个个执行下去(见下面的 executeFrom)。
func (a *App) runGraph(result *runResult, graph M, startNodeID string) error {
	// graph["nodes"] 的值类型是 any(即空接口 interface{},可装任意类型,见 GO入门笔记『接口』)。
	// .([]any) 是"类型断言":断言它其实是一个切片;逗号后面本可接一个 bool 表示是否成功,这里用 _ 丢弃。
	nodesAny, _ := graph["nodes"].([]any)
	// nodeMap:把"节点 id → 节点"建成索引,之后按 id 找节点就很快。map 是键值字典。
	nodeMap := map[string]M{}
	// for ... range 遍历切片:下标用不到就写 _,只取每个元素 nodeAny。见 GO入门笔记『复合类型』。
	for _, nodeAny := range nodesAny {
		// 再断言每个元素确实是 map[string]any;ok 为真才收进索引(跳过脏数据)。
		if node, ok := nodeAny.(map[string]any); ok {
			nodeMap[asString(node["id"])] = node
		}
	}
	// adjacency 是"邻接表":源节点 id → 从它出发的所有边。这就是图里"边"的存储方式。
	edgesAny, _ := graph["edges"].([]any)
	adjacency := map[string][]M{}
	for _, edgeAny := range edgesAny {
		if edge, ok := edgeAny.(map[string]any); ok {
			source := asString(edge["source"])
			// append 往切片末尾追加元素;adjacency[source] 不存在时会被当成空切片,可直接 append。
			adjacency[source] = append(adjacency[source], edge)
		}
	}
	// 把每个节点的出边按 edge id 排序,保证每次跑图的分支顺序一致(结果可复现)。
	// sort.Slice 的第二个参数是一个匿名"比较函数":Go 里函数本身也是值,可以直接当参数传进去(回调)。
	for _, edgeList := range adjacency {
		sort.Slice(edgeList, func(i, j int) bool {
			return asString(edgeList[i]["id"]) < asString(edgeList[j]["id"])
		})
	}

	// traversalIndex:记录"走过的第几条边",给流转日志排序用。
	traversalIndex := 0
	execution := result.Execution

	// logTransition 是一个赋给变量的匿名函数(闭包):它能记住外面的 traversalIndex 等变量。
	// 每走过一条边就往数据库插一条流转日志,并存下当时的 payload 快照。
	// a.DB.Create(&行) 是 GORM 的插入操作(& 传指针,把这条记录写进表)。见 GO入门笔记『框架:GORM』。
	logTransition := func(edge M, payload M, iterationIndex *int, branchKey string) {
		traversalIndex++
		a.DB.Create(&db.WorkflowExecutionTransition{
			WorkflowExecutionID: execution.ID,
			EdgeID:              asString(edge["id"]),
			SourceNodeID:        asString(edge["source"]),
			TargetNodeID:        asString(edge["target"]),
			TraversalIndex:      traversalIndex,
			IterationIndex:      iterationIndex,
			BranchKey:           truncateRunes(branchKey, 32),
			PayloadSnapshotJSON: serializeSnapshot(payload, a.Cfg.Workflow.MaxOutputSnapshotBytes),
			CreatedAt:           time.Now(),
		})
	}

	// executeFrom 是真正的"执行主循环":访问一个节点 → 执行它 → 再顺着它的出边递归执行下游节点,
	// 就这样深度优先地把整张图走完(像顺着流程图一路往下点)。
	// 这里先用 var 声明、再赋值,是 Go 写"递归匿名函数"的固定套路:
	// 只有先声明了名字,函数体内部才能引用自己(executeFrom 调 executeFrom)。
	var executeFrom func(nodeID string) error
	executeFrom = func(nodeID string) error {
		// 按 id 取节点;map 里取不到 key 会得到零值(这里是 nil),据此判断节点不存在。
		node := nodeMap[nodeID]
		if node == nil {
			return bizErr("Workflow node does not exist: %s", nodeID)
		}
		nodeType := asString(node["type"])
		// 进入节点就先写一条"运行中"的节点日志,并保存此刻 sharedState 的"输入快照"
		// (serializeSnapshot 把它转成 JSON,超过配置上限会截断,避免存爆)。
		nodeLog := db.WorkflowExecutionNode{
			WorkflowExecutionID: execution.ID, NodeID: nodeID, NodeType: nodeType,
			Status: "running", StartedAt: time.Now(),
			InputSnapshotJSON: serializeSnapshot(result.SharedState, a.Cfg.Workflow.MaxOutputSnapshotBytes),
		}
		// GORM 插入后用 .Error 字段拿错误;插入失败就中止本节点。
		if err := a.DB.Create(&nodeLog).Error; err != nil {
			return err
		}

		// publishEvent 也是一个闭包,待会儿传给节点用:节点想发领域事件时就调它。
		// 里面用 for range 遍历 map(得到 key, value),把调用方传入的 payload/metadata 合并到基础字段上。
		publishEvent := func(eventType, aggregateType string, payload, metadata M) (int64, error) {
			mergedPayload := a.buildEventBasePayload(result.Definition, result.RuntimeEntry, execution.ID, result.TriggerCtx)
			for key, value := range payload {
				mergedPayload[key] = value
			}
			mergedMetadata := M{
				"triggerType":  orString(asString(result.TriggerCtx["triggerType"]), execution.TriggerType),
				"workflowCode": result.Definition.Code,
			}
			for key, value := range metadata {
				mergedMetadata[key] = value
			}
			return a.publishDomainEvent(
				eventType, aggregateType, fmt.Sprint(execution.ID),
				mergedPayload, mergedMetadata, &execution.ID, &nodeLog.ID,
			)
		}

		// 关键一步:按节点类型从"节点注册表"(定义在 nodes.go)里查出对应处理器 definitionImpl,
		// 再调用它的 Execute 把这个节点真正执行掉。Execute 拿到一个上下文 struct,
		// 里面装了共享状态、当前节点,以及上面的 publishEvent 回调等。
		definitionImpl, err := getNodeDefinition(nodeType)
		var nodeResult *nodeExecResult
		// 只有查到了处理器(err == nil)才执行;查不到就带着错误往下走,进入失败分支。
		if err == nil {
			nodeResult, err = definitionImpl.Execute(&nodeExecContext{
				App: a, Definition: result.Definition, RuntimeEntry: result.RuntimeEntry,
				Execution: execution, NodeLog: &nodeLog, Node: node, Graph: graph,
				SharedState: result.SharedState, TriggerCtx: result.TriggerCtx,
				PublishEvent: publishEvent,
			})
		}
		// 节点执行完:算出耗时,按成功/失败两条路更新刚才那条节点日志。
		finishedAt := time.Now()
		durationMs := finishedAt.Sub(nodeLog.StartedAt).Milliseconds()
		// 失败:标记 failed 并记下错误消息,然后 return err —— 这个错误会一路往上冒
		// (runGraph → runExecutionGraph → 调用方),整张图就此中止。
		if err != nil {
			a.DB.Model(&nodeLog).Updates(map[string]any{
				"status": "failed", "finished_at": finishedAt, "duration_ms": durationMs,
				"error_message": err.Error(),
			})
			log.Printf("[engine] node failed: execution_id=%d node_id=%s err=%v", execution.ID, nodeID, err)
			return err
		}
		// 成功:标记 success,并把节点输出存成"输出快照"(同样按上限截断)。
		a.DB.Model(&nodeLog).Updates(map[string]any{
			"status": "success", "finished_at": finishedAt, "duration_ms": durationMs,
			"output_snapshot_json": serializeSnapshot(nodeResult.Output, a.Cfg.Workflow.MaxOutputSnapshotBytes),
		})

		// Terminate 为真表示这是"结束"节点:到此为止,不再往下走。
		if nodeResult.Terminate {
			return nil
		}

		// nextEdges:当前节点的所有出边(下游走向)。
		nextEdges := adjacency[nodeID]
		// 若节点选了某个分支(如条件节点选了 "true"/"false"),只保留匹配该分支的边。
		// SelectedBranch 是 *string 指针,非 nil 才有选择;*nodeResult.SelectedBranch 是解引用取出字符串。
		// make([]M, 0, n):新建长度 0、预留 n 容量的切片,后续 append 更省内存分配。
		if nodeResult.SelectedBranch != nil {
			filtered := make([]M, 0, len(nextEdges))
			for _, edge := range nextEdges {
				if edgeBranchKey(edge) == *nodeResult.SelectedBranch {
					filtered = append(filtered, edge)
				}
			}
			nextEdges = filtered
		}

		// 遍历节点(foreach):ForeachItems 非 nil 表示要对一个数组逐个跑一遍下游子图。
		if nodeResult.ForeachItems != nil {
			if len(nextEdges) == 0 {
				return nil
			}
			nextEdge := nextEdges[0]
			nextNodeID := asString(nextEdge["target"])
			loopConfigAll, _ := result.SharedState["loopConfig"].(map[string]any)
			loopConfig, _ := loopConfigAll[nodeID].(map[string]any)
			itemKey := orString(asString(loopConfig["itemKey"]), "currentItem")
			indexKey := orString(asString(loopConfig["indexKey"]), "currentIndex")
			// 从 map 取值的"逗号 ok"写法:第二个返回值表示这个键原来是否存在。
			// 先把旧值记下来,循环结束后好还原,避免嵌套循环互相覆盖。
			previousItem, hasPreviousItem := result.SharedState[itemKey]
			previousIndex, hasPreviousIndex := result.SharedState[indexKey]
			// range 一个切片给出下标 index 和元素 item;这里每个元素都跑一遍下游。
			for index, item := range nodeResult.ForeachItems {
				result.SharedState[itemKey] = item
				result.SharedState[indexKey] = index
				iteration := index
				logTransition(nextEdge, M{
					"output": nodeResult.Output, "loopItem": item, "loopIndex": index,
					"itemKey": itemKey, "indexKey": indexKey,
				}, &iteration, edgeBranchKey(nextEdge))
				// 递归执行下游节点;子图里任何一步出错都立即中止整轮循环。
				if err := executeFrom(nextNodeID); err != nil {
					return err
				}
			}
			// 循环收尾:原来有旧值就还原,原来没有就用 delete 从 map 里删掉这个键。
			if hasPreviousItem {
				result.SharedState[itemKey] = previousItem
			} else {
				delete(result.SharedState, itemKey)
			}
			if hasPreviousIndex {
				result.SharedState[indexKey] = previousIndex
			} else {
				delete(result.SharedState, indexKey)
			}
			return nil
		}

		// 普通情况:把当前节点的每一条出边都走一遍 —— 先记流转日志,再递归进入目标节点。
		// 这层 for + 递归就构成了"深度优先"遍历整张图。iterationIndex 传 nil 表示这不是循环里的一步。
		for _, edge := range nextEdges {
			payload := M{"output": nodeResult.Output}
			branchKey := edgeBranchKey(edge)
			if nodeResult.SelectedBranch != nil {
				payload["selectedBranch"] = *nodeResult.SelectedBranch
				branchKey = *nodeResult.SelectedBranch
			}
			logTransition(edge, payload, nil, branchKey)
			if err := executeFrom(asString(edge["target"])); err != nil {
				return err
			}
		}
		return nil
	}

	// 从起始节点点火,整张图的执行就此展开。
	return executeFrom(startNodeID)
}

// ---------- 标准事件发布 ----------
//
// 下面几个函数负责在执行"到达终态"后对外广播领域事件:成功走 succeeded、失败走 failed,
// 另有一条 recovered 路径专门处理"僵尸执行被回收"的情况;每种终态都会再补一条 finished 事件。
// 这就是本文件对"结果/错误"的分类与通告。

// buildEventBasePayload 拼出所有工作流事件都带的公共字段(执行 id、定义信息、触发类型等)。
func (a *App) buildEventBasePayload(definition *db.WorkflowDefinition, runtimeEntry *db.WorkflowRuntimeEntry, executionID int64, triggerCtx M) M {
	// 一行同时给两个变量赋初值(多重赋值);runtimeEntry 可能为 nil,存在才覆盖。
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
	definition *db.WorkflowDefinition, runtimeEntry *db.WorkflowRuntimeEntry,
	executionID int64, nodeID *int64, eventType string, triggerCtx, payload M,
) {
	mergedPayload := a.buildEventBasePayload(definition, runtimeEntry, executionID, triggerCtx)
	for key, value := range payload {
		mergedPayload[key] = value
	}
	metadata := M{"workflowCode": definition.Code, "nodeId": nilOrValue(nodeID)}
	if _, err := a.publishDomainEvent(
		eventType, "workflow_execution", fmt.Sprint(executionID),
		mergedPayload, metadata, &executionID, nodeID,
	); err != nil {
		log.Printf("[engine] publish standard event failed: execution_id=%d event=%s err=%v", executionID, eventType, err)
	}
}

// publishExecutionSucceeded success 终态后的标准事件。
func (a *App) publishExecutionSucceeded(result *runResult) {
	taskResult := orEmptyMap(result.SharedState["taskResult"])
	payload := M{"status": "success"}
	for key, value := range taskResult {
		payload[key] = value
	}
	a.publishStandardEvent(result.Definition, result.RuntimeEntry, result.Execution.ID, nil, "workflow.execution.succeeded", result.TriggerCtx, payload)
	a.publishStandardEvent(result.Definition, result.RuntimeEntry, result.Execution.ID, nil, "workflow.execution.finished", result.TriggerCtx, payload)
	if result.RuntimeEntry != nil {
		a.DB.Model(&db.WorkflowRuntimeEntry{}).Where("id = ?", result.RuntimeEntry.ID).Updates(map[string]any{
			"last_triggered_at": result.FinishedAt, "last_error_message": "", "updated_at": time.Now(),
		})
	}
}

// publishExecutionFailed failed 终态后的标准事件。
func (a *App) publishExecutionFailed(result *runResult, errorMessage string) {
	a.publishFailureEvents(result.Definition, result.RuntimeEntry, result.Execution.ID, result.TriggerCtx, errorMessage, result.StartedAt, result.FinishedAt)
	if result.RuntimeEntry != nil {
		a.DB.Model(&db.WorkflowRuntimeEntry{}).Where("id = ?", result.RuntimeEntry.ID).Updates(map[string]any{
			"last_triggered_at": result.FinishedAt, "last_error_message": errorMessage, "updated_at": time.Now(),
		})
	}
}

// publishRecoveredFailureEvents stale 恢复后的失败事件。
func (a *App) publishRecoveredFailureEvents(executionID int64, errorMessage string) {
	execution, err := a.getExecutionByID(executionID)
	if err != nil || execution.WorkflowDefinition == nil {
		return
	}
	runtimeEntry := a.getRuntimeEntryByDefinitionAndKey(execution.WorkflowDefinition.ID, execution.StartEntryKey)
	triggerCtx := extractTriggerContext(execution.ContextSnapshotJSON)
	startedAt := firstTime(execution.StartedAt, execution.ClaimedAt, &execution.QueuedAt)
	finishedAt := time.Now()
	if execution.FinishedAt != nil {
		finishedAt = *execution.FinishedAt
	}
	a.publishFailureEvents(execution.WorkflowDefinition, runtimeEntry, executionID, triggerCtx, errorMessage, startedAt, finishedAt)
}

func (a *App) publishFailureEvents(
	definition *db.WorkflowDefinition, runtimeEntry *db.WorkflowRuntimeEntry,
	executionID int64, triggerCtx M, errorMessage string, startedAt, finishedAt time.Time,
) {
	a.publishStandardEvent(definition, runtimeEntry, executionID, nil, "workflow.execution.failed", triggerCtx, M{
		"status": "failed", "errorMessage": errorMessage,
		"startedAt": fmtTimeV(startedAt), "finishedAt": fmtTimeV(finishedAt),
	})
	a.publishStandardEvent(definition, runtimeEntry, executionID, nil, "workflow.execution.finished", triggerCtx, M{
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

func buildStartInputs(startNode M, triggerCtx M) M {
	config, _ := startNode["config"].(map[string]any)
	result := M{}
	if base, ok := config["inputBindings"].(map[string]any); ok {
		for key, value := range base {
			result[key] = value
		}
	}
	if extra, ok := triggerCtx["inputs"].(map[string]any); ok {
		for key, value := range extra {
			result[key] = value
		}
	}
	return result
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

func timeNow() time.Time { return time.Now() }
