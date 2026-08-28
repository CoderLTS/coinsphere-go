package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const workflowFailureEventType = "io.coinsphere.workflow.run.failed"

var errWorkflowBackpressure = errors.New("workflow backlog limit reached")

type WorkflowEventView struct {
	ID           int64  `json:"id"`
	Source       string `json:"source"`
	EventID      string `json:"eventId"`
	Type         string `json:"type"`
	PartitionKey string `json:"partitionKey"`
	ReceivedAt   string `json:"receivedAt"`
}

func (a *App) PublishWorkflowEvent(ctx context.Context, event cloudevents.Event) (WorkflowEventView, error) {
	record, err := a.persistWorkflowEvent(ctx, event, 0)
	if err != nil {
		return WorkflowEventView{}, err
	}
	a.publishWorkflowEventRunUpdates(record.ID)
	return workflowEventView(record), nil
}

func (a *App) PublishWorkflowWebhook(ctx context.Context, workflowID int64, secret, eventID, partitionKey string, data map[string]any) (WorkflowEventView, error) {
	eventID = strings.TrimSpace(eventID)
	partitionKey = strings.TrimSpace(partitionKey)
	if workflowID <= 0 || strings.TrimSpace(secret) == "" || len(secret) > maxWorkflowSecretBytes || eventID == "" || len(eventID) > 128 ||
		partitionKey == "" || len(partitionKey) > 256 || data == nil {
		return WorkflowEventView{}, errors.New("webhook request is invalid")
	}
	var record db.WorkflowEventRecord
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var workflow db.Workflow
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&workflow, workflowID).Error; err != nil ||
			workflow.Status != WorkflowStatusActive || workflow.ActiveRevisionID == nil {
			return fmt.Errorf("%w: webhook", ErrNotFound)
		}
		var revision db.WorkflowRevision
		if err := tx.First(&revision, *workflow.ActiveRevisionID).Error; err != nil {
			return fmt.Errorf("%w: webhook", ErrNotFound)
		}
		graph, err := a.buildWorkflowRunGraph(revision.GraphJSON)
		if err != nil {
			return errors.New("load webhook workflow graph failed")
		}
		trigger := graph.nodes[revision.MainTriggerNodeID]
		if trigger.NodeType != "official.connector.webhook" {
			return fmt.Errorf("%w: webhook", ErrNotFound)
		}
		var binding db.WorkflowSecretBinding
		if err := tx.Where("revision_id = ? AND node_instance_id = ? AND field_name = 'secret'", revision.ID, trigger.NodeInstanceID).
			First(&binding).Error; err != nil {
			return fmt.Errorf("%w: webhook", ErrNotFound)
		}
		expected, err := a.Cipher.Decrypt(binding.EncryptedValue)
		if err != nil {
			return errors.New("decrypt webhook secret failed")
		}
		expectedHash, actualHash := sha256.Sum256([]byte(expected)), sha256.Sum256([]byte(secret))
		if subtle.ConstantTimeCompare(expectedHash[:], actualHash[:]) != 1 {
			return fmt.Errorf("%w: webhook", ErrNotFound)
		}
		var config struct {
			EventType string `json:"eventType"`
		}
		if json.Unmarshal(trigger.Config, &config) != nil || strings.TrimSpace(config.EventType) == "" {
			return errors.New("webhook trigger configuration is invalid")
		}
		source := fmt.Sprintf("urn:coinsphere:connector:webhook:%d", workflowID)
		identity := fmt.Sprintf("%d:%s%s", len(source), source, eventID)
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, identity).Error; err != nil {
			return errors.New("lock webhook event identity failed")
		}
		eventTime := time.Now().UTC()
		var existing db.WorkflowEventRecord
		if err := tx.Where("source = ? AND event_id = ?", source, eventID).First(&existing).Error; err == nil {
			var persisted struct {
				Time time.Time `json:"time"`
			}
			if json.Unmarshal([]byte(existing.EventJSON), &persisted) != nil || persisted.Time.IsZero() {
				return errors.New("load existing webhook event failed")
			}
			eventTime = persisted.Time.UTC()
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("load existing webhook event failed")
		}
		event := cloudevents.NewEvent()
		event.SetID(eventID)
		event.SetSource(source)
		event.SetType(strings.TrimSpace(config.EventType))
		event.SetTime(eventTime)
		event.SetExtension("partitionkey", partitionKey)
		if err := event.SetData(cloudevents.ApplicationJSON, data); err != nil {
			return errors.New("encode webhook event failed")
		}
		record, err = a.persistWorkflowEventTx(tx, event, workflowID)
		return err
	})
	if err != nil {
		return WorkflowEventView{}, err
	}
	a.publishWorkflowEventRunUpdates(record.ID)
	return workflowEventView(record), nil
}

func (a *App) publishWorkflowEventRunUpdates(eventRecordID int64) {
	if eventRecordID <= 0 {
		return
	}
	var deliveries []db.WorkflowEventDelivery
	if a.DB.Select("workflow_id, run_id").Where("event_record_id = ?", eventRecordID).Find(&deliveries).Error != nil {
		return
	}
	for _, delivery := range deliveries {
		a.PublishWorkflowRunUpdated(delivery.WorkflowID, delivery.RunID)
	}
}

func (a *App) persistWorkflowEvent(ctx context.Context, event cloudevents.Event, targetWorkflowID int64) (db.WorkflowEventRecord, error) {
	var record db.WorkflowEventRecord
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		record, err = a.persistWorkflowEventTx(tx, event, targetWorkflowID)
		return err
	})
	return record, err
}

func (a *App) persistWorkflowEventTx(tx *gorm.DB, event cloudevents.Event, targetWorkflowID int64) (db.WorkflowEventRecord, error) {
	raw, partitionKey, _, err := validateWorkflowCloudEvent(event)
	if err != nil {
		return db.WorkflowEventRecord{}, err
	}
	now := time.Now().UTC()
	record := db.WorkflowEventRecord{
		Source: event.Source(), EventID: event.ID(), SpecVersion: event.SpecVersion(), EventType: event.Type(),
		Subject: event.Subject(), EventTime: event.Time().UTC(), DataContentType: event.DataContentType(),
		PartitionKey: partitionKey, EventJSON: string(raw), ReceivedAt: now,
	}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
	if result.Error != nil {
		return db.WorkflowEventRecord{}, errors.New("persist workflow event failed")
	}
	if result.RowsAffected == 0 {
		if err := tx.Where("source = ? AND event_id = ?", event.Source(), event.ID()).First(&record).Error; err != nil {
			return db.WorkflowEventRecord{}, errors.New("load duplicate workflow event failed")
		}
		same, err := workflowJSONBEqual(tx, record.EventJSON, string(raw))
		if err != nil {
			return db.WorkflowEventRecord{}, errors.New("compare duplicate workflow event failed")
		}
		if !same {
			return db.WorkflowEventRecord{}, fmt.Errorf("%w: CloudEvent identity already has different content", ErrConflict)
		}
	}
	if err := a.deliverWorkflowEventTx(tx, record, event, targetWorkflowID, now); err != nil {
		return db.WorkflowEventRecord{}, err
	}
	return record, nil
}

func validateWorkflowCloudEvent(event cloudevents.Event) ([]byte, string, map[string]any, error) {
	if err := event.Validate(); err != nil || event.SpecVersion() != cloudevents.VersionV1 {
		return nil, "", nil, errors.New("CloudEvent must be valid version 1.0")
	}
	_, offset := event.Time().Zone()
	if event.Time().IsZero() || offset != 0 {
		return nil, "", nil, errors.New("CloudEvent time must be UTC")
	}
	if len(event.Source()) > 500 || len(event.ID()) > 128 || len(event.Type()) > 255 || len(event.Subject()) > 500 {
		return nil, "", nil, errors.New("CloudEvent context attribute is too long")
	}
	partitionKey, _ := event.Extensions()["partitionkey"].(string)
	partitionKey = strings.TrimSpace(partitionKey)
	if partitionKey == "" || len(partitionKey) > 256 {
		return nil, "", nil, errors.New("CloudEvent partitionkey must contain 1 to 256 bytes")
	}
	var data map[string]any
	if err := event.DataAs(&data); err != nil || data == nil {
		return nil, "", nil, errors.New("CloudEvent data must be a JSON object")
	}
	raw, err := json.Marshal(event)
	if err != nil || len(raw) > maxWorkflowGraphBytes {
		return nil, "", nil, errors.New("CloudEvent exceeds the 1 MiB limit")
	}
	return raw, partitionKey, data, nil
}

func (a *App) deliverWorkflowEventTx(tx *gorm.DB, record db.WorkflowEventRecord, event cloudevents.Event, targetWorkflowID int64, now time.Time) error {
	query := tx.Where("status = ? AND active_revision_id IS NOT NULL", WorkflowStatusActive)
	if targetWorkflowID > 0 {
		query = query.Where("id = ?", targetWorkflowID)
	} else {
		query = query.Where("mode = ?", WorkflowModeEvent)
	}
	var workflows []db.Workflow
	if err := query.Order("id").Find(&workflows).Error; err != nil {
		return errors.New("load workflow event subscribers failed")
	}
	if targetWorkflowID > 0 && len(workflows) == 0 {
		return fmt.Errorf("%w: target workflow is not running", ErrConflict)
	}
	for _, workflow := range workflows {
		var revision db.WorkflowRevision
		if err := tx.First(&revision, *workflow.ActiveRevisionID).Error; err != nil {
			return errors.New("load workflow event revision failed")
		}
		graph, err := a.buildWorkflowRunGraph(revision.GraphJSON)
		if err != nil {
			return errors.New("load workflow event graph failed")
		}
		trigger := graph.nodes[revision.MainTriggerNodeID]
		if targetWorkflowID == 0 && !workflowEventTriggerMatches(trigger.Config, event) {
			continue
		}
		var existing int64
		if err := tx.Model(&db.WorkflowEventDelivery{}).
			Where("event_record_id = ? AND workflow_id = ?", record.ID, workflow.ID).Count(&existing).Error; err != nil {
			return errors.New("check workflow event delivery failed")
		}
		if existing > 0 {
			continue
		}
		if err := enforceWorkflowBacklog(tx, workflow.ID); err != nil {
			return err
		}
		triggerType := WorkflowModeEvent
		if workflow.Mode == WorkflowModeStream {
			triggerType = WorkflowModeStream
		} else if trigger.NodeType == "official.connector.webhook" {
			triggerType = "webhook"
		} else if event.Type() == workflowFailureEventType {
			triggerType = "failure"
		}
		run := db.WorkflowRun{
			WorkflowID: workflow.ID, RevisionID: revision.ID, TriggerType: triggerType,
			TriggerKey: fmt.Sprint(record.ID), EventRecordID: &record.ID, PartitionKey: record.PartitionKey,
			Status: RunStatusQueued, NotBefore: now, TriggeredAt: event.Time().UTC(), ResultSummary: `{}`, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&run).Error; err != nil {
			return errors.New("create workflow event run failed")
		}
		delivery := db.WorkflowEventDelivery{
			EventRecordID: record.ID, WorkflowID: workflow.ID, RevisionID: revision.ID, RunID: run.ID, CreatedAt: now,
		}
		if err := tx.Create(&delivery).Error; err != nil {
			return errors.New("record workflow event delivery failed")
		}
	}
	return nil
}

func workflowEventTriggerMatches(raw json.RawMessage, event cloudevents.Event) bool {
	var config struct {
		Types   []string `json:"types"`
		Source  string   `json:"source"`
		Subject string   `json:"subject"`
	}
	if json.Unmarshal(raw, &config) != nil || len(config.Types) == 0 {
		return false
	}
	if config.Source != "" && config.Source != event.Source() {
		return false
	}
	if config.Subject != "" && config.Subject != event.Subject() {
		return false
	}
	for _, eventType := range config.Types {
		if eventType == event.Type() {
			return true
		}
	}
	return false
}

func (a *App) enqueueWorkflowEvent(tx *gorm.DB, event cloudevents.Event) error {
	raw, _, _, err := validateWorkflowCloudEvent(event)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	item := db.WorkflowEventOutbox{
		Source: event.Source(), EventID: event.ID(), EventJSON: string(raw), Status: "pending",
		MaxAttempts: 10, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&item)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var existing db.WorkflowEventOutbox
		if err := tx.Where("source = ? AND event_id = ?", event.Source(), event.ID()).First(&existing).Error; err != nil {
			return errors.New("load duplicate workflow event outbox item failed")
		}
		same, err := workflowJSONBEqual(tx, existing.EventJSON, string(raw))
		if err != nil {
			return errors.New("compare duplicate workflow event outbox item failed")
		}
		if !same {
			return fmt.Errorf("%w: CloudEvent outbox identity already has different content", ErrConflict)
		}
	}
	return nil
}

func (a *App) dispatchWorkflowEventOutbox(ctx context.Context, now time.Time) error {
	var record db.WorkflowEventRecord
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item db.WorkflowEventOutbox
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = 'pending' AND available_at <= ?", now).Order("available_at, id").First(&item).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return errors.New("claim workflow event outbox failed")
		}
		item.AttemptCount++
		var event cloudevents.Event
		if err := json.Unmarshal([]byte(item.EventJSON), &event); err == nil {
			record, err = a.persistWorkflowEventTx(tx, event, 0)
			if err == nil {
				return tx.Model(&item).Updates(map[string]any{
					"status": "published", "attempt_count": item.AttemptCount, "published_at": now,
					"last_error_category": nil, "updated_at": now,
				}).Error
			}
		}
		status := "pending"
		if item.AttemptCount >= item.MaxAttempts {
			status = "dead_letter"
		}
		category := "delivery"
		return tx.Model(&item).Updates(map[string]any{
			"status": status, "attempt_count": item.AttemptCount,
			"available_at":        now.Add(time.Duration(item.AttemptCount) * time.Second),
			"last_error_category": category, "updated_at": now,
		}).Error
	})
	if err == nil {
		a.publishWorkflowEventRunUpdates(record.ID)
	}
	return err
}

func newWorkflowFailureEvent(run db.WorkflowRun, category string, now time.Time) cloudevents.Event {
	event := cloudevents.NewEvent()
	event.SetID(fmt.Sprintf("run-%d-failed", run.ID))
	event.SetSource("urn:coinsphere:workflow-core")
	event.SetType(workflowFailureEventType)
	event.SetSubject(fmt.Sprintf("workflow/%d/run/%d", run.WorkflowID, run.ID))
	event.SetTime(now.UTC())
	event.SetExtension("partitionkey", fmt.Sprintf("workflow:%d", run.WorkflowID))
	_ = event.SetData(cloudevents.ApplicationJSON, map[string]any{
		"workflowId": run.WorkflowID, "runId": run.ID, "revisionId": run.RevisionID, "errorCategory": category,
	})
	return event
}

func (a *App) workflowRunEvent(ctx context.Context, run db.WorkflowRun) (cloudevents.Event, map[string]any, error) {
	if run.EventRecordID == nil {
		return cloudevents.Event{}, nil, errors.New("workflow run has no event")
	}
	var record db.WorkflowEventRecord
	if err := a.DB.WithContext(ctx).First(&record, *run.EventRecordID).Error; err != nil {
		return cloudevents.Event{}, nil, errors.New("load workflow run event failed")
	}
	var event cloudevents.Event
	if err := json.Unmarshal([]byte(record.EventJSON), &event); err != nil {
		return cloudevents.Event{}, nil, errors.New("decode workflow run event failed")
	}
	var data map[string]any
	if err := event.DataAs(&data); err != nil || data == nil {
		return cloudevents.Event{}, nil, errors.New("decode workflow run event data failed")
	}
	return event, data, nil
}

func workflowEventContext(event cloudevents.Event) map[string]string {
	partitionKey, _ := event.Extensions()["partitionkey"].(string)
	return map[string]string{
		"id": event.ID(), "source": event.Source(), "type": event.Type(), "subject": event.Subject(),
		"time": event.Time().UTC().Format(time.RFC3339Nano), "partitionkey": strings.TrimSpace(partitionKey),
	}
}

func workflowJSONBEqual(tx *gorm.DB, left, right string) (bool, error) {
	var equal bool
	err := tx.Raw(`SELECT CAST(? AS jsonb) = CAST(? AS jsonb)`, left, right).Scan(&equal).Error
	return equal, err
}

func workflowEventView(record db.WorkflowEventRecord) WorkflowEventView {
	return WorkflowEventView{
		ID: record.ID, Source: record.Source, EventID: record.EventID, Type: record.EventType,
		PartitionKey: record.PartitionKey, ReceivedAt: formatWorkflowTime(record.ReceivedAt),
	}
}
