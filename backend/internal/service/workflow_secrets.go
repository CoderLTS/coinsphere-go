package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"gorm.io/gorm"
)

type WorkflowSecretChange struct {
	NodeInstanceID string  `json:"nodeInstanceId"`
	Field          string  `json:"field"`
	Value          *string `json:"value,omitempty"`
	Remove         bool    `json:"remove,omitempty"`
}

type workflowSecretKey struct {
	nodeInstanceID string
	field          string
}

const maxWorkflowSecretBytes = 64 << 10

func validateWorkflowSecretChanges(graph validatedWorkflowGraph, changes []WorkflowSecretChange) (map[workflowSecretKey]WorkflowSecretChange, error) {
	if len(changes) > 256 {
		return nil, errors.New("secretChanges must not exceed 256 items")
	}
	result := make(map[workflowSecretKey]WorkflowSecretChange, len(changes))
	for _, change := range changes {
		key := workflowSecretKey{strings.TrimSpace(change.NodeInstanceID), strings.TrimSpace(change.Field)}
		if !workflowNodeIDPattern.MatchString(key.nodeInstanceID) || key.field == "" || len(key.field) > 128 {
			return nil, errors.New("secret change requires a valid nodeInstanceId and field")
		}
		desc, ok := graph.descriptors[key.nodeInstanceID]
		if !ok {
			return nil, fmt.Errorf("secret change references unknown node %q", key.nodeInstanceID)
		}
		fields, _ := workflowSecretFields(desc.ConfigSchema)
		if _, ok := fields[key.field]; !ok {
			return nil, fmt.Errorf("node %q does not declare secret field %q", key.nodeInstanceID, key.field)
		}
		if _, duplicate := result[key]; duplicate {
			return nil, fmt.Errorf("duplicate secret change for node %q field %q", key.nodeInstanceID, key.field)
		}
		if change.Remove == (change.Value != nil) {
			return nil, fmt.Errorf("secret change for node %q field %q must set value or remove", key.nodeInstanceID, key.field)
		}
		if change.Value != nil && (strings.TrimSpace(*change.Value) == "" || len(*change.Value) > maxWorkflowSecretBytes) {
			return nil, fmt.Errorf("secret change for node %q field %q must contain 1 to %d bytes", key.nodeInstanceID, key.field, maxWorkflowSecretBytes)
		}
		result[key] = change
	}
	return result, nil
}

func (a *App) persistWorkflowSecrets(tx *gorm.DB, workflowID, previousRevisionID int64, revision db.WorkflowRevision, graph validatedWorkflowGraph, changes map[workflowSecretKey]WorkflowSecretChange, now time.Time) error {
	var previousRevision db.WorkflowRevision
	if err := tx.Where("workflow_id = ? AND id = ?", workflowID, previousRevisionID).First(&previousRevision).Error; err != nil {
		return errors.New("load active workflow revision failed")
	}
	previousGraph, err := a.validateWorkflowGraph(json.RawMessage(previousRevision.GraphJSON))
	if err != nil {
		return errors.New("active workflow revision graph is invalid")
	}
	previousTypes := previousGraph.nodeTypes
	for nodeID, nodeType := range graph.nodeTypes {
		if previousType := previousTypes[nodeID]; previousType != "" && previousType != nodeType {
			return fmt.Errorf("%w: node %q cannot change type without a new nodeInstanceId", ErrConflict, nodeID)
		}
	}

	var previous []db.WorkflowSecretBinding
	if err := tx.Where("workflow_id = ? AND revision_id = ?", workflowID, previousRevisionID).Find(&previous).Error; err != nil {
		return errors.New("load active workflow secret bindings failed")
	}
	values := make(map[workflowSecretKey]string, len(previous)+len(changes))
	for _, binding := range previous {
		key := workflowSecretKey{binding.NodeInstanceID, binding.FieldName}
		desc, nodeExists := graph.descriptors[key.nodeInstanceID]
		fields, _ := workflowSecretFields(desc.ConfigSchema)
		if nodeExists && previousTypes[key.nodeInstanceID] == graph.nodeTypes[key.nodeInstanceID] {
			if _, fieldExists := fields[key.field]; fieldExists {
				values[key] = binding.EncryptedValue
			}
		}
	}
	for key, change := range changes {
		if change.Remove {
			delete(values, key)
			continue
		}
		if a.Cipher == nil {
			return errors.New("workflow secret cipher is unavailable")
		}
		values[key] = a.Cipher.Encrypt(*change.Value)
		if values[key] == "" {
			return errors.New("encrypt workflow secret failed")
		}
	}
	for key := range graph.requiredSecrets {
		if values[key] == "" {
			return fmt.Errorf("node %q requires secret field %q", key.nodeInstanceID, key.field)
		}
	}

	keys := make([]workflowSecretKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].nodeInstanceID == keys[j].nodeInstanceID {
			return keys[i].field < keys[j].field
		}
		return keys[i].nodeInstanceID < keys[j].nodeInstanceID
	})
	for _, key := range keys {
		binding := db.WorkflowSecretBinding{
			RevisionID: revision.ID, WorkflowID: workflowID, NodeInstanceID: key.nodeInstanceID,
			FieldName: key.field, EncryptedValue: values[key], CreatedAt: now,
		}
		if err := tx.Create(&binding).Error; err != nil {
			return errors.New("create workflow secret binding failed")
		}
	}
	return nil
}

func ensureWorkflowRevisionSecrets(tx *gorm.DB, workflowID, revisionID int64, graph validatedWorkflowGraph) error {
	if len(graph.requiredSecrets) == 0 {
		return nil
	}
	var bindings []db.WorkflowSecretBinding
	if err := tx.Where("workflow_id = ? AND revision_id = ?", workflowID, revisionID).Find(&bindings).Error; err != nil {
		return errors.New("load workflow secret bindings failed")
	}
	configured := make(map[workflowSecretKey]bool, len(bindings))
	for _, binding := range bindings {
		configured[workflowSecretKey{binding.NodeInstanceID, binding.FieldName}] = true
	}
	for key := range graph.requiredSecrets {
		if !configured[key] {
			return fmt.Errorf("%w: node %q requires secret field %q", ErrConflict, key.nodeInstanceID, key.field)
		}
	}
	return nil
}

func (a *App) workflowRevisionSecretFields(ctx context.Context, workflowID int64, revisionIDs []int64) (map[int64]map[string]map[string]bool, error) {
	result := make(map[int64]map[string]map[string]bool, len(revisionIDs))
	for _, revisionID := range revisionIDs {
		result[revisionID] = map[string]map[string]bool{}
	}
	if len(revisionIDs) == 0 {
		return result, nil
	}
	var bindings []db.WorkflowSecretBinding
	if err := a.DB.WithContext(ctx).Where("workflow_id = ? AND revision_id IN ?", workflowID, revisionIDs).
		Order("revision_id, node_instance_id, field_name").Find(&bindings).Error; err != nil {
		return nil, errors.New("load workflow secret binding status failed")
	}
	for _, binding := range bindings {
		if result[binding.RevisionID][binding.NodeInstanceID] == nil {
			result[binding.RevisionID][binding.NodeInstanceID] = map[string]bool{}
		}
		result[binding.RevisionID][binding.NodeInstanceID][binding.FieldName] = true
	}
	return result, nil
}

func (a *App) attachWorkflowRevisionSecrets(ctx context.Context, workflowID int64, views []WorkflowRevisionView) error {
	ids := make([]int64, len(views))
	for index := range views {
		ids[index] = views[index].ID
	}
	statuses, err := a.workflowRevisionSecretFields(ctx, workflowID, ids)
	if err != nil {
		return err
	}
	for index := range views {
		views[index].SecretFields = statuses[views[index].ID]
	}
	return nil
}
