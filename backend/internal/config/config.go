// Package config 加载 YAML 配置并支持 COINSPHERE_ 前缀环境变量覆盖。
package config

import (
	"fmt"
	"os"
	"path/filepath"
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

// DatabaseConfig 数据库连接配置,driver 决定方言。
type DatabaseConfig struct {
	// struct(结构体)= 一组字段打包;字段后反引号里的 `yaml:"driver"` 是 struct tag(标签),
	// 告诉 YAML 库:配置文件里的 driver: 这一项对应本字段(见 GO入门笔记『框架:YAML』)。
	// 字段首字母大写才能被别的包读写(见 GO入门笔记『可见性』),所以这里字段都是大写开头。
	Driver   string `yaml:"driver"` // sqlite | mysql | postgres
	Path     string `yaml:"path"`   // sqlite 文件路径
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
	Schema   string `yaml:"schema"` // 仅 postgres
	Params   string `yaml:"params"` // 追加到 DSN 的额外参数
}

// ServerConfig HTTP 服务配置。
type ServerConfig struct {
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	PublicBaseURL string `yaml:"public_base_url"`
}

// AuthConfig 认证配置。
type AuthConfig struct {
	SecretKey             string `yaml:"secret_key"`
	AccessTokenTTLMinutes int    `yaml:"access_token_ttl_minutes"`
	RefreshTokenTTLDays   int    `yaml:"refresh_token_ttl_days"`
	PasswordIterations    int    `yaml:"password_iterations"`
}

// WorkflowConfig 工作流运行时配置。
type WorkflowConfig struct {
	ExecutorConcurrency              int   `yaml:"executor_concurrency"`
	HeartbeatIntervalSeconds         int   `yaml:"heartbeat_interval_seconds"`
	ExecutionStaleTimeoutSeconds     int   `yaml:"execution_stale_timeout_seconds"`
	PollIntervalMs                   int   `yaml:"poll_interval_ms"`
	OutboxPollIntervalMs             int   `yaml:"outbox_poll_interval_ms"`
	OutboxBatchSize                  int   `yaml:"outbox_batch_size"`
	ExecutionRetentionDays           int   `yaml:"execution_retention_days"`
	RetentionDeleteBatchSize         int   `yaml:"retention_delete_batch_size"`
	MaxInputSnapshotBytes            int   `yaml:"max_input_snapshot_bytes"`
	MaxOutputSnapshotBytes           int   `yaml:"max_output_snapshot_bytes"`
	ScheduleReconcileIntervalSeconds int   `yaml:"schedule_reconcile_interval_seconds"`
	StaleRecoveryIntervalSeconds     int   `yaml:"stale_recovery_interval_seconds"`
	BacklogLimitPerKey               int   `yaml:"backlog_limit_per_key"`
	SemaphoreLimitPerKey             int   `yaml:"semaphore_limit_per_key"`
	MaxAttempts                      int   `yaml:"max_attempts"`
	RetryBackoffSeconds              []int `yaml:"retry_backoff_seconds"`
}

// LogConfig 日志配置。
type LogConfig struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}

// AppConfig 完整配置树。
type AppConfig struct {
	Database DatabaseConfig `yaml:"database"`
	Server   ServerConfig   `yaml:"server"`
	Auth     AuthConfig     `yaml:"auth"`
	Workflow WorkflowConfig `yaml:"workflow"`
	Log      LogConfig      `yaml:"log"`
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
	// normalize 收尾修正。filepath.Dir(path) 取配置文件所在目录,用于把相对的 sqlite 路径补成绝对路径。
	cfg.normalize(filepath.Dir(path))
	return cfg, nil
}

// defaultConfig 返回内置默认配置;配置文件与环境变量只需覆盖想改的项,其余保持这里的默认值。
func defaultConfig() *AppConfig {
	return &AppConfig{
		Database: DatabaseConfig{Driver: "sqlite", Path: "data/coinsphere.db", Schema: "coinsphere"},
		Server:   ServerConfig{Host: "0.0.0.0", Port: 6987},
		Auth: AuthConfig{
			SecretKey:             "coinsphere-dev-secret",
			AccessTokenTTLMinutes: 1440,
			RefreshTokenTTLDays:   7,
			PasswordIterations:    390000,
		},
		Workflow: WorkflowConfig{
			ExecutorConcurrency:              4,
			HeartbeatIntervalSeconds:         10,
			ExecutionStaleTimeoutSeconds:     900,
			PollIntervalMs:                   1000,
			OutboxPollIntervalMs:             1000,
			OutboxBatchSize:                  100,
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
		},
		Log: LogConfig{Level: "info"},
	}
}

// normalize 对读入的配置做规范化与兜底(补默认值、统一大小写、修正路径等)。
// (c *AppConfig) 是"指针接收者":表示这是 AppConfig 的方法,方法内可直接修改 c 指向的配置对象。
func (c *AppConfig) normalize(baseDir string) {
	if c.Server.PublicBaseURL == "" {
		c.Server.PublicBaseURL = fmt.Sprintf("http://127.0.0.1:%d", c.Server.Port)
	}
	c.Server.PublicBaseURL = strings.TrimRight(c.Server.PublicBaseURL, "/")
	if c.Database.Driver == "" {
		c.Database.Driver = "sqlite"
	}
	c.Database.Driver = strings.ToLower(strings.TrimSpace(c.Database.Driver))
	if c.Database.Driver == "postgresql" || c.Database.Driver == "pgsql" || c.Database.Driver == "psql" {
		c.Database.Driver = "postgres"
	}
	// sqlite 用相对路径时,以配置文件所在目录为基准拼成绝对路径,免得受"从哪个目录启动"影响。
	if c.Database.Driver == "sqlite" && c.Database.Path != "" && !filepath.IsAbs(c.Database.Path) {
		c.Database.Path = filepath.Join(baseDir, c.Database.Path)
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
