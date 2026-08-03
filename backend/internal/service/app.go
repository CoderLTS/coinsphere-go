// Package service 全部业务逻辑。App 结构承载共享依赖,方法按领域分布在各文件。
package service

import (
	"context"
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
	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/security"
)

// ErrPermission 业务权限错误(映射为 HTTP 403)。
// var 写在函数外 = 包级变量(整个包都能用);errors.New 造一个固定的"哨兵错误"值,
// 别处可用 errors.Is(err, ErrPermission) 来比对是不是这种错。
// 首字母大写 ErrPermission = 导出标识符,别的包能引用;见 GO入门笔记『项目怎么组织』。
var ErrPermission = errors.New("permission denied")

// App 单进程运行时:数据库 + 配置 + 安全组件 + 实时推送 + 队列内部状态。
//
// App 就是本包的"依赖容器":把整个后端要共用的东西(数据库连接、配置、安全工具、
// 实时推送中心、后台循环的开关)全部塞进这一个结构体。程序启动时用 NewApp 造出一个
// *App,之后各领域的功能都写成方法 func (a *App) Xxx(),通过接收者 a 拿到这些共享依赖。
// 所以读懂这个结构体,就等于拿到了理解整个 service 包的钥匙。
// struct(结构体)= 把一组字段打包在一起,类似别的语言的对象但没有继承;见 GO入门笔记『复合类型』。
type App struct {
	// 下面这组都是指针字段(类型前的 * 表示"指向某对象的指针"),指向启动时创建好的共享组件:
	//   DB     —— 数据库句柄(GORM),所有增删改查都通过它
	//   Cfg    —— 全局配置(来自 config.yml)
	//   Hasher —— 密码哈希/校验工具
	//   Tokens —— 登录令牌(access/refresh token)的签发与校验
	//   Cipher —— 敏感信息的对称加解密
	//   Hub    —— WebSocket 连接中心,负责给前端实时推送
	// 用指针可以避免复制大对象,并让多处共享同一份实例。见 GO入门笔记『复合类型』。
	DB       *gorm.DB
	Cfg      *config.AppConfig
	Hasher   *security.PasswordHasher
	Tokens   *security.TokenManager
	Cipher   *security.SecretCipher
	Hub      *Hub
	database *gorm.DB

	// dummyHash 预先算好的“假密码哈希”:登录时若用户不存在/停用,也拿它跑一次校验,
	// 让各分支耗时一致,消除通过响应快慢枚举用户名的可能(见评审 #7)。
	dummyHash string

	// WorkerID 当前进程的工作节点标识,会写进执行记录,便于区分"是哪个节点执行的"。
	WorkerID string

	// map[键类型]值类型 是 Go 的字典/哈希表;sync.Mutex 是互斥锁。
	// 多个 goroutine 同时读写同一个 map 会崩溃,所以用 runningKeysMu 加锁保护 runningKeys。
	// 见 GO入门笔记『并发』。
	// runningKeys 进程内并发键占用表,替代原 Redis 并发租约。
	// ponytail: 单实例内存实现;多实例部署时需换成数据库级约束。
	runningKeys   map[string]int
	runningKeysMu sync.Mutex

	// dispatcherWake 是 channel(管道,goroutine 之间传信号用),元素是 struct{}
	// (零字节类型,只当"信号"不携带数据):
	//   dispatcherWake —— 有新任务入队时往里发一下,立刻唤醒派发循环,降低延迟
	// runtimeCtx 贯通进程关机、后台循环和工作流执行;wg 等所有后台 goroutine 干净退出。
	// dispatcherWake 入队后立即唤醒派发循环,降低触发延迟。
	dispatcherWake chan struct{}

	runtimeCtx    context.Context
	runtimeCancel context.CancelFunc
	wg            sync.WaitGroup
}

// NewApp 组装运行时。
//
// NewApp 是 App 的"构造函数":Go 没有 class / constructor 关键字,惯例就是写一个 New 开头
// 的函数,把各个依赖 new 好、填进结构体再返回。返回类型 (*App, error) 是多返回值:
// 一个成功结果(*App 指针)+ 一个错误(error),见 GO入门笔记『变量、函数、错误』。
func NewApp(parentCtx context.Context, gdb *gorm.DB, cfg *config.AppConfig, workerID string) (*App, error) {
	if parentCtx == nil {
		return nil, errors.New("runtime context is required")
	}
	// 00003 是可靠 Outbox 运行时的原子 schema 契约。服务进程只校验、不代跑 migration；
	// 否则全新 SQLite 会被旧 AutoMigrate 建成缺列表，后台循环只能持续失败而无法投递。
	if (cfg.Database.Driver == "sqlite" || cfg.Database.Driver == "postgres") &&
		!gdb.Migrator().HasColumn(&db.DomainEventOutbox{}, "max_attempts") {
		return nil, errors.New("database migration 00003 is required; run coinsphere-migrate -direction up before starting the backend")
	}
	// := 是短变量声明(自动推断类型,只能在函数内部用);这里一次接住两个返回值:cipher 和 err。
	// 紧跟的 if err != nil 是 Go 最典型的错误处理:出错就带着错误提前 return,不靠抛异常。
	cipher, err := security.NewSecretCipher(cfg.Auth.EncryptionKey)
	if err != nil {
		return nil, err
	}
	// 复用同一个 hasher:既装进 App.Hasher,也用它预算一份假哈希填 dummyHash(见评审 #7)。
	hasher := security.NewPasswordHasher(cfg.Auth.PasswordIterations)
	runtimeCtx, runtimeCancel := context.WithCancel(parentCtx)
	// &App{...} 是"结构体字面量 + 取地址(&)":当场把各字段填好,并返回它的指针 *App。
	// 字段名后跟冒号按名赋值,顺序随意。make(chan struct{}, 1) 造一个容量为 1 的带缓冲 channel。
	// 结尾的 nil 占据 error 返回值的位置,表示"没有错误"。
	return &App{
		DB:             gdb.WithContext(runtimeCtx),
		Cfg:            cfg,
		Hasher:         hasher,
		Tokens:         security.NewTokenManager(cfg.Auth.SecretKey, cfg.Auth.AccessTokenTTLMinutes, cfg.Auth.RefreshTokenTTLDays),
		Cipher:         cipher,
		Hub:            NewHub(),
		WorkerID:       workerID,
		dummyHash:      hasher.HashPassword(security.RandomToken()),
		runningKeys:    map[string]int{},
		dispatcherWake: make(chan struct{}, 1),
		runtimeCtx:     runtimeCtx,
		runtimeCancel:  runtimeCancel,
		database:       gdb,
	}, nil
}

func (a *App) dbWithContext(ctx context.Context) *gorm.DB {
	if a.database != nil {
		return a.database.WithContext(ctx)
	}
	return a.DB.WithContext(ctx)
}

// Go 用一个固定的"参考时间"当格式模板:2006-01-02 15:04:05 恰好是 1 2 3 4 5 6(月日时分秒年)。
// 想要什么格式,就把这串"魔法数字"摆成什么样,而不像别的语言用 %Y-%m-%d 这类占位符。
const timeLayout = "2006-01-02 15:04:05"

// fmtTime 时间格式化,nil 输出空串,与原后端一致。
// 参数 *time.Time 是指针,所以可能是 nil(表示"没有时间");这也是 Go 表达"可选值"的常用手法:
// 先判空,再顺着指针调用 .Format 格式化。
func fmtTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format(timeLayout)
}

// 这个版本收非指针的 time.Time(是"值"不会是 nil),改用 IsZero() 判断是不是"零值时间"。
func fmtTimeV(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(timeLayout)
}

// M JSON 对象别名。
// 注意这里的 = :它让 M 成为 map[string]any 的"别名"(两者完全等同、可互换)。
// 若写成 type M map[string]any(没有 =)则是"另造一个新类型"。any = 任意类型(interface{} 的别名),
// 所以 M 就是"字符串做键、任意值"的 JSON 对象,后面到处用它拼接口响应。见 GO入门笔记『复合类型』。
type M = map[string]any

// readPath 读取点号路径,支持列表下标。
func readPath(source any, dottedPath string) any {
	current := source
	// for ... range 遍历:range 一个切片时,_ 丢弃下标、part 取到每个元素值。见 GO入门笔记『复合类型』。
	for _, part := range strings.Split(dottedPath, ".") {
		if part == "" {
			continue
		}
		// 类型 switch:current 的静态类型是 any(任意类型),这里在运行时判断它"实际"是什么——
		// 是 map 就走第一个 case、是 slice 就走第二个,node 会被转成对应的具体类型再使用。
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
	// []byte(text) 是类型转换:把字符串转成字节切片(json 库要字节)。&value 传的是指针,
	// 让 Unmarshal 把解析结果"写回"到 value 里。if err := ...; err != nil 是"在 if 里就地
	// 声明并判断",这个 err 只在该 if 块内可见。
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
	// strings.Builder 是高效拼接字符串的缓冲区,比反复用 + 号拼接省内存。
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
	// text[:limit] 是切片操作:取前 limit 个字节组成一个新切片(共享底层数据,不复制内容)。
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
// args ...any 是"可变参数":调用时可传任意多个值,函数内部 args 就是一个切片;
// 传给 fmt.Errorf 时用 args... 再"展开"回去。fmt.Errorf 按格式串生成一个 error 值。
func bizErr(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// truncate 截断字符串到 n 个字节内(按 rune 安全)。
func truncateRunes(value string, n int) string {
	// []rune(value) 把字符串按"字符(Unicode 码点)"拆开:中文一个字占多个字节,
	// 只有转成 []rune 才能按"字数"安全截断,不会把一个汉字从中间切碎。
	runes := []rune(value)
	if len(runes) <= n {
		return value
	}
	return string(runes[:n])
}
