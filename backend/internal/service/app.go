// Package service 全部业务逻辑。App 结构承载共享依赖,方法按领域分布在各文件。
package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/security"
)

// ErrPermission 业务权限错误(映射为 HTTP 403)。
var ErrPermission = errors.New("permission denied")

// App 单进程运行时:数据库 + 配置 + 安全组件 + 实时推送 + 队列内部状态。
type App struct {
	DB     *gorm.DB
	Cfg    *config.AppConfig
	Hasher *security.PasswordHasher
	Tokens *security.TokenManager
	Cipher *security.SecretCipher
	Hub    *Hub

	WorkerID string

	// runningKeys 进程内并发键占用表,替代原 Redis 并发租约。
	// ponytail: 单实例内存实现;多实例部署时需换成数据库级约束。
	runningKeys   map[string]int
	runningKeysMu sync.Mutex

	// dispatcherWake 入队后立即唤醒派发循环,降低触发延迟。
	dispatcherWake chan struct{}

	stop chan struct{}
	wg   sync.WaitGroup
}

// NewApp 组装运行时。
func NewApp(gdb *gorm.DB, cfg *config.AppConfig, workerID string) (*App, error) {
	cipher, err := security.NewSecretCipher(cfg.Auth.SecretKey)
	if err != nil {
		return nil, err
	}
	return &App{
		DB:             gdb,
		Cfg:            cfg,
		Hasher:         security.NewPasswordHasher(cfg.Auth.PasswordIterations),
		Tokens:         security.NewTokenManager(cfg.Auth.SecretKey, cfg.Auth.AccessTokenTTLMinutes, cfg.Auth.RefreshTokenTTLDays),
		Cipher:         cipher,
		Hub:            NewHub(),
		WorkerID:       workerID,
		runningKeys:    map[string]int{},
		dispatcherWake: make(chan struct{}, 1),
		stop:           make(chan struct{}),
	}, nil
}

const timeLayout = "2006-01-02 15:04:05"

// fmtTime 时间格式化,nil 输出空串,与原后端一致。
func fmtTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format(timeLayout)
}

func fmtTimeV(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(timeLayout)
}

// M JSON 对象别名。
type M = map[string]any

// readPath 读取点号路径,支持列表下标。
func readPath(source any, dottedPath string) any {
	current := source
	for _, part := range strings.Split(dottedPath, ".") {
		if part == "" {
			continue
		}
		switch node := current.(type) {
		case map[string]any:
			current = node[part]
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(node) {
				return nil
			}
			current = node[index]
		default:
			return nil
		}
	}
	return current
}

// loadJSONObject 解析 JSON 文本为对象,失败返回空对象。
func loadJSONObject(text string) M {
	if strings.TrimSpace(text) == "" {
		return M{}
	}
	var value M
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return M{}
	}
	if value == nil {
		return M{}
	}
	return value
}

// dumpJSON 序列化为 JSON 文本(不转义中文)。
func dumpJSON(value any) string {
	var buf strings.Builder
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "{}"
	}
	return strings.TrimRight(buf.String(), "\n")
}

// serializeSnapshot 序列化快照并按字节上限截断。
func serializeSnapshot(value any, maxBytes int) string {
	text := dumpJSON(value)
	if len(text) <= maxBytes {
		return text
	}
	limit := maxBytes - 32
	if limit < 0 {
		limit = 0
	}
	preview := text[:limit]
	// 避免截断在多字节字符中间。
	for len(preview) > 0 && !utf8.ValidString(preview) {
		preview = preview[:len(preview)-1]
	}
	return dumpJSON(M{"_truncated": true, "preview": preview})
}

// pagedResult 统一分页响应。
func pagedResult(records any, current, size int, total int64) M {
	return M{"records": records, "current": current, "size": size, "total": total}
}

// bizErr 业务错误(等价原后端的 ValueError,统一转为 code=400)。
func bizErr(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// truncate 截断字符串到 n 个字节内(按 rune 安全)。
func truncateRunes(value string, n int) string {
	runes := []rune(value)
	if len(runes) <= n {
		return value
	}
	return string(runes[:n])
}

// ptr 便捷取地址。
func ptr[T any](v T) *T { return &v }
