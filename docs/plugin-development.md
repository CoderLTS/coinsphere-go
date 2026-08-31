# CoinSphere 插件开发指南

本文面向开发可信本地源码插件的工程师，描述当前 `coinsphere-plugin.json`、Go SDK、Vue 入口、migration、契约测试和生命周期。插件运维命令见[本地开发与诊断](runbooks/development.md)，平台边界见[当前架构](architecture/overview.md)。

## 1. 先理解插件模型

CoinSphere 插件不是运行时扩展包。安装会把源码复制进主仓库的 Backend/Frontend 安装目录，生成静态注册表，更新 Go module 依赖，执行插件 migration，并重新构建包含前后端的应用镜像。应用启动只加载已经编译进二进制和前端产物的插件，不扫描外部目录。

插件与 Go App 运行在同一进程，Vue 页面也运行在主 Web 应用中，因此：

- 只安装经过完整代码审查的本地可信源码。
- SDK 的 Secret、State、Artifact 和 RouteScope 是开发边界，不是安全沙箱。
- 插件技术上可以访问进程、文件系统、网络和数据库；manifest 不会限制普通 Go 权限。
- 插件错误、阻塞、panic、内存泄漏或恶意代码都可能影响整个应用。
- 当前不支持远程下载、签名市场、热加载、共享库、iframe 或独立插件进程。

当前兼容版本定义在 `backend/version/version.go`：

| 项目 | 当前值 |
| --- | --- |
| Core | `2.0.0` |
| SDK major | `2` |
| manifest `schemaVersion` | `1` |
| Go | `1.26.6` |
| JSON Schema | Draft 2020-12 |

插件 ID、版本和节点版本发布后都应视为持久契约。不要复用已发布 ID 表达不兼容语义。

## 2. 最小目录

一个只提供 Action 的最小插件仍必须包含 Backend Go module、Frontend 入口和至少一份 versioned migration：

```text
hello-plugin/
├─ coinsphere-plugin.json
├─ backend/
│  ├─ go.mod
│  └─ plugin.go
├─ frontend/
│  └─ index.ts
└─ migrations/
   └─ 00001_initial.sql
```

manifest 中的路径使用 `/`，必须是插件根目录内的相对路径。绝对路径、反斜杠、符号链接逃逸和不存在的文件都会被拒绝。

## 3. Manifest

下面是与最小目录匹配的完整 `coinsphere-plugin.json`：

```json
{
  "schemaVersion": 1,
  "id": "example.hello",
  "name": "Hello Plugin",
  "version": "1.0.0",
  "sdkMajor": 2,
  "requiresCore": ">=2.0.0 <3.0.0",
  "backend": {
    "module": "example.com/coinsphere/hello",
    "package": "backend"
  },
  "frontend": {
    "entry": "frontend/index.ts"
  },
  "migrations": {
    "directory": "migrations"
  },
  "contributes": ["nodes", "migrations"]
}
```

字段规则：

| 字段 | 规则 |
| --- | --- |
| `schemaVersion` | 当前只能是 `1` |
| `id` | 至少两个小写点分段，可包含数字和段内连字符；同时决定插件 schema 名 |
| `name` | 非空显示名 |
| `version` | 严格 SemVer，例如 `1.2.3` |
| `sdkMajor` | 必须等于当前 SDK major `2` |
| `requiresCore` | 合法 SemVer constraint，必须包含当前 Core `2.0.0` |
| `backend.module` | 必须与 `backend.package` 目录内 `go.mod` 的 module 完全一致 |
| `frontend.entry` | 插件根内存在的 TypeScript 入口文件 |
| `migrations.directory` | 插件根内存在的目录，至少包含一份版本化 SQL |
| `contributes` | 声明实际贡献；非 migration 项必须在 `Register` 中实际注册 |

`contributes` 只接受：

- `nodes`
- `triggers`
- `strategies`
- `apiRoutes`
- `pages`
- `resultPages`
- `assistantQueries`
- `migrations`

注册未声明的贡献或声明后没有注册都会失败。重复插件 ID、节点类型、策略 ID、页面 key、结果页 key 或路由也会失败。

### 助手只读查询

插件拥有需要被平台助手查询的领域数据时，可在 manifest 的 `contributes` 增加 `assistantQueries`，并通过 `Registrar.AssistantQuery` 注册查询：

```go
err := registrar.AssistantQuery(sdk.AssistantQueryDescriptor{
    Name:        "summary",
    Description: "查询插件领域状态的有界摘要。",
    InputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`),
}, sdk.AssistantQueryHandlerFunc(func(ctx context.Context, input json.RawMessage, scope sdk.SystemScope) (json.RawMessage, error) {
    return json.Marshal(map[string]any{"status": "ready"})
}))
```

查询名使用小写字母、数字和下划线；平台会用插件 ID 命名空间化最终工具名。输入在调用前按 Schema 校验，Handler 只接收 Core 注入的 `SystemScope`，不得从输入扩大用户或插件范围。结果必须是 64 KiB 内的合法 JSON，不得包含密钥、令牌、原始载荷或个人数据。只在插件确有独立持久数据时注册查询；平台知识、核心数据和工作流生成留在 Core。

## 4. Backend Go module

最小 `backend/go.mod`：

```go
module example.com/coinsphere/hello

go 1.26.6

require (
    coinsphere/backend v0.0.0
    github.com/gin-gonic/gin v1.12.0
)
```

插件只应导入公开包：

```go
import (
    "coinsphere/backend/plugin/sdk"
    "github.com/gin-gonic/gin"
)
```

不要导入 `coinsphere/backend/internal/*`。Go 的 `internal` 规则会阻止外部 module 使用它们，即使插件安装后位于主仓库中。

在插件源码目录独立运行测试时，可使用未提交的 `go.work`，或在开发副本的 `go.mod` 临时加入指向 CoinSphere `backend` 的 `replace`。安装器会在主 Backend `go.mod` 中为插件 module 生成本地 `require/replace`；不要手工修改生成后的主注册表或依赖项。

## 5. 最小 Action

每个 Backend 插件 module 必须导出：

```go
func Register(sdk.Registrar) error
```

以下 `backend/plugin.go` 注册一个无状态、无副作用的 Action：

```go
package hello

import (
    "context"
    "encoding/json"

    "coinsphere/backend/plugin/sdk"
)

var emptyObject = json.RawMessage(`{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object",
  "additionalProperties":false
}`)

var messageObject = json.RawMessage(`{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object",
  "properties":{"message":{"type":"string"}},
  "required":["message"],
  "additionalProperties":false
}`)

type echoAction struct{}

func (echoAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
    if err := ctx.Err(); err != nil {
        return sdk.ActionResult{}, err
    }
    var input struct {
        Message string `json:"message"`
    }
    if err := json.Unmarshal(request.Input, &input); err != nil {
        return sdk.ActionResult{}, err
    }
    output, err := json.Marshal(input)
    if err != nil {
        return sdk.ActionResult{}, err
    }
    return sdk.ActionResult{Output: output}, nil
}

func Register(registrar sdk.Registrar) error {
    return registrar.Action(sdk.NodeDescriptor{
        Type:         "example.hello.echo",
        Version:      "1.0.0",
        Kind:         sdk.NodeKindAction,
        ConfigSchema: emptyObject,
        UISchema:     json.RawMessage(`{}`),
        InputSchema:  messageObject,
        OutputSchema: messageObject,
        Pool:         sdk.PoolStream,
        SideEffect:   sdk.SideEffectNone,
        State:        sdk.StateStateless,
    }, echoAction{})
}
```

节点 `Type` 必须是小写点分 key，且不能使用保留前缀 `core.`。节点 `Version` 必须是严格 SemVer。Config/Input/Output Schema 必须显式声明 Draft 2020-12；UI Schema 只要求是 JSON 对象。需要多出口时在 `NodeDescriptor.Branches` 声明至少两个稳定分支键；运行时以节点输出的字符串 `branch` 选择端口，再执行边上的 Boolean CEL，同一端口可以连接零个或多个下游节点。

Backend 会在执行前后分别校验输入和输出 Schema。插件仍应处理 JSON 解码、外部响应、URL、文件和凭据等信任边界错误，并响应 `context.Context` 取消。

## 6. Action 上下文

`sdk.ActionRequest` 提供：

| 字段 | 用途 |
| --- | --- |
| `Revision` | 固定工作流与修订 ID |
| `NodeInstanceID` | 稳定节点实例 ID |
| `OperationKey` | 当前 Run/节点/Loop 迭代的稳定幂等键 |
| `Input` | 已按映射解析并通过 Schema 校验的 JSON |
| `Config` | 当前修订的普通节点配置 |
| `Secrets` | 读取当前节点声明的加密密钥 |
| `State` | 当前插件/工作流/节点命名空间状态 |
| `Artifacts` | 写入或打开内容寻址制品 |
| `Logger` | 写入当前节点的结构化日志 |

副作用必须以 `OperationKey` 或同等数据库唯一键实现幂等。不要把密钥、授权头、Cookie、DSN、原始请求/响应或个人数据写入 Logger、Output 或 Artifact。

`StatePersistent` 只适合需要跨 Run 保存小型 JSON 状态的节点。大型结果使用 Artifact，领域事实使用插件自有表；不要把状态存储当数据库替代品。

## 7. Trigger

长运行 Trigger 实现：

```go
type TriggerHandler interface {
    Run(context.Context, TriggerRequest, Emitter) error
}
```

通过 `registrar.Trigger` 注册，并同时在 manifest 声明 `triggers`。Trigger 必须：

- 使用 `NodeKindTrigger` 和 `PoolStream`。
- 只通过 Emitter 发布合法 CloudEvents 1.0 事件。
- 在连接、读取、退避和发送时响应上下文取消。
- 等待 Emitter 返回，接受工作流背压，不建立无界内存队列。
- 为事件设置稳定 `(source,id)` 和 1 至 256 字节的 `partitionkey`。
- 让断线重连和重复事件在数据库唯一约束下保持幂等。

Trigger 退出错误会使工作流进入 `error`。平台不会在插件内部替你恢复私有连接状态。

## 8. Strategy

策略通过 `registrar.Strategy` 注册，并在 manifest 声明 `strategies`。实现必须返回：

```go
type Strategy interface {
    Descriptor() sdk.StrategyDescriptor
    Evaluate(context.Context, sdk.EvaluateRequest) (decimal.Decimal, error)
}
```

策略 ID 和版本必须稳定，`MinimumLookback` 必须大于零，参数 Schema 必须是 Draft 2020-12。`Evaluate` 接收按时间升序的已闭合 K 线和 UTC 评估时间，返回 Decimal 目标仓位。

策略应保持无状态、确定性和只读：不访问数据库、不发送通知、不交易、不读取系统时间或环境隐式配置。相同 K 线、参数和版本必须得到相同输出，实时评估与回测才能共享实现。

## 9. RouteScope

路由通过 `registrar.Route` 注册，并在 manifest 声明 `apiRoutes`。`Method` 使用 HTTP 方法，`Pattern` 必须以 `/` 开头，路径参数使用 Gin 的 `:param` 语法；插件只提供相对 pattern，核心决定最终 URL 和授权。Handler 签名为 `func(*gin.Context, sdk.RouteScope)`，使用 `c.Param`、`c.Query` 和 `c.Request.Context()` 读取请求数据与传递取消信号。

```go
err := registrar.Route(sdk.RouteDescriptor{
    Method:  http.MethodPost,
    Pattern: "/accounts/:accountId/rebuild",
    Scope:   sdk.ScopeSystem,
}, func(c *gin.Context, scope sdk.RouteScope) {
    accountID := c.Param("accountId")
    // 校验 accountID 后调用领域服务，并通过 c 写入响应。
})
```

| Scope | 当前状态 | 最终入口与语义 |
| --- | --- | --- |
| `sdk.ScopeSystem` | 已挂载 | `/api/v1/plugins/{pluginId}{pattern}`；仅超级管理员，注入用户和角色 |
| `sdk.ScopeResult` | 已挂载 | `/api/v1/result-views/{viewId}/plugins/{pluginId}{pattern}`；注入固定 View scope/filter、用户、角色和允许操作 |
| `sdk.ScopeWorkflow` | SDK 已定义，当前未挂载公共 HTTP | 保留给工作流/节点管理面；插件不能假定存在可调用 URL |

Result 路由如果设置 `Action`，该 action 只能用于 `ScopeResult`，并且请求必须同时通过 ResultView 白名单和 RBAC。当前核心识别 `approve`、`reject`、`retry`、`cancel`、`pause`、`export` 等权限映射；插件仍须检查自己的领域状态。

不要从 query、path、header 或 JSON body 自行恢复用户提交的 workflow ID 来扩大范围。Result 路由必须使用核心注入的 `sdk.ResultScope`。

## 10. Page 与 ResultPage

普通插件页通过 `registrar.Page` 注册，包含 `PageKey`、标题、图标和 keep-alive 设置；启动种子会把页面加入系统菜单。ResultPage 通过 `registrar.ResultPage` 注册，并声明固定范围/过滤器 Schema、允许操作和移动端能力。

例如：

```go
if err := registrar.ResultPage(sdk.ResultPageDescriptor{
    PageKey:        "overview",
    Title:          "Hello Results",
    ComponentEntry: "frontend/ResultPage.vue",
    ScopeSchema:    emptyObject,
    FilterSchema:   emptyObject,
    Actions:        []string{"export"},
    Mobile:         true,
}); err != nil {
    return err
}
```

同时把 `resultPages` 加入 manifest，并在 `frontend/index.ts` 导出相同 page key：

```ts
export const resultPages = {
  overview: () => import('./ResultPage.vue')
}
```

只提供 Backend 节点、不提供页面时，最小 Frontend 入口可以是：

```ts
export {}
```

普通页使用 `pages` 映射，结果页使用 `resultPages` 映射。组件在主应用中运行，可使用已经安装的 Vue/Element Plus 能力，但外部插件安装器当前不会管理独立 npm 依赖；新增前端依赖前必须先把它作为主 Frontend 的显式依赖评审。

## 11. Migration 与数据所有权

插件 migration 使用 Goose SQL，文件名必须包含唯一数字版本，并同时包含 Up 和 Down：

```sql
-- +goose Up
CREATE TABLE hello_records (
    id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    operation_key VARCHAR(64) NOT NULL UNIQUE,
    message TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM hello_records LIMIT 1) THEN
        RAISE EXCEPTION 'refusing to drop non-empty hello_records';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE hello_records;
```

安装器先创建 `plugin_<规范化插件 ID>` schema，将 `search_path` 设为插件 schema 和 `public`，并把 migration 账本保存在插件 schema 的 `schema_migrations`。插件 migration 应使用未限定表名或自己的 schema，不得修改其他插件表、核心表或 migration 账本。

金融时间使用 `TIMESTAMPTZ`，价格、数量、金额和费率使用 `NUMERIC(38,18)`；JSON 对外返回十进制字符串。领域幂等键和不变量应由数据库唯一约束、外键和 CHECK 约束保证。

兼容升级只能追加更高版本 migration，已安装 migration 必须字节不变。Down 只为安装失败恢复和隔离环境验证提供，并应拒绝删除非空持久事实。正常卸载不执行 Down。

## 12. 契约测试

`coinsphere/backend/plugin/contracttest` 使用与应用相同的 manifest 和 Registry 校验插件。最小测试可以加载插件并执行 Action：

```go
package hello

import (
    "context"
    "encoding/json"
    "testing"

    "coinsphere/backend/plugin/contracttest"
    "coinsphere/backend/plugin/sdk"
)

func TestEchoContract(t *testing.T) {
    contract := contracttest.Load(t, "..", Register)
    result := contract.Execute(context.Background(), "example.hello.echo", sdk.ActionRequest{
        Input: json.RawMessage(`{"message":"hello"}`),
    })
    if string(result.Output) != `{"message":"hello"}` {
        t.Fatalf("unexpected output: %s", result.Output)
    }
}
```

`Contract.ServeRoute` 可执行已注册 Route，`Contract.ResultPage` 会检查描述符和组件路径。插件自己的测试还应覆盖外部输入校验、上下文取消、幂等副作用、Decimal/UTC、migration Up/Down 保护和升级兼容性。

## 13. 本地校验

在 CoinSphere `backend` 目录执行只读 manifest 校验：

```powershell
go run ./cmd/coinsphere plugin validate D:\plugins\hello-plugin
```

一次可以传入多个目录，工具会同时检查跨插件 ID 和前端目录冲突。`validate` 会检查：

- manifest 严格 JSON 和未知字段。
- Core/SDK SemVer 兼容性。
- 所有声明路径都在插件根目录内。
- Backend module 与 `go.mod` 一致。
- migration 版本、重复版本和 Up/Down 标记。
- 插件 schema 名是否合法。
- Go/Vue 静态注册表能否确定性生成。

`validate` 不复制源码、不连接数据库、不执行 migration、不修改注册表、不更新依赖，也不构建镜像。它不会编译插件实现；提交安装前仍应在插件开发环境完成 Go 和 Vue 检查。

## 14. 安装与生命周期

变更前停止应用，备份数据库和 Backend 持久目录，并确保同一 checkout 没有另一个插件维护命令。然后在 `backend` 目录执行：

```powershell
go run ./cmd/coinsphere plugin install --config ./config.yml --backend-root . D:\plugins\hello-plugin
go run ./cmd/coinsphere plugin upgrade --config ./config.yml --backend-root . D:\plugins\hello-plugin
go run ./cmd/coinsphere plugin uninstall --config ./config.yml --backend-root . example.hello
go run ./cmd/coinsphere plugin purge-data --config ./config.yml --backend-root . --confirm "PURGE example.hello" example.hello
```

生命周期行为：

| 动作 | 行为 |
| --- | --- |
| `install` | 校验、执行 plugin migration、复制 Backend/Frontend、生成注册表、更新 Go 依赖、构建镜像、记录安装版本 |
| `upgrade` | 要求版本增加且 major 不变，验证旧 migration 字节不变，只追加 migration，再执行与 install 相同的构建流程 |
| `uninstall` | 有活动引用时拒绝；移除编译输入并重建镜像，安装记录改为 uninstalled，保留 schema 和数据 |
| `purge-data` | 要求已经卸载、无任何活动或历史引用、确认文本精确匹配；事务删除插件 schema 和安装记录 |

安装器不会启动或重启候选镜像。安装、升级或卸载在构建/记录失败时会恢复源码目录、生成注册表、主 `go.mod/go.sum` 和本次可回滚 migration。应用切换和健康检查仍按部署 Runbook 手工完成。

## 15. 发布检查清单

- manifest、Go module、Frontend 入口和 migration 路径完全匹配。
- 插件/节点/策略/page key 使用稳定命名，版本为严格 SemVer。
- manifest 贡献与 `Register` 实际注册一致。
- 所有 Schema 使用正确 Draft，密钥字段只通过节点 SecretReader 读取。
- Action/Trigger 响应取消，副作用使用稳定操作键幂等。
- Route 只使用当前已挂载作用域，不信任客户端范围。
- migration 只修改插件 schema，历史文件不变，Down 保护非空事实。
- Frontend 页面不依赖未进入主应用锁文件的包。
- `plugin validate`、插件自身测试和目标环境构建通过。
- 代码评审确认插件为完全可信源码，且不包含真实密钥、私有交易调用或敏感日志。
