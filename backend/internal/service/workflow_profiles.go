package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func aiProfileConfig(model db.AIModelConfig) map[string]any {
	return map[string]any{"endpoint": strings.TrimRight(model.BaseURL, "/") + "/chat/completions", "model": model.ModelName, "timeoutSeconds": max(1, min(120, model.TimeoutMS/1000))}
}

func ensureProfileSnapshotUnreferenced(tx *gorm.DB, id string) error {
	var count int64
	if err := tx.Model(&db.WorkflowRevision{}).Where("graph_json @> CAST(? AS jsonb)", mustJSONString(M{"nodes": []M{{"profileId": id}}})).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: profile is referenced by a workflow revision", ErrConflict)
	}
	if err := tx.Model(&db.WorkflowRunProfileSnapshot{}).Where("profile_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: profile is referenced by a run snapshot", ErrConflict)
	}
	return nil
}

func (a *App) currentProfileSnapshot(tx *gorm.DB, id, kind string) (map[string]any, map[string]string, int64, error) {
	if strings.HasPrefix(id, "ai:") && kind == "ai" {
		modelID, err := strconv.ParseInt(strings.TrimPrefix(id, "ai:"), 10, 64)
		if err != nil {
			return nil, nil, 0, errors.New("模型连接 ID 无效")
		}
		var model db.AIModelConfig
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&model, modelID).Error; err != nil {
			return nil, nil, 0, err
		}
		if !model.IsEnabled {
			return nil, nil, 0, errors.New("模型连接已停用")
		}
		secret, err := a.Cipher.Decrypt(model.APIKeyCiphertext)
		if err != nil {
			return nil, nil, 0, errors.New("读取模型连接失败")
		}
		return aiProfileConfig(model), map[string]string{"apiKey": secret}, model.UpdatedAt.UnixNano(), nil
	}
	var row db.WorkflowProfileSnapshot
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&row, "id=?", id).Error; err != nil {
		return nil, nil, 0, err
	}
	if row.Type != kind || !row.Enabled {
		return nil, nil, 0, errors.New("连接类型不匹配或连接已停用")
	}
	config, err := decodeJSONObject(json.RawMessage(row.ConfigJSON))
	if err != nil {
		return nil, nil, 0, err
	}
	plain, err := a.Cipher.Decrypt(row.SecretsCiphertext)
	if err != nil {
		return nil, nil, 0, errors.New("读取连接凭据失败")
	}
	values := map[string]string{}
	if err := json.Unmarshal([]byte(plain), &values); err != nil {
		return nil, nil, 0, err
	}
	return config, values, row.Version, nil
}

func (a *App) snapshotRunProfiles(tx *gorm.DB, run db.WorkflowRun, graph workflowRunGraph) error {
	for _, nodeID := range graph.order {
		node := graph.nodes[nodeID]
		desc := graph.descriptors[nodeID]
		if desc.ProfileType == "" {
			if node.NodeType == "core.loop" {
				if err := a.snapshotWorkflowLoopProfiles(tx, run, node); err != nil {
					return err
				}
			}
			continue
		}
		config, secrets, version, err := a.currentProfileSnapshot(tx, node.ProfileID, desc.ProfileType)
		if err != nil {
			return err
		}
		encrypted := a.Cipher.Encrypt(mustJSONString(secrets))
		row := db.WorkflowRunProfileSnapshot{RunID: run.ID, NodeInstanceID: nodeID, ProfileID: node.ProfileID, Version: version, ConfigJSON: mustJSONString(config), SecretsCiphertext: encrypted}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func (a *App) snapshotWorkflowLoopProfiles(tx *gorm.DB, run db.WorkflowRun, loopNode workflowGraphNode) error {
	var config workflowLoopConfig
	if err := json.Unmarshal(loopNode.Config, &config); err != nil {
		return errors.New("loop profile configuration is invalid")
	}
	for _, bodyNode := range config.Body.Nodes {
		desc, ok := a.workflowNodeDescriptors()[bodyNode.NodeType]
		if !ok || desc.ProfileType == "" {
			continue
		}
		id, err := workflowLoopNodeID(loopNode.NodeInstanceID, bodyNode.NodeInstanceID)
		if err != nil {
			return err
		}
		configValues, secrets, version, err := a.currentProfileSnapshot(tx, bodyNode.ProfileID, desc.ProfileType)
		if err != nil {
			return err
		}
		encrypted := a.Cipher.Encrypt(mustJSONString(secrets))
		if err := tx.Create(&db.WorkflowRunProfileSnapshot{RunID: run.ID, NodeInstanceID: id, ProfileID: bodyNode.ProfileID, Version: version, ConfigJSON: mustJSONString(configValues), SecretsCiphertext: encrypted}).Error; err != nil {
			return err
		}
	}
	return nil
}

type workflowProfileSecrets map[string]string

func (s workflowProfileSecrets) Read(_ context.Context, field string) ([]byte, error) {
	value, ok := s[field]
	if !ok {
		return nil, errors.New("连接缺少所需凭据")
	}
	return []byte(value), nil
}

func (a *App) workflowProfileSnapshot(ctx context.Context, runID int64, node workflowGraphNode, desc sdk.NodeDescriptor) (json.RawMessage, sdk.SecretReader, error) {
	var config map[string]any
	var secrets map[string]string
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if runID > 0 {
			var row db.WorkflowRunProfileSnapshot
			if err := tx.First(&row, "run_id=? AND node_instance_id=?", runID, node.NodeInstanceID).Error; err != nil {
				return err
			}
			if row.ProfileID != node.ProfileID {
				return errors.New("运行连接快照不匹配")
			}
			config, secrets = nil, nil
			if json.Unmarshal([]byte(row.ConfigJSON), &config) != nil {
				return errors.New("连接快照无效")
			}
			plain, err := a.Cipher.Decrypt(row.SecretsCiphertext)
			if err != nil {
				return errors.New("读取连接快照失败")
			}
			if json.Unmarshal([]byte(plain), &secrets) != nil {
				return errors.New("连接凭据快照无效")
			}
			return nil
		}
		current, values, _, err := a.currentProfileSnapshot(tx, node.ProfileID, desc.ProfileType)
		if err != nil {
			return err
		}
		config, secrets = current, values
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	local, err := decodeJSONObject(node.Config)
	if err != nil {
		return nil, nil, err
	}
	for field, value := range config {
		if _, exists := local[field]; exists {
			return nil, nil, errors.New("节点配置不得覆盖连接字段")
		}
		local[field] = value
	}
	complete := map[string]any{}
	for field, value := range local {
		complete[field] = value
	}
	for field, value := range secrets {
		complete[field] = value
	}
	if validateWorkflowSchemaValue(desc.ConfigSchema, complete) != nil {
		return nil, nil, errors.New("有效节点配置不符合插件要求")
	}
	raw := mustJSON(local)
	if desc.ValidateConfig != nil {
		if err := desc.ValidateConfig(raw); err != nil {
			return nil, nil, err
		}
	}
	return raw, workflowProfileSecrets(secrets), nil
}

// 与连接删除共享行锁，使“检查引用”和“保存修订”之间没有悬空引用窗口。
func validateWorkflowProfileSnapshotReferences(tx *gorm.DB, graph validatedWorkflowGraph) error {
	ids := make([]string, 0, len(graph.nodes))
	for id := range graph.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return graph.nodes[ids[i]].ProfileID < graph.nodes[ids[j]].ProfileID })
	for _, id := range ids {
		node, desc := graph.nodes[id], graph.descriptors[id]
		if node.ProfileID == "" {
			continue
		}
		if desc.ProfileType == "" {
			return fmt.Errorf("node %q does not accept a profile", id)
		}
		if err := validateWorkflowProfileSnapshotReference(tx, node.ProfileID, desc.ProfileType); err != nil {
			return fmt.Errorf("node %q: %w", id, err)
		}
	}
	for loopID, node := range graph.nodes {
		if node.NodeType != "core.loop" {
			continue
		}
		var config workflowLoopConfig
		if err := json.Unmarshal(node.Config, &config); err != nil {
			return fmt.Errorf("loop %q profile configuration is invalid", loopID)
		}
		for _, body := range config.Body.Nodes {
			desc, ok := graph.descriptors[loopID+"."+body.NodeInstanceID]
			if !ok || desc.ProfileType == "" {
				continue
			}
			if err := validateWorkflowProfileSnapshotReference(tx, body.ProfileID, desc.ProfileType); err != nil {
				return fmt.Errorf("loop %q node %q: %w", loopID, body.NodeInstanceID, err)
			}
		}
	}
	return nil
}

func validateWorkflowProfileSnapshotReference(tx *gorm.DB, id, kind string) error {
	if id == "" {
		return errors.New("profile reference is required")
	}
	if strings.HasPrefix(id, "ai:") && kind == "ai" {
		modelID, err := strconv.ParseInt(strings.TrimPrefix(id, "ai:"), 10, 64)
		if err != nil {
			return errors.New("invalid model profile")
		}
		var model db.AIModelConfig
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Select("id").First(&model, modelID).Error; err != nil {
			return errors.New("model profile unavailable")
		}
		return nil
	}
	var profile db.WorkflowProfileSnapshot
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Select("id", "type").First(&profile, "id=?", id).Error; err != nil || profile.Type != kind {
		return errors.New("profile type mismatch or profile unavailable")
	}
	return nil
}
