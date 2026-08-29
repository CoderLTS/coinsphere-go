// Package config loads YAML configuration with COINSPHERE_ environment overrides.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	envPrefix     = "COINSPHERE_"
	envPathSep    = "__"
	configPathEnv = envPrefix + "CONFIG_PATH"
)

const DefaultInsecureSecret = "coinsphere-dev-secret"

type DatabaseConfig struct {
	DSN                    string `yaml:"dsn"`
	MaxOpenConns           int    `yaml:"max_open_conns"`
	MaxIdleConns           int    `yaml:"max_idle_conns"`
	ConnMaxIdleTimeSeconds int    `yaml:"conn_max_idle_time_seconds"`
}

type ServerConfig struct {
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	PublicBaseURL string `yaml:"public_base_url"`
}

type AuthConfig struct {
	SecretKey               string `yaml:"secret_key"`
	BootstrapAdminPassword  string `yaml:"bootstrap_admin_password"`
	AccessTokenTTLMinutes   int    `yaml:"access_token_ttl_minutes"`
	PasswordIterations      int    `yaml:"password_iterations"`
	LoginRateLimitPerMinute int    `yaml:"login_rate_limit_per_minute"`
}

type LogConfig struct {
	Level         string `yaml:"level"`
	RetentionDays int    `yaml:"retention_days"`
}

type WorkflowConfig struct {
	HTTPAllowedHosts []string `yaml:"http_allowed_hosts"`
}

type AppConfig struct {
	Database DatabaseConfig `yaml:"database"`
	Server   ServerConfig   `yaml:"server"`
	Auth     AuthConfig     `yaml:"auth"`
	Workflow WorkflowConfig `yaml:"workflow"`
	Log      LogConfig      `yaml:"log"`
}

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
	cfg.normalize()
	return cfg, nil
}

func defaultConfig() *AppConfig {
	return &AppConfig{
		Database: DatabaseConfig{MaxOpenConns: 40, MaxIdleConns: 10, ConnMaxIdleTimeSeconds: 300},
		Server:   ServerConfig{Host: "0.0.0.0", Port: 6987},
		Auth: AuthConfig{
			SecretKey: DefaultInsecureSecret, AccessTokenTTLMinutes: 15,
			PasswordIterations: 390000, LoginRateLimitPerMinute: 10,
		},
		Log: LogConfig{Level: "info", RetentionDays: 30},
	}
}

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
	if c.Auth.BootstrapAdminPassword == "" {
		c.Auth.BootstrapAdminPassword = "coinsphere"
	}
	if c.Auth.AccessTokenTTLMinutes < 1 {
		c.Auth.AccessTokenTTLMinutes = 15
	}
	if c.Auth.PasswordIterations < 1 {
		c.Auth.PasswordIterations = 390000
	}
	if c.Auth.LoginRateLimitPerMinute < 1 {
		c.Auth.LoginRateLimitPerMinute = 10
	}
	if c.Log.RetentionDays < 1 || c.Log.RetentionDays > 365 {
		c.Log.RetentionDays = 30
	}
}

func (c *AppConfig) Validate() error {
	if c.Auth.SecretKey == "" || c.Auth.SecretKey == DefaultInsecureSecret {
		if os.Getenv("COINSPHERE_ALLOW_INSECURE_SECRET") != "1" {
			return fmt.Errorf("auth.secret_key 未设置或仍为默认值,存在令牌伪造风险:请用环境变量 COINSPHERE_AUTH__SECRET_KEY 或 config.yml 配一个随机密钥(如 `openssl rand -hex 32`);本地开发可临时设 COINSPHERE_ALLOW_INSECURE_SECRET=1 放行")
		}
	}
	return nil
}

func applyEnvOverrides(tree map[string]any) {
	for _, item := range os.Environ() {
		name, value, ok := strings.Cut(item, "=")
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
