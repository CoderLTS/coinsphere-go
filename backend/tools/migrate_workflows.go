package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// WorkflowMigration 工作流迁移工具
// 将旧版工作流节点迁移到新版统一指标节点

type Workflow struct {
	ID        string          `gorm:"primaryKey;type:varchar(128)"`
	Name      string          `gorm:"type:varchar(255);not null"`
	Graph     json.RawMessage `gorm:"type:jsonb;not null"`
	UpdatedAt time.Time
}

type WorkflowGraph struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Nodes         []map[string]interface{} `json:"nodes"`
	Edges         []map[string]interface{} `json:"edges"`
}

func main() {
	// 从环境变量读取数据库配置
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "host=localhost user=postgres password=postgres dbname=coinsphere port=5432 sslmode=disable"
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogLevel(logger.Info),
	})
	if err != nil {
		log.Fatalf("Failed to connect database: %v", err)
	}

	// 获取所有工作流
	var workflows []Workflow
	if err := db.Find(&workflows).Error; err != nil {
		log.Fatalf("Failed to query workflows: %v", err)
	}

	log.Printf("Found %d workflows to process", len(workflows))

	migrated := 0
	skipped := 0
	failed := 0

	for _, wf := range workflows {
		log.Printf("\n=== Processing workflow: %s (%s) ===", wf.Name, wf.ID)

		var graph WorkflowGraph
		if err := json.Unmarshal(wf.Graph, &graph); err != nil {
			log.Printf("  ERROR: Failed to parse graph: %v", err)
			failed++
			continue
		}

		// 检查是否需要迁移
		needsMigration := false
		for _, node := range graph.Nodes {
			nodeType, _ := node["type"].(string)
			if isOldIndicatorNode(nodeType) {
				needsMigration = true
				break
			}
		}

		if !needsMigration {
			log.Printf("  SKIP: No old indicator nodes found")
			skipped++
			continue
		}

		// 执行迁移
		if err := migrateWorkflow(db, &wf, &graph); err != nil {
			log.Printf("  ERROR: Migration failed: %v", err)
			failed++
		} else {
			log.Printf("  SUCCESS: Workflow migrated")
			migrated++
		}
	}

	log.Printf("\n=== Migration Summary ===")
	log.Printf("Total workflows: %d", len(workflows))
	log.Printf("Migrated: %d", migrated)
	log.Printf("Skipped: %d", skipped)
	log.Printf("Failed: %d", failed)
}

func isOldIndicatorNode(nodeType string) bool {
	oldIndicators := []string{
		"official.quant.volume_spike_condition",
		"official.quant.price_change_condition",
		"official.quant.macd_condition",
		"official.quant.kdj_condition",
		"official.quant.rsi_condition",
		"official.quant.bollinger_condition",
	}

	for _, old := range oldIndicators {
		if nodeType == old {
			return true
		}
	}
	return false
}

func migrateWorkflow(db *gorm.DB, wf *Workflow, graph *WorkflowGraph) error {
	// 更新 schema version
	graph.SchemaVersion = 2

	// 迁移节点
	for i := range graph.Nodes {
		node := graph.Nodes[i]
		nodeType, _ := node["type"].(string)

		if !isOldIndicatorNode(nodeType) {
			continue
		}

		log.Printf("  Migrating node: %s (%s)", node["id"], nodeType)

		// 转换配置
		config, ok := node["config"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("invalid node config for node %s", node["id"])
		}

		newConfig, err := convertIndicatorConfig(nodeType, config)
		if err != nil {
			return fmt.Errorf("failed to convert config for node %s: %w", node["id"], err)
		}

		// 更新节点类型和配置
		node["type"] = "official.quant.unified_indicator"
		node["config"] = newConfig

		graph.Nodes[i] = node
	}

	// 序列化并保存
	newGraph, err := json.Marshal(graph)
	if err != nil {
		return fmt.Errorf("failed to marshal graph: %w", err)
	}

	wf.Graph = newGraph
	if err := db.Save(wf).Error; err != nil {
		return fmt.Errorf("failed to save workflow: %w", err)
	}

	// 记录迁移日志
	logEntry := map[string]interface{}{
		"workflow_id":   wf.ID,
		"workflow_name": wf.Name,
		"migrated_at":   time.Now(),
		"schema_from":   1,
		"schema_to":     2,
	}
	logJSON, _ := json.Marshal(logEntry)

	if err := db.Exec("INSERT INTO workflow_migration_logs (id, workflow_id, old_version, new_version, changes, migrated_at) VALUES (gen_random_uuid(), ?, 1, 2, ?, NOW())",
		wf.ID, string(logJSON)).Error; err != nil {
		log.Printf("  WARNING: Failed to write migration log: %v", err)
	}

	return nil
}

func convertIndicatorConfig(oldType string, oldConfig map[string]interface{}) (map[string]interface{}, error) {
	// 提取通用字段
	venue, _ := oldConfig["venue"].(string)
	market, _ := oldConfig["market"].(string)
	instrument, _ := oldConfig["instrument"].(string)
	interval, _ := oldConfig["interval"].(string)
	checkInterval, _ := oldConfig["checkInterval"].(string)
	name, _ := oldConfig["name"].(string)
	params, _ := oldConfig["parameters"].(map[string]interface{})

	if venue == "" {
		venue = "binance"
	}

	// 确定指标类型
	indicatorType := ""
	switch oldType {
	case "official.quant.volume_spike_condition":
		indicatorType = "volume_spike"
	case "official.quant.price_change_condition":
		indicatorType = "price_change"
	case "official.quant.macd_condition":
		indicatorType = "macd"
	case "official.quant.kdj_condition":
		indicatorType = "kdj"
	case "official.quant.rsi_condition":
		indicatorType = "rsi"
	case "official.quant.bollinger_condition":
		indicatorType = "bollinger"
	default:
		return nil, fmt.Errorf("unknown indicator type: %s", oldType)
	}

	// 构建新配置
	newConfig := map[string]interface{}{
		"dataSource": map[string]interface{}{
			"mode":       "query",
			"venue":      venue,
			"market":     market,
			"instrument": instrument,
			"interval":   interval,
		},
		"monitoring": map[string]interface{}{
			"checkInterval": checkInterval,
			"name":          name,
		},
		"indicator": map[string]interface{}{
			"type":       indicatorType,
			"parameters": params,
		},
		"condition": map[string]interface{}{
			"mode": "auto",
		},
	}

	return newConfig, nil
}
