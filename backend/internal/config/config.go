// Package config 加载 YAML 配置并支持 COINSPHERE_ 前缀环境变量覆盖。
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// 一组相关常量用 const ( ... ) 打包声明。下面几个用于"环境变量覆盖"(见文件末尾 applyEnvOverrides):
// 只处理以 COINSPHERE_ 开头的环境变量,用 __ 表示配置的层级分隔。
const (
	envPrefix     = "COINSPHERE_"
	envPathSep    = "__"
	configPathEnv = envPrefix + "CONFIG_PATH"
)

// DefaultInsecureSecret 仓库自带的占位签名密钥;生产必须覆盖,否则 Validate 拒绝启动(见评审 #1)。
const DefaultInsecureSecret = "coinsphere-dev-secret"

// DatabaseConfig 是 PostgreSQL 连接与连接池配置。
type DatabaseConfig struct {
	DSN                    string `yaml:"dsn"`
	MaxOpenConns           int    `yaml:"max_open_conns"`
	MaxIdleConns           int    `yaml:"max_idle_conns"`
	ConnMaxIdleTimeSeconds int    `yaml:"conn_max_idle_time_seconds"`
}

// ServerConfig HTTP 服务配置。
type ServerConfig struct {
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	PublicBaseURL string `yaml:"public_base_url"`
}

// AuthConfig 认证配置。
type AuthConfig struct {
	SecretKey string `yaml:"secret_key"`
	// EncryptionKey / WebhookPepper 留空时在 normalize 里回落到 SecretKey(向后兼容);
	// 想做密钥分离(见评审 #3)就各配一个独立随机值。
	EncryptionKey           string `yaml:"encryption_key"`
	WebhookPepper           string `yaml:"webhook_pepper"`
	BootstrapAdminPassword  string `yaml:"bootstrap_admin_password"` // 内置超管初始密码,留空默认 coinsphere(见评审 #2)
	AccessTokenTTLMinutes   int    `yaml:"access_token_ttl_minutes"`
	PasswordIterations      int    `yaml:"password_iterations"`
	LoginRateLimitPerMinute int    `yaml:"login_rate_limit_per_minute"` // 每 IP 每分钟登录上限,<=0 用默认 10(见评审 #6)
}

// WorkflowConfig 工作流运行时配置。
type WorkflowConfig struct {
	HTTPAllowedHosts                 []string `yaml:"http_allowed_hosts"`
	ExecutorConcurrency              int      `yaml:"executor_concurrency"`
	HeartbeatIntervalSeconds         int      `yaml:"heartbeat_interval_seconds"`
	ExecutionStaleTimeoutSeconds     int      `yaml:"execution_stale_timeout_seconds"`
	PollIntervalMs                   int      `yaml:"poll_interval_ms"`
	OutboxPollIntervalMs             int      `yaml:"outbox_poll_interval_ms"`
	OutboxBatchSize                  int      `yaml:"outbox_batch_size"`
	OutboxLeaseSeconds               int      `yaml:"outbox_lease_seconds"`
	ExecutionRetentionDays           int      `yaml:"execution_retention_days"`
	RetentionDeleteBatchSize         int      `yaml:"retention_delete_batch_size"`
	MaxInputSnapshotBytes            int      `yaml:"max_input_snapshot_bytes"`
	MaxOutputSnapshotBytes           int      `yaml:"max_output_snapshot_bytes"`
	ScheduleReconcileIntervalSeconds int      `yaml:"schedule_reconcile_interval_seconds"`
	StaleRecoveryIntervalSeconds     int      `yaml:"stale_recovery_interval_seconds"`
	BacklogLimitPerKey               int      `yaml:"backlog_limit_per_key"`
	SemaphoreLimitPerKey             int      `yaml:"semaphore_limit_per_key"`
	MaxAttempts                      int      `yaml:"max_attempts"`
	RetryBackoffSeconds              []int    `yaml:"retry_backoff_seconds"`
	// GraphNodeConcurrency 单张(子)图内同时执行的节点数上限,<=0 时 normalize 补成 8。
	GraphNodeConcurrency int `yaml:"graph_node_concurrency"`
	// DisableNodeInputSnapshot 关闭"每进一个节点就把整张共享状态序列化落库"。
	// 大图 / foreach 多元素时这项写入成本很高;关掉后仍保留节点的输出快照与失败信息。
	DisableNodeInputSnapshot bool `yaml:"disable_node_input_snapshot"`
}

// AssistantConfig 智能助手运行配置。
type AssistantConfig struct {
	// HistoryMaxMessages 每次调用模型时最多携带多少条历史消息。
	// 不设上限的话,会话越长每次请求越贵,最终会撞上模型的上下文窗口。<=1 时 normalize 补成 40。
	HistoryMaxMessages int `yaml:"history_max_messages"`
	// AgentNodeTimeoutMs 工作流里 assistant.agent 节点单次调用的超时(毫秒)。<=0 时补成 120000。
	AgentNodeTimeoutMs int `yaml:"agent_node_timeout_ms"`
}

// LogConfig 日志配置。
type LogConfig struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}

// AppConfig 完整配置树。
type AppConfig struct {
	Database  DatabaseConfig  `yaml:"database"`
	Server    ServerConfig    `yaml:"server"`
	Auth      AuthConfig      `yaml:"auth"`
	Workflow  WorkflowConfig  `yaml:"workflow"`
	Assistant AssistantConfig `yaml:"assistant"`
	Log       LogConfig       `yaml:"log"`
}

// Load 读取配置文件并应用环境变量覆盖。
// 返回 *AppConfig(指针):只回传配置树的"地址",避免复制整棵结构;第二个返回值 error 报告是否失败。
func Load(path string) (*AppConfig, error) {
	// 路径优先级:命令行 -config > 环境变量 COINSPHERE_CONFIG_PATH > 默认 config.yml。
	if path == "" {
		path = strings.TrimSpace(os.Getenv(configPathEnv))
	}
	if path == "" {
		path = "config.yml"
	}
	// os.ReadFile 一次性把文件读成字节切片 []byte。读失败就用 %w 把原始错误"包裹"后往上抛,
	// 保留错误链(见 GO入门笔记『错误处理』)。
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	// 先把 YAML 解析成通用的 map[string]any(键是字符串、值是任意类型),方便逐项做环境变量覆盖。
	var tree map[string]any
	if err := yaml.Unmarshal(raw, &tree); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if tree == nil {
		tree = map[string]any{}
	}
	applyEnvOverrides(tree)

	// 覆盖完再 Marshal 回字节、Unmarshal 进强类型的 cfg:先填默认值,配置里写了的项才覆盖掉默认。
	merged, err := yaml.Marshal(tree)
	if err != nil {
		return nil, err
	}
	cfg := defaultConfig()
	if err := yaml.Unmarshal(merged, cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	cfg.normalize()
	return cfg, nil
}

// defaultConfig 返回内置默认配置;配置文件与环境变量只需覆盖想改的项,其余保持这里的默认值。
func defaultConfig() *AppConfig {
	return &AppConfig{
		Database: DatabaseConfig{
			MaxOpenConns:           40,
			MaxIdleConns:           10,
			ConnMaxIdleTimeSeconds: 300,
		},
		Server: ServerConfig{Host: "0.0.0.0", Port: 6987},
		Auth: AuthConfig{
			SecretKey:             DefaultInsecureSecret,
			AccessTokenTTLMinutes: 15,
			PasswordIterations:    390000,
		},
		Workflow: WorkflowConfig{
			ExecutorConcurrency:              4,
			HeartbeatIntervalSeconds:         10,
			ExecutionStaleTimeoutSeconds:     900,
			PollIntervalMs:                   1000,
			OutboxPollIntervalMs:             1000,
			OutboxBatchSize:                  100,
			OutboxLeaseSeconds:               30,
			ExecutionRetentionDays:           30,
			RetentionDeleteBatchSize:         1000,
			MaxInputSnapshotBytes:            65536,
			MaxOutputSnapshotBytes:           131072,
			ScheduleReconcileIntervalSeconds: 30,
			StaleRecoveryIntervalSeconds:     15,
			BacklogLimitPerKey:               1000,
			SemaphoreLimitPerKey:             1,
			MaxAttempts:                      4,
			RetryBackoffSeconds:              []int{30, 120, 600},
			GraphNodeConcurrency:             8,
		},
		Assistant: AssistantConfig{
			HistoryMaxMessages: 40,
			AgentNodeTimeoutMs: 120000,
		},
		Log: LogConfig{Level: "info"},
	}
}

// normalize 对读入的配置做规范化与兜底(补默认值、统一大小写、修正路径等)。
// (c *AppConfig) 是"指针接收者":表示这是 AppConfig 的方法,方法内可直接修改 c 指向的配置对象。
func (c *AppConfig) normalize() {
	if c.Server.PublicBaseURL == "" {
		c.Server.PublicBaseURL = fmt.Sprintf("http://127.0.0.1:%d", c.Server.Port)
	}
	c.Server.PublicBaseURL = strings.TrimRight(c.Server.PublicBaseURL, "/")
	c.Database.DSN = strings.TrimSpace(c.Database.DSN)
	if c.Database.MaxOpenConns < 1 {
		c.Database.MaxOpenConns = 40
	}
	if c.Database.MaxIdleConns < 1 {
		c.Database.MaxIdleConns = 10
	}
	if c.Database.ConnMaxIdleTimeSeconds < 1 {
		c.Database.ConnMaxIdleTimeSeconds = 300
	}
	if len(c.Workflow.RetryBackoffSeconds) == 0 {
		c.Workflow.RetryBackoffSeconds = []int{30, 120, 600}
	}
	if c.Workflow.ExecutorConcurrency < 1 {
		c.Workflow.ExecutorConcurrency = 1
	}
	if c.Workflow.MaxAttempts < 1 {
		c.Workflow.MaxAttempts = 1
	}
	if c.Workflow.OutboxLeaseSeconds < 1 {
		c.Workflow.OutboxLeaseSeconds = 30
	}
	if c.Workflow.GraphNodeConcurrency < 1 {
		c.Workflow.GraphNodeConcurrency = 8
	}
	if c.Assistant.HistoryMaxMessages <= 1 {
		c.Assistant.HistoryMaxMessages = 40
	}
	if c.Assistant.AgentNodeTimeoutMs <= 0 {
		c.Assistant.AgentNodeTimeoutMs = 120000
	}
	// 认证兜底:加密密钥 / webhook pepper 留空时回落到签名密钥(向后兼容),其余给默认值。
	if c.Auth.EncryptionKey == "" {
		c.Auth.EncryptionKey = c.Auth.SecretKey
	}
	if c.Auth.WebhookPepper == "" {
		c.Auth.WebhookPepper = c.Auth.SecretKey
	}
	if c.Auth.BootstrapAdminPassword == "" {
		c.Auth.BootstrapAdminPassword = "coinsphere"
	}
	if c.Auth.LoginRateLimitPerMinute <= 0 {
		c.Auth.LoginRateLimitPerMinute = 10
	}
}

// Validate 启动前安全校验:签名密钥必须显式配置且不是仓库默认值,否则任何人可用公开默认密钥伪造登录令牌(评审 #1)。
// 本地开发想临时用默认值,设环境变量 COINSPHERE_ALLOW_INSECURE_SECRET=1 放行。
func (c *AppConfig) Validate() error {
	if c.Auth.SecretKey == "" || c.Auth.SecretKey == DefaultInsecureSecret {
		if os.Getenv("COINSPHERE_ALLOW_INSECURE_SECRET") != "1" {
			return fmt.Errorf("auth.secret_key 未设置或仍为默认值,存在令牌伪造风险:请用环境变量 COINSPHERE_AUTH__SECRET_KEY 或 config.yml 配一个随机密钥(如 `openssl rand -hex 32`);本地开发可临时设 COINSPHERE_ALLOW_INSECURE_SECRET=1 放行")
		}
	}
	return nil
}

// applyEnvOverrides 把 COINSPHERE_A__B=value 写入嵌套配置树。
func applyEnvOverrides(tree map[string]any) {
	// 遍历所有环境变量。os.Environ() 返回 "KEY=VALUE" 字符串切片,range 逐个取出(第一个返回值下标用 _ 丢弃)。
	for _, kv := range os.Environ() {
		// 用第一个 = 把 "KEY=VALUE" 切成 name/value;只处理带 COINSPHERE_ 前缀、且不是 CONFIG_PATH 的变量。
		name, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, envPrefix) || name == configPathEnv {
			continue
		}
		// 去掉前缀后按 __ 分段:COINSPHERE_DATABASE__PORT → ["database","port"],对应配置树里的层级路径。
		parts := strings.Split(strings.ToLower(name[len(envPrefix):]), strings.ToLower(envPathSep))
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		// 顺着 parts 一层层往里走,中间层不存在就补一个空 map;最后一段留到循环外单独赋值。
		cursor := tree
		for _, part := range parts[:len(parts)-1] {
			next, isMap := cursor[part].(map[string]any)
			if !isMap {
				next = map[string]any{}
				cursor[part] = next
			}
			cursor = next
		}
		// 尝试把字符串值按 YAML 解析(让 "true"/"123" 变成布尔/数字);解析不了就当普通字符串。
		var parsed any
		if err := yaml.Unmarshal([]byte(value), &parsed); err != nil || parsed == nil {
			parsed = value
		}
		cursor[parts[len(parts)-1]] = parsed
	}
}
