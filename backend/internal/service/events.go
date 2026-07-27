package service

import (
	"log"
	"time"

	"coinsphere/backend/internal/db"
)

// —— 本文件:领域事件 outbox(发件箱)模式 ——
// 「为什么用 outbox」:业务动作(比如"工作流执行完成")往往要顺带触发别的事(发通知、再触发别的工作流)。
// 若在业务事务里直接去投递/触发,那步一旦失败或很慢,就可能拖垮业务动作,或出现"业务成功了、通知却丢了"。
// outbox 的办法是把两件事拆开、且都落到同一个数据库:
//   ① 业务处理时,只把"发生了什么事件"作为一行写进 outbox 表(和业务数据同一事务,要么都成功要么都回滚,绝不丢事件);
//   ② 另有一个后台循环(见 loops.go)不断把 outbox 里 pending 的事件捞出来,慢慢投递、匹配、触发工作流;
//      失败就标记 failed 并记重试次数,下次再来 —— 业务侧完全不受影响。
// 一句话:先可靠落库、后异步投递,把"产生事件"和"消费事件"解耦。

// domainEvent 标准化领域事件。
// 从 outbox 表的一行"还原"出来、程序内部好用的事件对象。字段里的 *int64(指针)表示"可有可无",没关联时就是 nil;
// time.Time 是标准库的时间类型。这是本文件第一个 struct。见 GO入门笔记『复合类型』
type domainEvent struct {
	OutboxID                int64
	EventType               string
	AggregateType           string
	AggregateID             string
	Payload                 M
	Metadata                M
	WorkflowExecutionID     *int64
	WorkflowExecutionNodeID *int64
	CreatedAt               time.Time
}

// publishDomainEvent 领域事件先落 outbox,由后台循环投递。
// outbox 的"第一步":业务代码调用它,只做一件事 —— 往 outbox 表插入一行 status="pending" 的事件记录,然后立即返回;
// 真正的投递/触发交给后台的 drainPendingEvents。(a *App) 是方法接收者;返回 (int64, error) 是"新记录ID + 错误"。
// 连续几个同类型参数可合并写类型:eventType, aggregateType, aggregateID string 表示这三个都是 string。
func (a *App) publishDomainEvent(
	eventType, aggregateType, aggregateID string,
	payload, metadata M,
	workflowExecutionID, workflowExecutionNodeID *int64,
) (int64, error) {
	// AvailableAt=now 表示"现在就可投递"(也可设未来时间实现延迟事件);:= 是短变量声明,自动推断类型。dumpJSON 把 map 转成 JSON 文本存进一列。
	now := time.Now()
	record := db.DomainEventOutbox{
		EventType: eventType, AggregateType: aggregateType, AggregateID: aggregateID,
		WorkflowExecutionID: workflowExecutionID, WorkflowExecutionNodeID: workflowExecutionNodeID,
		PayloadJSON: dumpJSON(payload), MetadataJSON: dumpJSON(metadata),
		Status: "pending", AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
	// Create(&record) = INSERT 一行;成功后自增主键 record.ID 被 GORM 写回,返回给调用方。见 GO入门笔记『框架:GORM』
	if err := a.DB.Create(&record).Error; err != nil {
		return 0, err
	}
	return record.ID, nil
}

// drainPendingEvents 拉取并处理一批 pending outbox 事件。
// outbox 的"第二步",由后台循环(loops.go)反复调用:每次捞最多 limit 条"到点且待处理"的事件来投递。
func (a *App) drainPendingEvents(limit int) {
	// 查询条件:status=pending 且 available_at<=now(到点了);按 id 升序、限量 limit。? 是占位符,防 SQL 注入。见 GO入门笔记『框架:GORM』
	var rows []db.DomainEventOutbox
	a.DB.Where("status = ? AND available_at <= ?", "pending", time.Now()).
		Order("id ASC").Limit(limit).Find(&rows)
	// for i := range rows + row := &rows[i]:用下标取"指向真实元素的指针",而非副本(与 notify.go 里同款写法)。见 GO入门笔记『复合类型』
	for i := range rows {
		row := &rows[i]
		event := &domainEvent{
			OutboxID: row.ID, EventType: row.EventType,
			AggregateType: row.AggregateType, AggregateID: row.AggregateID,
			Payload: loadJSONObject(row.PayloadJSON), Metadata: loadJSONObject(row.MetadataJSON),
			WorkflowExecutionID: row.WorkflowExecutionID, WorkflowExecutionNodeID: row.WorkflowExecutionNodeID,
			CreatedAt: row.CreatedAt,
		}
		// 逐条投递:处理失败 → 标记 failed、记录错误信息与重试次数(attempt_count+1),continue 跳过继续下一条,不影响别的事件。
		// log.Printf 打印日志(%d 是数字、%v 是任意值);truncateRunes 把过长的错误信息截断到 4000 字符再入库。见 GO入门笔记『错误处理』
		if err := a.handleEventTriggeredEntries(event); err != nil {
			log.Printf("[events] outbox subscriber failed: outbox_id=%d err=%v", row.ID, err)
			now := time.Now()
			a.DB.Model(row).Updates(map[string]any{
				"status": "failed", "processed_at": now,
				"last_error_message": truncateRunes(err.Error(), 4000),
				"attempt_count":      row.AttemptCount + 1, "updated_at": now,
			})
			continue
		}
		// 处理成功 → 标记 processed 并清空错误信息;这条事件从此不再被后台捞起。
		now := time.Now()
		a.DB.Model(row).Updates(map[string]any{
			"status": "processed", "processed_at": now, "last_error_message": "", "updated_at": now,
		})
	}
}

// handleEventTriggeredEntries 把事件匹配到 start.event 入口并入队执行。
// outbox 的"消费方":查出所有"事件触发型(start_type=event)、已启用、且有生效版本"的工作流入口,逐个看这条事件是否命中,命中就跑。
func (a *App) handleEventTriggeredEntries(event *domainEvent) error {
	// Preload 预加载关联对象(定义、运行时状态);Joins 关联运行时状态表以便在 Where 里过滤。见 GO入门笔记『框架:GORM』
	var entries []db.WorkflowRuntimeEntry
	a.DB.Preload("WorkflowDefinition").Preload("WorkflowRuntimeState").
		Joins("JOIN workflow_runtime_states ON workflow_runtime_entries.workflow_runtime_state_id = workflow_runtime_states.id").
		Where(
			"workflow_runtime_entries.start_type = ? AND workflow_runtime_entries.is_enabled = ? "+
				"AND workflow_runtime_states.active_workflow_definition_id IS NOT NULL",
			"event", true,
		).
		Order("workflow_runtime_entries.updated_at DESC, workflow_runtime_entries.id DESC").
		Find(&entries)

	for i := range entries {
		entry := &entries[i]
		definition := entry.WorkflowDefinition
		state := entry.WorkflowRuntimeState
		// 一串 nil / 版本校验:定义或状态缺失、或"当前生效版本"不是这个定义,就跳过。
		// || 会"短路":前面任一条为真就不再计算后面,从而保证轮到 *state.ActiveWorkflowDefinitionID 解引用时它一定非 nil。
		if definition == nil || state == nil || state.ActiveWorkflowDefinitionID == nil ||
			*state.ActiveWorkflowDefinitionID != definition.ID {
			continue
		}
		graph := loadJSONObject(definition.GraphJSON)
		startNode := findStartNodeByEntryKey(graph, entry.EntryKey, "start.event")
		if startNode == nil {
			continue
		}
		// startNode["config"].(map[string]any) 是"类型断言":把 any 取成 map;逗号后的 _ 丢弃"断言是否成功"的标志。
		// eventTriggerMatches 判断这条事件是否满足该入口配置的事件类型与过滤条件,不匹配就跳过。
		config, _ := startNode["config"].(map[string]any)
		if !eventTriggerMatches(config, event) {
			continue
		}
		log.Printf(
			"[events] event matched runtime entry: outbox_id=%d workflow_code=%s entry_key=%s",
			event.OutboxID, definition.Code, entry.EntryKey,
		)
		// 命中!把这条工作流入队执行。_, err := 里的 _ 丢弃第一个返回值,只关心错误。
		// idempotencyKey(幂等键)确保同一事件不会把同一入口重复触发多次。
		_, err := a.RunRuntimeEntry(entry.ID, M{
			"triggerType":     "event",
			"triggerOutboxId": event.OutboxID,
			"triggerKey":      buildEventTriggerKey(event.EventType, event.OutboxID, entry.ID),
			"idempotencyKey":  buildEventTriggerKey(event.EventType, event.OutboxID, entry.ID),
			"payload":         event.Payload,
		})
		// 出错时:若只是"积压超限"就跳过这条(不算失败,下轮再来);其它错误则向上返回,让 drainPendingEvents 把整条事件标记 failed。
		if err != nil {
			if isBacklogExceeded(err) {
				log.Printf("[events] skip event enqueue due to backlog limit: outbox_id=%d entry=%s", event.OutboxID, entry.EntryKey)
				continue
			}
			return err
		}
	}
	return nil
}

// eventTriggerMatches 事件类型与过滤条件匹配。
// 这是普通函数(没有接收者,不挂在任何类型上):入参是入口配置和事件,返回 bool。
func eventTriggerMatches(triggerConfig map[string]any, event *domainEvent) bool {
	// 先比事件类型:配置里没写、或与事件类型不一致,直接判不匹配。
	eventType := asString(triggerConfig["eventType"])
	if eventType == "" || event.EventType != eventType {
		return false
	}
	// 再逐条比过滤器。filter, ok := x.(map[string]any) 是"带 ok 的类型断言":ok 为 false 表示类型不符,这里就跳过。
	filters, _ := triggerConfig["filters"].([]any)
	for _, filterAny := range filters {
		filter, ok := filterAny.(map[string]any)
		if !ok {
			continue
		}
		// 取事件 payload 里 path 指向的实际值,与配置的 expected 比;pyStr 把两边转成可比较的字符串。任一条不满足即整体不匹配。
		path := asString(filter["path"])
		expected := filter["equals"]
		actual := readPath(event.Payload, path)
		if pyStr(actual) != pyStr(expected) {
			return false
		}
	}
	return true
}

func buildEventTriggerKey(eventType string, outboxID, entryID int64) string {
	return "event:" + eventType + ":" + int64Text(outboxID) + ":" + int64Text(entryID)
}

// findStartNodeByEntryKey 在工作流图的 nodes 里找出 entryKey 匹配、且类型符合(requiredType)的起始节点;找不到返回 nil。
func findStartNodeByEntryKey(graph M, entryKey, requiredType string) M {
	// 工作流图存成 JSON,取出来是 any;graph["nodes"].([]any) 把它断言成切片再遍历。
	nodes, _ := graph["nodes"].([]any)
	for _, nodeAny := range nodes {
		node, ok := nodeAny.(map[string]any)
		if !ok {
			continue
		}
		nodeType := asString(node["type"])
		if requiredType != "" {
			if nodeType != requiredType {
				continue
			}
		} else if !isStartNodeType(nodeType) {
			continue
		}
		config, _ := node["config"].(map[string]any)
		if asString(config["entryKey"]) == entryKey {
			return node
		}
	}
	return nil
}

// isStartNodeType 判断某节点类型是否属于"起始节点"。startNodeTypes 是个 map[string]bool(见 workflowdef.go);
// map 查不存在的键会返回值类型的零值(bool 的零值是 false),所以直接返回查表结果即可。
func isStartNodeType(nodeType string) bool { return startNodeTypes[nodeType] }
