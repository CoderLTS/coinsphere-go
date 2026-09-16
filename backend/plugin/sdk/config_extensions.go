package sdk

import (
	"encoding/json"
	"fmt"
	"time"
)

// ConfigLevel 节点配置层级
type ConfigLevel string

const (
	ConfigLevelBasic    ConfigLevel = "basic"    // 基础：3-5个核心参数
	ConfigLevelAdvanced ConfigLevel = "advanced" // 高级：增加可选参数
	ConfigLevelExpert   ConfigLevel = "expert"   // 专家：完整配置
)

// LeveledConfigSchema 支持分层的配置 Schema
type LeveledConfigSchema struct {
	Basic    json.RawMessage `json:"basic"`    // 基础层 Schema
	Advanced json.RawMessage `json:"advanced"` // 高级层 Schema
	Expert   json.RawMessage `json:"expert"`   // 专家层 Schema（完整）
}

// NodeConfigTemplate 节点配置模板
type NodeConfigTemplate struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Category    string          `json:"category"`
	NodeType    string          `json:"nodeType"`
	Level       ConfigLevel     `json:"level"`
	Preset      json.RawMessage `json:"preset"`
	Tags        []string        `json:"tags"`
	IsBuiltin   bool            `json:"isBuiltin"`
	CreatedBy   string          `json:"createdBy,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

// ParameterGroup 参数分组定义
type ParameterGroup struct {
	ID          string   `json:"id"`          // 分组 ID
	Title       string   `json:"title"`       // 分组标题
	Description string   `json:"description"` // 分组说明
	Icon        string   `json:"icon"`        // 图标
	Order       int      `json:"order"`       // 排序
	Collapsible bool     `json:"collapsible"` // 是否可折叠
	Collapsed   bool     `json:"collapsed"`   // 默认折叠状态
	Fields      []string `json:"fields"`      // 包含的字段名
}

// WorkflowContext 工作流执行上下文（扩展）
type WorkflowContext interface {
	// 原有方法
	GetSecret(key string) (string, error)
	LogInfo(message string, fields map[string]interface{})
	LogError(message string, err error, fields map[string]interface{})

	// 缓存管理
	GetCache(key string) (interface{}, bool)
	SetCache(key string, value interface{})
	SetCacheWithTTL(key string, value interface{}, ttl time.Duration)
	ClearCache()

	// K线查询（带缓存）
	LoadCandles(venue string, query CandleQuery) ([]Candle, error)
	LoadCandlesBatch(queries []CandleQueryWithVenue) (map[string][]Candle, error)
}

// CandleQueryWithVenue 带交易所信息的 K 线查询
type CandleQueryWithVenue struct {
	Venue string
	CandleQuery
}

// CacheKey 返回缓存键
func (q CandleQueryWithVenue) CacheKey() string {
	return fmt.Sprintf("candles:%s:%s:%s:%s:%d", q.Venue, q.Market, q.Instrument, q.Interval, q.Limit)
}

// ConfigTemplateRegistry 配置模板注册表
type ConfigTemplateRegistry interface {
	RegisterTemplate(template NodeConfigTemplate) error
	GetTemplate(templateID string) (NodeConfigTemplate, error)
	ListTemplates(nodeType string, level ConfigLevel) ([]NodeConfigTemplate, error)
	ListAllTemplates() ([]NodeConfigTemplate, error)
}
