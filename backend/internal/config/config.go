// Package config 加载 YAML 配置并支持 COINSPHERE_ 前缀环境变量覆盖。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	envPrefix     = "COINSPHERE_"
	envPathSep    = "__"
	configPathEnv = envPrefix + "CONFIG_PATH"
)

// DatabaseConfig 数据库连接配置,driver 决定方言。
type DatabaseConfig struct {
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
func Load(path string) (*AppConfig, error) {
	if path == "" {
		path = strings.TrimSpace(os.Getenv(configPathEnv))
	}
	if path == "" {
		path = "config.yml"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var tree map[string]any
	if err := yaml.Unmarshal(raw, &tree); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if tree == nil {
		tree = map[string]any{}
	}
	applyEnvOverrides(tree)

	merged, err := yaml.Marshal(tree)
	if err != nil {
		return nil, err
	}
	cfg := defaultConfig()
	if err := yaml.Unmarshal(merged, cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	cfg.normalize(filepath.Dir(path))
	return cfg, nil
}

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
	for _, kv := range os.Environ() {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, envPrefix) || name == configPathEnv {
			continue
		}
		parts := strings.Split(strings.ToLower(name[len(envPrefix):]), strings.ToLower(envPathSep))
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		cursor := tree
		for _, part := range parts[:len(parts)-1] {
			next, isMap := cursor[part].(map[string]any)
			if !isMap {
				next = map[string]any{}
				cursor[part] = next
			}
			cursor = next
		}
		var parsed any
		if err := yaml.Unmarshal([]byte(value), &parsed); err != nil || parsed == nil {
			parsed = value
		}
		cursor[parts[len(parts)-1]] = parsed
	}
}
