# Go 入门笔记 —— 看懂本项目后端代码

写给完全没接触过 Go 的人。目标不是教你成为 Go 专家,而是让你**读得懂 `backend/` 里的每一段代码**。
代码里的行内注释会引用本文的小节名(如"见 GO入门笔记『并发』"),所以基础概念只在这里讲一遍。

配套阅读顺序建议：先看[代码结构](../docs/code-structure.md)，再按 `main.go`（入口）→ `internal/config` → `internal/db` → `internal/api` → `internal/service` 阅读。

---

## 0. 一分钟看懂 Go 是什么

- **编译型**:代码 `go build` 成一个 `.exe`,直接运行,不像 Python 需要解释器。
- **强类型**:每个变量都有确定类型,编译期就检查错误。
- **自带并发**:一个关键字 `go` 就能开一个"轻量线程"(goroutine)。本项目的调度器/执行器全靠它,不再需要 Python 版的 Redis + 多进程。
- **没有 class、没有继承、没有异常**:用 struct(结构体)+ method(方法)+ interface(接口)组织代码,用返回值 `error` 处理错误。

---

## 1. 项目怎么组织:module / package / import

```go
// go.mod 第一行
module coinsphere/backend      // 整个项目的"根名字",import 时以它开头
go 1.26                        // 需要的 Go 版本
```

- **package(包)**:每个 `.go` 文件第一行 `package xxx`。**同一个文件夹里的所有文件必须是同一个 package**。一个 package 内部,函数/变量互相直接可见,不用 import。
- **import(导入)**:要用别的文件夹(包)里的东西,先 import。

```go
import (
    "log"                              // 标准库(Go 自带)
    "coinsphere/backend/internal/db"   // 本项目的包 = module 名 + 文件夹路径
    "gorm.io/gorm"                     // 第三方库(go.mod 里 require 的)
)
```

- **`internal/` 文件夹**:Go 的特殊约定 —— `internal` 里的包只能被本项目 import,外部项目无法引用。用来放"内部实现"。
- **大写 = 公开,小写 = 私有**:标识符(函数名/变量名/字段名)**首字母大写**表示能被别的包访问(public),**小写**表示只在本包内可见(private)。这是 Go 唯一的可见性规则,没有 `public`/`private` 关键字。
  例:`func (a *App) StartRuntime(ctx context.Context)` 外部能调;`func (a *App) spawn()` 只能本包调。

---

## 2. 变量、函数、错误

### 变量声明
```go
var name string = "coinsphere"   // 完整写法
name := "coinsphere"             // 短声明:自动推断类型,函数内部最常用
count := 0                       // 推断为 int
```
`:=` 只能在函数**内部**用,且左边至少有一个是新变量。

### 函数 & 多返回值
Go 函数可以**返回多个值**,这点和 Python 的元组类似,但是语言原生的:
```go
func Load(path string) (*AppConfig, error) {   // 返回两个值:配置指针 + 错误
    ...
    return cfg, nil                             // 成功:err 用 nil(空)
}
```

### 错误处理:没有 try/except,只有 `if err != nil`
Go 把"出错了吗"作为**返回值**传出来,而不是抛异常。**最常见的一段代码**:
```go
cfg, err := config.Load(path)   // 同时接住结果和错误
if err != nil {                 // err 不是 nil 就代表出错了
    log.Fatalf("load config: %v", err)   // 打日志并退出
}
// 走到这里说明 err == nil,cfg 一定可用
```
- `nil` = "空/无",相当于别的语言的 null。
- `%v` 是格式化占位符(见 `fmt.Sprintf`/`log.Printf`),把任意值转成字符串。
- `fmt.Errorf("read config %s: %w", path, err)` 里的 `%w` 表示"包裹"另一个错误,保留原始错误链。

---

## 3. 复合类型:struct、指针、slice、map

### struct(结构体)= 一组字段打包
```go
type ServerConfig struct {
    Host string `yaml:"host"`   // 字段名 类型 `标签`
    Port int    `yaml:"port"`
}
```
- 反引号里的 **struct tag(标签)** 是给库看的元数据。本项目里:
  - `yaml:"host"` 告诉 YAML 库:配置文件里的 `host:` 对应这个字段(见『框架:YAML』)。
  - `gorm:"column:xxx"` 告诉 GORM:这个字段对应数据库哪一列(见『框架:GORM』)。
  - `json:"xxx"` 告诉 JSON 库:输出成 API 响应时用哪个键名。

### 指针 `*` 和 `&`
- `&x` = 取 x 的**地址**(得到一个指针)。
- `*T` = "指向 T 的指针"这种类型;`*p` = 顺着指针**取出**它指向的值。
- 为什么用指针?① 传大结构体时不复制,省内存;② 想让函数**修改**原对象而不是副本;③ 用 `nil` 表示"没有这个值"(可选值)。
```go
func NewApp(...) (*App, error)   // 返回 *App:一个指向 App 的指针
app.StartRuntime(ctx)            // app 是指针,用 . 直接调方法(Go 自动解引用)
```

### slice(切片)和 map(字典)
```go
var states []db.WorkflowRuntimeState   // slice:可变长数组,[]T 表示"一堆 T"
retry := []int{30, 120, 600}           // 带初值的 slice
m := map[string]any{"code": 200}       // map:键值对,map[键类型]值类型
```
- `any` 是 `interface{}` 的别名,表示"任意类型"(类似 Python 的 Any / TS 的 any)。API 响应体经常用 `map[string]any` 拼 JSON。
- `append(s, x)` 往 slice 追加元素;`len(s)` 取长度。
- `for i, v := range slice { ... }` 遍历,`i` 是下标,`v` 是值。遍历 map 也是 `range`。

---

## 4. 方法与接收者:Go 的"面向对象"

Go 没有 class。把函数"挂"到某个类型上,就成了方法:
```go
type App struct { ... }

// (a *App) 叫"接收者":表示这是 App 的方法,方法内用 a 指代当前对象(类似别的语言的 this/self)
func (a *App) StartRuntime(ctx context.Context) {
    a.spawn(ctx, a.schedulerLoop) // 调本对象的其它方法/字段
}
```
- **指针接收者 `(a *App)`**:方法内可以修改对象;不复制对象。本项目几乎都用它。
- 值接收者 `(a App)`:操作的是副本,改了不影响原对象。

调用:`app.StartRuntime(ctx)`。`ctx` 被取消时,Runtime 的循环和在途执行会一起收到停止通知。

---

## 5. interface(接口)= 只约定"能做什么",不管"是谁"

接口是一组方法签名。**任何类型只要实现了这些方法,就自动算"是"这个接口**(不需要显式声明 implements)。
```go
// 标准库里:http.Handler 接口只要求一个方法 ServeHTTP(...)
// Gin 的 Engine 实现了 http.Handler,能直接交给 http.Server 用。
```
本项目你会遇到的接口:
- `gorm.Dialector` —— "数据库方言"接口。本项目的 `db.go` 固定使用 `postgres.Open(...)`，避免为非目标数据库维护不同事务和约束语义。
- `http.Handler` —— HTTP 服务器接收的处理器接口；本项目的 Gin Engine 实现了它。

---

## 6. 并发(本项目的灵魂,重点看)

Python 版要 orchestrator + worker 两个进程 + Redis 协调;Go 版在**一个进程内**用 goroutine 搞定。看 `internal/service/loops.go`。

### goroutine:`go f()` 就开一条并发执行流
```go
go func() {                 // 开一个新 goroutine,和主流程同时跑
    log.Println("在后台跑")
}()                         // 末尾的 () 表示立刻调用这个匿名函数
```

### channel:goroutine 之间传数据/信号的"管道"
```go
ch := make(chan struct{})      // 创建一个 channel;struct{} 是"零字节"类型,只当信号用
close(ch)                      // 关闭 channel —— 所有在等它的 goroutine 会立刻收到通知
<-ch                           // 从 channel 接收(会阻塞,直到有数据或被 close)
ch <- struct{}{}               // 向 channel 发送
```

### select:同时等多个 channel,谁先来走谁
本项目用它实现"可被中断的 sleep":
```go
func sleeping(ctx context.Context, interval time.Duration) bool {
    timer := time.NewTimer(interval)
    defer timer.Stop()
    select {
    case <-ctx.Done():          // 根 Context 被信号或关机流程取消
        return false            //   → 提前醒来,返回 false 让循环退出
    case <-timer.C:             // 否则睡够 interval 时间
        return true
    }
}
```

### sync.WaitGroup:等一批 goroutine 全部结束
```go
a.wg.Add(1)          // 计数 +1:我要开一个 goroutine
go func() {
    defer a.wg.Done() // 结束时计数 -1(defer 见下一节)
    loop()
}()
...
a.wg.Wait()          // 阻塞,直到计数归零;实际关机还要用 Context 给这段等待设置总上限
```

### context.Context:传递"取消信号"和超时
你会在函数参数里看到 `ctx context.Context`。它是 Go 里传递"该停手了/超时了"的标准方式。`main.go` 用 `signal.NotifyContext` 把 `SIGINT`/`SIGTERM` 变成唯一根 Context 的取消信号,HTTP、Runtime、数据库和 WebSocket 都沿调用链接收它;应用最多等待 30 秒,Compose 最多等待 40 秒。

---

## 7. defer:登记"函数返回前一定要做的收尾"

```go
func (a *App) spawn(ctx context.Context, loop func(context.Context)) {
    a.wg.Add(1)
    go func() {
        defer a.wg.Done()   // 不管下面怎么退出,这个 goroutine 结束前一定执行 Done()
        loop(ctx)
    }()
}
```
`defer` 常用于:关闭文件/连接、解锁、计数收尾。多个 defer 按**后进先出**顺序执行。

---

## 8. 泛型:`[T any]`

`internal/api/api.go` 里有:
```go
func decodeBody[T any](c *gin.Context) (*T, error)   // T 是"类型参数",调用时替换成具体类型
```
意思是“对任意类型 T，把请求体 JSON 解析成一个 *T”。调用时 T 替换成 handler 声明的请求结构体，因此不用为每种请求重写解析函数。

---

## 9. 其它会撞见的小语法

- `:=` 里的 `_`:**丢弃**不想要的返回值。`hostname, _ := os.Hostname()` = 只要主机名,忽略错误。
- `iota`:在 `const (...)` 里自动递增,用来定义一组枚举常量。
- `switch`:比 C 系语言更简洁,`case` 默认不"穿透",不用写 break。
- `//` 单行注释,`/* */` 多行注释。
- 首字母大写才导出(见 §1),所以你会看到 `DB`、`Cfg` 这种大写字段名——它们要被别的包访问。

---

## 10. 用到的框架/库速查

| 库 | 干什么的 | 在哪看 |
|---|---|---|
| **Gin** | HTTP 路由与中间件 | `internal/api/api.go`、`routes.go` |
| **net/http**(标准库) | HTTP 服务器与通用接口 | `main.go`、`internal/api/api.go` |
| **GORM**(gorm.io/gorm) | ORM,把 struct 当数据库表操作,不用手写大部分 SQL | `internal/db/*.go`、各 service |
| **gorilla/websocket** | WebSocket 长连接(实时通知) | `internal/service/realtime.go` |
| **robfig/cron** | 解析 cron 表达式,算下次触发时间 | `internal/service/cronx.go` |
| **fernet-go** | 对称加密(密文存储敏感配置) | `internal/security/security.go` |
| **x/crypto/pbkdf2** | 密码哈希(和 Python 版兼容) | `internal/security/security.go` |
| **yaml.v3** | 解析 `config.yml` | `internal/config/config.go` |

### 框架:Gin
```go
router := gin.New()                                      // 路由表
router.POST("/api/v1/auth/login", s.handleLogin)        // 方法+路径 → 处理函数
router.PUT("/api/v1/admin/users/:userId", s.handleUser) // :userId 是路径参数
// 处理函数签名:func(c *gin.Context)
//   c.Param("userId") 取路径参数,c.Request.Context() 传递取消信号。
```

### 框架:GORM(把 struct 当表)
```go
type User struct {
    ID   int64  `gorm:"primaryKey"`
    Name string `gorm:"column:username"`
}
db.AutoMigrate(&User{})                 // 按 struct 自动建表/加列
db.Where("status = ?", "queued").Find(&list)  // SELECT ... WHERE status='queued'
db.First(&u, id)                        // 按主键查一条(查不到返回 ErrRecordNotFound)
db.Create(&u)                           // INSERT
db.Save(&u)                             // UPDATE(整行)
```
`?` 是占位符,值单独传,GORM 帮你转义,**防 SQL 注入**。`&u` 传指针,GORM 把查询结果**写回**到 u。

---

## 11. 怎么编译运行

```powershell
cd backend
go mod download          # 下载 go.mod 里列的依赖
go build -o server.exe . # 编译成一个 exe
.\server.exe             # 运行（默认读 ./config.yml，连接 PostgreSQL，监听 :6987）
```
改完代码重新 `go build` 即可。`go run .` 可以"编译+运行"一步到位(开发时方便)。

---

读到这里,再回去看 `main.go`,应该能一行行看懂了。祝顺利 🙂
