package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"coinsphere/backend/internal/db"
)

// —— 本文件:领域事件 outbox(发件箱)模式 ——
// 「为什么用 outbox」:业务动作(比如"工作流执行完成")往往要顺带触发别的事(发通知、再触发别的工作流)。
// 若在业务事务里直接去投递/触发,那步一旦失败或很慢,就可能拖垮业务动作,或出现"业务成功了、通知却丢了"。
// outbox 的办法是把两件事拆开、且都落到同一个数据库:
//   ① 业务处理时,只把"发生了什么事件"作为一行写进 outbox 表;需要与业务状态原子提交的调用方必须传入同一事务;
//   ② 另有一个后台循环(见 loops.go)原子认领到期事件,再投递、匹配并触发工作流;
//      失败按退避重新排队,尝试耗尽进入死信 —— 业务侧完全不受影响。
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

var errLoadEventSubscribers = errors.New("load event subscribers")

const (
	outboxFailureBacklog    = "subscriber_backlog_exceeded"
	outboxFailureQuery      = "subscriber_query_failed"
	outboxFailureSubscriber = "subscriber_delivery_failed"
)

// publishDomainEvent 领域事件先落 outbox,由后台循环投递。
// outbox 的"第一步":业务代码调用它,只做一件事 —— 往 outbox 表插入一行 status="pending" 的事件记录,然后立即返回;
// 真正的投递/触发交给后台的 drainPendingEvents。(a *App) 是方法接收者;返回 (int64, error) 是"新记录ID + 错误"。
// 连续几个同类型参数可合并写类型:eventType, aggregateType, aggregateID string 表示这三个都是 string。
func (a *App) publishDomainEvent(
	eventType, aggregateType, aggregateID string,
	payload, metadata M,
	workflowExecutionID, workflowExecutionNodeID *int64,
) (int64, error) {
	return a.publishDomainEventWithDB(a.DB, eventType, aggregateType, aggregateID, payload, metadata, workflowExecutionID, workflowExecutionNodeID)
}

func (a *App) publishDomainEventWithDB(
	database *gorm.DB,
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
	if err := database.Create(&record).Error; err != nil {
		return 0, err
	}
	return record.ID, nil
}

// drainPendingEvents 依次恢复过期租约、原子认领并投递一批事件，最后领取未告警死信。
// 每个存储步骤彼此独立：单步失败只输出固定操作名，既不记录 payload/metadata，也不输出异常正文、Owner 或 token。
func (a *App) drainPendingEvents(ctx context.Context, limit int) {
	if ctx.Err() != nil || limit < 1 {
		return
	}
	database := a.dbWithContext(ctx)
	if recovered, err := db.RecoverExpiredOutboxEvents(ctx, database, limit); err != nil {
		slog.ErrorContext(ctx, "outbox store operation failed", "operation", "recover", "error_category", "database")
	} else if len(recovered) > 0 {
		slog.InfoContext(ctx, "outbox leases recovered", "count", len(recovered))
	}

	// 存储层保留原子批量认领契约；dispatcher 按实际处理能力逐条即时认领，
	// 避免慢首项让尚未开始处理的批次尾部事件白白耗尽租约和尝试次数。
	for range limit {
		claims, err := db.ClaimOutboxEvents(ctx, database, a.WorkerID, 1, a.outboxLeaseDuration())
		if err != nil {
			slog.ErrorContext(ctx, "outbox store operation failed", "operation", "claim", "error_category", "database")
			break
		}
		if len(claims) == 0 {
			break
		}
		a.deliverClaimedOutboxEvent(ctx, claims[0])
		if ctx.Err() != nil {
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	alertIDs, err := db.MarkOutboxDeadLettersAlerted(ctx, database, limit)
	if err != nil {
		slog.ErrorContext(ctx, "outbox store operation failed", "operation", "alert", "error_category", "database")
		return
	}
	for _, id := range alertIDs {
		slog.WarnContext(ctx, "outbox dead letter", "outbox_id", id)
	}
}

// deliverClaimedOutboxEvent 在处理前先验证并续租，慢订阅期间按租约三分之一周期续租。
// 若续租失败会取消本次处理，最终 fenced 写入只能由仍持有当前租约代次的 Owner 完成。
func (a *App) deliverClaimedOutboxEvent(ctx context.Context, claim db.DomainEventOutbox) {
	leaseDuration := a.outboxLeaseDuration()
	renewed, err := db.RenewOutboxEventLease(ctx, a.dbWithContext(ctx), claim, leaseDuration)
	if err != nil {
		slog.ErrorContext(ctx, "outbox store operation failed", "operation", "renew", "outbox_id", claim.ID, "error_category", "database")
		return
	}
	if !renewed {
		slog.WarnContext(ctx, "outbox lease lost", "operation", "renew", "outbox_id", claim.ID, "attempt", claim.AttemptCount)
		return
	}

	deliveryCtx, cancelDelivery := context.WithCancel(ctx)
	stopHeartbeat := make(chan struct{})
	heartbeatResult := make(chan bool, 1)
	go func() {
		ticker := time.NewTicker(leaseDuration / 3)
		defer ticker.Stop()
		for {
			select {
			case <-stopHeartbeat:
				heartbeatResult <- true
				return
			case <-deliveryCtx.Done():
				heartbeatResult <- false
				return
			case <-ticker.C:
				ok, err := db.RenewOutboxEventLease(deliveryCtx, a.dbWithContext(deliveryCtx), claim, leaseDuration)
				if err != nil {
					slog.ErrorContext(deliveryCtx, "outbox store operation failed", "operation", "heartbeat", "outbox_id", claim.ID, "error_category", "database")
				} else if !ok {
					slog.WarnContext(deliveryCtx, "outbox lease lost", "operation", "heartbeat", "outbox_id", claim.ID, "attempt", claim.AttemptCount)
				}
				if err != nil || !ok {
					cancelDelivery()
					heartbeatResult <- false
					return
				}
			}
		}
	}()

	event := &domainEvent{
		OutboxID: claim.ID, EventType: claim.EventType,
		AggregateType: claim.AggregateType, AggregateID: claim.AggregateID,
		Payload: loadJSONObject(claim.PayloadJSON), Metadata: loadJSONObject(claim.MetadataJSON),
		WorkflowExecutionID: claim.WorkflowExecutionID, WorkflowExecutionNodeID: claim.WorkflowExecutionNodeID,
		CreatedAt: claim.CreatedAt,
	}
	deliveryErr := a.handleEventTriggeredEntries(deliveryCtx, event)
	close(stopHeartbeat)
	leaseValid := <-heartbeatResult
	cancelDelivery()
	if !leaseValid || ctx.Err() != nil {
		return
	}
	if deliveryErr == nil {
		completed, err := db.CompleteOutboxEvent(ctx, a.dbWithContext(ctx), claim)
		if err != nil {
			slog.ErrorContext(ctx, "outbox store operation failed", "operation", "complete", "outbox_id", claim.ID, "error_category", "database")
		} else if !completed {
			slog.WarnContext(ctx, "outbox lease lost", "operation", "complete", "outbox_id", claim.ID, "attempt", claim.AttemptCount)
		}
		return
	}

	category := outboxFailureSubscriber
	if errors.Is(deliveryErr, ErrBacklogExceeded) {
		category = outboxFailureBacklog
	} else if errors.Is(deliveryErr, errLoadEventSubscribers) {
		category = outboxFailureQuery
	}
	failed, err := db.FailOutboxEvent(ctx, a.dbWithContext(ctx), claim, a.retryBackoff(claim.AttemptCount), category)
	if err != nil {
		slog.ErrorContext(ctx, "outbox store operation failed", "operation", "fail", "outbox_id", claim.ID, "error_category", "database")
	} else if !failed {
		slog.WarnContext(ctx, "outbox lease lost", "operation", "fail", "outbox_id", claim.ID, "attempt", claim.AttemptCount)
	} else {
		slog.WarnContext(ctx, "outbox delivery failed", "outbox_id", claim.ID, "attempt", claim.AttemptCount, "error_category", category)
	}
}

func (a *App) outboxLeaseDuration() time.Duration {
	seconds := a.Cfg.Workflow.OutboxLeaseSeconds
	if seconds < 1 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second
}

// handleEventTriggeredEntries 把事件匹配到 start.event 入口并入队执行。
// outbox 的"消费方":查出所有"事件触发型(start_type=event)、已启用、且有生效版本"的工作流入口,逐个看这条事件是否命中,命中就跑。
func (a *App) handleEventTriggeredEntries(ctx context.Context, event *domainEvent) error {
	notificationErr := a.deliverStrategySignalNotification(ctx, event)
	// Preload 预加载关联对象(定义、运行时状态);Joins 关联运行时状态表以便在 Where 里过滤。见 GO入门笔记『框架:GORM』
	var entries []db.WorkflowRuntimeEntry
	result := a.DB.WithContext(ctx).Preload("WorkflowDefinition").Preload("WorkflowRuntimeState").
		Joins("JOIN workflow_runtime_states ON workflow_runtime_entries.workflow_runtime_state_id = workflow_runtime_states.id").
		Where(
			"workflow_runtime_entries.start_type = ? AND workflow_runtime_entries.is_enabled = ? "+
				"AND workflow_runtime_states.active_workflow_definition_id IS NOT NULL",
			"event", true,
		).
		Order("workflow_runtime_entries.updated_at DESC, workflow_runtime_entries.id DESC").
		Find(&entries)
	if result.Error != nil {
		return fmt.Errorf("%w: %v", errLoadEventSubscribers, result.Error)
	}

	for i := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
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
		slog.DebugContext(ctx, "event matched runtime entry",
			"outbox_id", event.OutboxID,
			"workflow_code", definition.Code,
			"entry_key", entry.EntryKey)
		// 命中!把这条工作流入队执行。_, err := 里的 _ 丢弃第一个返回值,只关心错误。
		// idempotencyKey(幂等键)确保同一事件不会把同一入口重复触发多次。
		_, err := a.RunRuntimeEntry(entry.ID, M{
			"triggerType":     "event",
			"triggerOutboxId": event.OutboxID,
			"triggerKey":      buildEventTriggerKey(event.EventType, event.OutboxID, entry.ID),
			"idempotencyKey":  buildEventTriggerKey(event.EventType, event.OutboxID, entry.ID),
			"payload":         event.Payload,
		})
		// 任一入口未能入队都让整条事件重排；已成功入口由稳定幂等键去重，积压入口则在退避后继续尝试。
		if err != nil {
			if errors.Is(err, ErrBacklogExceeded) {
				slog.WarnContext(ctx, "event enqueue deferred", "outbox_id", event.OutboxID, "entry_key", entry.EntryKey, "error_category", outboxFailureBacklog)
			}
			return err
		}
	}
	return notificationErr
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
