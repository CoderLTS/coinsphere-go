package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/security"
	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ConnectionPayload struct {
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	Config          map[string]any    `json:"config"`
	Secrets         map[string]string `json:"secrets"`
	Enabled         bool              `json:"enabled"`
	ExpectedVersion int64             `json:"expectedVersion"`
}

type ConnectionView struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	Version      int64           `json:"version"`
	Enabled      bool            `json:"enabled"`
	Config       json.RawMessage `json:"config"`
	SecretFields map[string]bool `json:"secretFields"`
}

func (a *App) connectionDescriptors() map[string]sdk.NodeDescriptor {
	result := map[string]sdk.NodeDescriptor{}
	for _, desc := range a.workflowNodeDescriptors() {
		if desc.ConnectionType != "" {
			result[desc.ConnectionType] = desc
		}
	}
	return result
}

func connectionSchema(desc sdk.NodeDescriptor) json.RawMessage {
	properties, required := workflowSchemaProperties(desc.ConfigSchema)
	selected := map[string]any{}
	keys := []string{}
	for _, field := range desc.ConnectionFields {
		selected[field] = properties[field]
		if required[field] {
			keys = append(keys, field)
		}
	}
	return mustJSON(map[string]any{"type": "object", "properties": selected, "required": keys, "additionalProperties": false})
}

func (a *App) ListConnectionTypes() []M {
	result := []M{}
	for name, desc := range a.connectionDescriptors() {
		result = append(result, M{"type": name, "schema": connectionSchema(desc)})
	}
	sort.Slice(result, func(i, j int) bool { return fmt.Sprint(result[i]["type"]) < fmt.Sprint(result[j]["type"]) })
	return result
}

func (a *App) ListConnections(ctx context.Context) ([]ConnectionView, error) {
	var rows []db.WorkflowConnection
	if err := a.DB.WithContext(ctx).Order("name,id").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]ConnectionView, 0, len(rows))
	for _, row := range rows {
		result = append(result, ConnectionView{ID: row.ID, Name: row.Name, Type: row.Type, Version: row.Version, Enabled: row.Enabled, Config: json.RawMessage(row.ConfigJSON), SecretFields: connectionSecretPresence(row.SecretFieldsJSON)})
	}
	var models []db.AIModelConfig
	if err := a.DB.WithContext(ctx).Order("priority,id").Find(&models).Error; err != nil {
		return nil, err
	}
	for _, model := range models {
		result = append(result, ConnectionView{ID: fmt.Sprintf("ai:%d", model.ID), Name: model.DisplayName, Type: "ai", Version: model.UpdatedAt.UnixNano(), Enabled: model.IsEnabled, Config: mustJSON(aiConnectionConfig(model)), SecretFields: map[string]bool{"apiKey": model.APIKeyCiphertext != ""}})
	}
	proxies, err := a.ListOutboundProxies(ctx)
	if err != nil {
		return nil, err
	}
	for _, proxy := range proxies {
		result = append(result, ConnectionView{ID: fmt.Sprintf("proxy:%d", proxy.ID), Name: proxy.Name, Type: "proxy", Enabled: proxy.IsEnabled, Config: mustJSON(proxy), SecretFields: map[string]bool{"password": proxy.PasswordConfigured}})
	}
	return result, nil
}

func connectionSecretPresence(raw string) map[string]bool {
	result := map[string]bool{}
	_ = json.Unmarshal([]byte(raw), &result)
	return result
}

func aiConnectionConfig(model db.AIModelConfig) map[string]any {
	return map[string]any{"endpoint": strings.TrimRight(model.BaseURL, "/") + "/chat/completions", "model": model.ModelName, "timeoutSeconds": max(1, min(120, model.TimeoutMS/1000))}
}

func (a *App) SaveConnection(ctx context.Context, id string, payload ConnectionPayload) (ConnectionView, error) {
	if payload.Type == "ai" || payload.Type == "proxy" {
		return ConnectionView{}, errors.New("请通过已有模型或代理配置接口管理该连接")
	}
	desc, ok := a.connectionDescriptors()[payload.Type]
	if !ok || strings.TrimSpace(payload.Name) == "" || utf8.RuneCountInString(payload.Name) > 120 {
		return ConnectionView{}, errors.New("连接类型或名称无效")
	}
	if payload.Config == nil {
		payload.Config = map[string]any{}
	}
	schema := connectionSchema(desc)
	secretFields, _ := workflowSecretFields(schema)
	for field := range payload.Config {
		if _, secret := secretFields[field]; secret {
			return ConnectionView{}, errors.New("密钥必须通过 secrets 传入")
		}
	}
	var row db.WorkflowConnection
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		values := map[string]string{}
		if id != "" {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id=?", id).Error; err != nil {
				return err
			}
			if row.Version != payload.ExpectedVersion || row.Type != payload.Type {
				return fmt.Errorf("%w: 连接已修改或类型不一致", ErrConflict)
			}
			plain, err := a.Cipher.Decrypt(row.SecretsCiphertext)
			if err != nil {
				return errors.New("读取连接凭据失败")
			}
			if json.Unmarshal([]byte(plain), &values) != nil {
				return errors.New("连接凭据无效")
			}
		} else {
			row.ID = security.RandomToken()
			row.Type = payload.Type
			row.CreatedAt = time.Now().UTC()
		}
		for field, value := range payload.Secrets {
			if _, ok := secretFields[field]; !ok || len(value) > maxWorkflowSecretBytes {
				return errors.New("连接凭据字段无效")
			}
			if value == "" {
				delete(values, field)
			} else {
				values[field] = value
			}
		}
		complete := map[string]any{}
		for field, value := range payload.Config {
			complete[field] = value
		}
		presence := map[string]bool{}
		for field, value := range values {
			complete[field] = value
			presence[field] = true
		}
		if validateWorkflowSchemaValue(schema, complete) != nil {
			return errors.New("连接配置不符合字段要求")
		}
		encrypted, err := a.Cipher.Encrypt(mustJSONString(values))
		if err != nil {
			return errors.New("加密连接凭据失败")
		}
		row.Name = strings.TrimSpace(payload.Name)
		row.Enabled = payload.Enabled
		row.ConfigJSON = mustJSONString(payload.Config)
		row.SecretsCiphertext = encrypted
		row.SecretFieldsJSON = mustJSONString(presence)
		row.Version++
		row.UpdatedAt = time.Now().UTC()
		return tx.Save(&row).Error
	})
	if err != nil {
		return ConnectionView{}, err
	}
	return ConnectionView{ID: row.ID, Name: row.Name, Type: row.Type, Version: row.Version, Enabled: row.Enabled, Config: json.RawMessage(row.ConfigJSON), SecretFields: connectionSecretPresence(row.SecretFieldsJSON)}, nil
}

func ensureConnectionUnreferenced(tx *gorm.DB, id string) error {
	var count int64
	if err := tx.Model(&db.WorkflowRevision{}).Where("graph_json @> CAST(? AS jsonb)", mustJSONString(M{"nodes": []M{{"connectionId": id}}})).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: 连接被工作流修订引用", ErrConflict)
	}
	if err := tx.Model(&db.WorkflowRunConnection{}).Where("connection_id=?", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: 连接被运行记录引用", ErrConflict)
	}
	return nil
}

func (a *App) DeleteConnection(ctx context.Context, id string) error {
	if strings.Contains(id, ":") {
		return errors.New("请通过已有模型或代理配置入口删除")
	}
	return a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row db.WorkflowConnection
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id=?", id).Error; err != nil {
			return err
		}
		if err := ensureConnectionUnreferenced(tx, id); err != nil {
			return err
		}
		return tx.Delete(&row).Error
	})
}

func (a *App) currentConnection(tx *gorm.DB, id, kind string) (map[string]any, map[string]string, int64, error) {
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
		return aiConnectionConfig(model), map[string]string{"apiKey": secret}, model.UpdatedAt.UnixNano(), nil
	}
	var row db.WorkflowConnection
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

func (a *App) snapshotRunConnections(tx *gorm.DB, run db.WorkflowRun, graph workflowRunGraph) error {
	for _, nodeID := range graph.order {
		node := graph.nodes[nodeID]
		desc := graph.descriptors[nodeID]
		if desc.ConnectionType == "" {
			if node.NodeType == "core.loop" {
				if err := a.snapshotWorkflowLoopConnections(tx, run, node); err != nil {
					return err
				}
			}
			continue
		}
		config, secrets, version, err := a.currentConnection(tx, node.ConnectionID, desc.ConnectionType)
		if err != nil {
			return err
		}
		encrypted, err := a.Cipher.Encrypt(mustJSONString(secrets))
		if err != nil {
			return err
		}
		row := db.WorkflowRunConnection{RunID: run.ID, NodeInstanceID: nodeID, ConnectionID: node.ConnectionID, Version: version, ConfigJSON: mustJSONString(config), SecretsCiphertext: encrypted}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func (a *App) snapshotWorkflowLoopConnections(tx *gorm.DB, run db.WorkflowRun, loopNode workflowGraphNode) error {
	var config workflowLoopConfig
	if err := json.Unmarshal(loopNode.Config, &config); err != nil {
		return errors.New("loop connection configuration is invalid")
	}
	for _, bodyNode := range config.Body.Nodes {
		desc, ok := a.workflowNodeDescriptors()[bodyNode.NodeType]
		if !ok || desc.ConnectionType == "" {
			continue
		}
		id, err := workflowLoopNodeID(loopNode.NodeInstanceID, bodyNode.NodeInstanceID)
		if err != nil {
			return err
		}
		configValues, secrets, version, err := a.currentConnection(tx, bodyNode.ConnectionID, desc.ConnectionType)
		if err != nil {
			return err
		}
		encrypted, err := a.Cipher.Encrypt(mustJSONString(secrets))
		if err != nil {
			return err
		}
		if err := tx.Create(&db.WorkflowRunConnection{RunID: run.ID, NodeInstanceID: id, ConnectionID: bodyNode.ConnectionID, Version: version, ConfigJSON: mustJSONString(configValues), SecretsCiphertext: encrypted}).Error; err != nil {
			return err
		}
	}
	return nil
}

type workflowConnectionSecrets map[string]string

func (s workflowConnectionSecrets) Read(_ context.Context, field string) ([]byte, error) {
	value, ok := s[field]
	if !ok {
		return nil, errors.New("连接缺少所需凭据")
	}
	return []byte(value), nil
}

func (a *App) workflowConnection(ctx context.Context, runID int64, node workflowGraphNode, desc sdk.NodeDescriptor) (json.RawMessage, sdk.SecretReader, error) {
	var config map[string]any
	var secrets map[string]string
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if runID > 0 {
			var row db.WorkflowRunConnection
			if err := tx.First(&row, "run_id=? AND node_instance_id=?", runID, node.NodeInstanceID).Error; err != nil {
				return err
			}
			if row.ConnectionID != node.ConnectionID {
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
		current, values, _, err := a.currentConnection(tx, node.ConnectionID, desc.ConnectionType)
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
	return raw, workflowConnectionSecrets(secrets), nil
}

// 与连接删除共享行锁，使“检查引用”和“保存修订”之间没有悬空引用窗口。
func validateWorkflowConnectionReferences(tx *gorm.DB, graph validatedWorkflowGraph) error {
	ids := make([]string, 0, len(graph.nodes))
	for id := range graph.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return graph.nodes[ids[i]].ConnectionID < graph.nodes[ids[j]].ConnectionID })
	for _, id := range ids {
		node, desc := graph.nodes[id], graph.descriptors[id]
		if node.ConnectionID == "" {
			continue
		}
		if desc.ConnectionType == "" {
			return fmt.Errorf("node %q does not accept a connection", id)
		}
		if err := validateWorkflowConnectionReference(tx, node.ConnectionID, desc.ConnectionType); err != nil {
			return fmt.Errorf("node %q: %w", id, err)
		}
	}
	for loopID, node := range graph.nodes {
		if node.NodeType != "core.loop" {
			continue
		}
		var config workflowLoopConfig
		if err := json.Unmarshal(node.Config, &config); err != nil {
			return fmt.Errorf("loop %q connection configuration is invalid", loopID)
		}
		for _, body := range config.Body.Nodes {
			desc, ok := graph.descriptors[loopID+"."+body.NodeInstanceID]
			if !ok || desc.ConnectionType == "" {
				continue
			}
			if err := validateWorkflowConnectionReference(tx, body.ConnectionID, desc.ConnectionType); err != nil {
				return fmt.Errorf("loop %q node %q: %w", loopID, body.NodeInstanceID, err)
			}
		}
	}
	return nil
}

func validateWorkflowConnectionReference(tx *gorm.DB, id, kind string) error {
	if id == "" {
		return errors.New("connection reference is required")
	}
	if strings.HasPrefix(id, "ai:") && kind == "ai" {
		modelID, err := strconv.ParseInt(strings.TrimPrefix(id, "ai:"), 10, 64)
		if err != nil {
			return errors.New("invalid model connection")
		}
		var model db.AIModelConfig
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Select("id").First(&model, modelID).Error; err != nil {
			return errors.New("model connection unavailable")
		}
		return nil
	}
	var connection db.WorkflowConnection
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Select("id", "type").First(&connection, "id=?", id).Error; err != nil || connection.Type != kind {
		return errors.New("connection type mismatch or connection unavailable")
	}
	return nil
}
