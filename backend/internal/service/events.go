package service

import (
	"log"
	"time"

	"coinsphere/backend/internal/db"
)

// domainEvent 标准化领域事件。
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
func (a *App) publishDomainEvent(
	eventType, aggregateType, aggregateID string,
	payload, metadata M,
	workflowExecutionID, workflowExecutionNodeID *int64,
) (int64, error) {
	now := time.Now()
	record := db.DomainEventOutbox{
		EventType: eventType, AggregateType: aggregateType, AggregateID: aggregateID,
		WorkflowExecutionID: workflowExecutionID, WorkflowExecutionNodeID: workflowExecutionNodeID,
		PayloadJSON: dumpJSON(payload), MetadataJSON: dumpJSON(metadata),
		Status: "pending", AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := a.DB.Create(&record).Error; err != nil {
		return 0, err
	}
	return record.ID, nil
}

// drainPendingEvents 拉取并处理一批 pending outbox 事件。
func (a *App) drainPendingEvents(limit int) {
	var rows []db.DomainEventOutbox
	a.DB.Where("status = ? AND available_at <= ?", "pending", time.Now()).
		Order("id ASC").Limit(limit).Find(&rows)
	for i := range rows {
		row := &rows[i]
		event := &domainEvent{
			OutboxID: row.ID, EventType: row.EventType,
			AggregateType: row.AggregateType, AggregateID: row.AggregateID,
			Payload: loadJSONObject(row.PayloadJSON), Metadata: loadJSONObject(row.MetadataJSON),
			WorkflowExecutionID: row.WorkflowExecutionID, WorkflowExecutionNodeID: row.WorkflowExecutionNodeID,
			CreatedAt: row.CreatedAt,
		}
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
		now := time.Now()
		a.DB.Model(row).Updates(map[string]any{
			"status": "processed", "processed_at": now, "last_error_message": "", "updated_at": now,
		})
	}
}

// handleEventTriggeredEntries 把事件匹配到 start.event 入口并入队执行。
func (a *App) handleEventTriggeredEntries(event *domainEvent) error {
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
		if definition == nil || state == nil || state.ActiveWorkflowDefinitionID == nil ||
			*state.ActiveWorkflowDefinitionID != definition.ID {
			continue
		}
		graph := loadJSONObject(definition.GraphJSON)
		startNode := findStartNodeByEntryKey(graph, entry.EntryKey, "start.event")
		if startNode == nil {
			continue
		}
		config, _ := startNode["config"].(map[string]any)
		if !eventTriggerMatches(config, event) {
			continue
		}
		log.Printf(
			"[events] event matched runtime entry: outbox_id=%d workflow_code=%s entry_key=%s",
			event.OutboxID, definition.Code, entry.EntryKey,
		)
		_, err := a.RunRuntimeEntry(entry.ID, M{
			"triggerType":     "event",
			"triggerOutboxId": event.OutboxID,
			"triggerKey":      buildEventTriggerKey(event.EventType, event.OutboxID, entry.ID),
			"idempotencyKey":  buildEventTriggerKey(event.EventType, event.OutboxID, entry.ID),
			"payload":         event.Payload,
		})
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
func eventTriggerMatches(triggerConfig map[string]any, event *domainEvent) bool {
	eventType := asString(triggerConfig["eventType"])
	if eventType == "" || event.EventType != eventType {
		return false
	}
	filters, _ := triggerConfig["filters"].([]any)
	for _, filterAny := range filters {
		filter, ok := filterAny.(map[string]any)
		if !ok {
			continue
		}
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

func findStartNodeByEntryKey(graph M, entryKey, requiredType string) M {
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

func isStartNodeType(nodeType string) bool { return startNodeTypes[nodeType] }
