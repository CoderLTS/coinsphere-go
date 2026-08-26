# CoinSphere Go 后端

当前后端提供认证、RBAC、系统管理、监控、可信插件生命周期、完整工作流执行，以及 Binance 公共行情、可信 Go 策略和回测。Paper、Testnet/Live、Private Executor 和 Python Worker 不属于当前运行面。

## 启动

```powershell
cd backend
$env:COINSPHERE_DATABASE__DSN = 'postgresql://coinsphere:test-only@127.0.0.1:5432/coinsphere?sslmode=disable'
$env:COINSPHERE_AUTH__SECRET_KEY = 'local-only-random-secret'
go run ./cmd/migrate -config ./config.yml -direction up
go run .
```

服务只读校验 migration 版本，不在启动时执行 DDL。首次启动会幂等写入内置角色、菜单和超级管理员；默认账号为 `coinsphere` / `coinsphere`，登录后必须立即修改密码。

## 数据基线

核心 migration 当前创建：

- `roles`、`users`、`user_roles`
- `menus`、`menu_buttons`、`role_menus`、`role_menu_buttons`
- `i18n_texts`、`audit_records`
- `plugin_installations`、`plugin_references`
- `workflows`、`workflow_revisions`、`workflow_secret_bindings`、`workflow_runtimes`
- `execution_batches`、`workflow_node_runs`、`workflow_checkpoints`、`workflow_node_states`
- `workflow_event_records`、`workflow_event_deliveries`、`workflow_event_outbox`、`workflow_human_tasks`
- `plugin_quant.instruments`、`plugin_quant.candles` hypertable、`plugin_quant.backtests`
- Goose 管理的 `schema_migrations`

基线启用 TimescaleDB，并为 Quant 闭合 K 线创建 hypertable。项目不提供旧数据库转换或兼容路径；重置和回滚步骤见[数据库迁移手册](../docs/runbooks/database-migrations.md)。

## 命令

```powershell
go run ./cmd/migrate -config ./config.yml -direction status
go run ./cmd/migrate -config ./config.yml -direction version
go run ./cmd/migrate -config ./config.yml -direction up
go run ./cmd/migrate -config ./config.yml -direction down -steps 1
go run ./cmd/admin -config ./config.yml -username coinsphere
go run ./cmd/coinsphere plugin validate <插件源码目录> [<插件源码目录>...]
```

`plugin validate` 只读校验；`install`/`upgrade` 执行插件 migration、更新静态注册和构建输入，`uninstall` 保留数据，`purge-data` 需要精确确认且拒绝删除仍有引用的数据。

执行后端门禁：

```powershell
go mod tidy -diff
go vet ./...
go build ./...
```

## 目录

```text
main.go              HTTP 服务入口、migration 版本校验和有界关机
cmd/admin            管理员密码恢复命令
cmd/coinsphere       插件清单只读校验命令
cmd/migrate          独立 migration 命令
internal/api         V2 基线路由、中间件和 handlers
internal/config      YAML 配置与环境变量覆盖
internal/db          V2 基线模型、连接和种子数据
internal/migration   Goose Runner、版本化核心 SQL 和迁移契约
internal/pluginbuild Go/Vue 静态注册表渲染器
internal/pluginregistry 编译进应用的 Go 插件注册表
internal/perm        基线权限码与菜单映射
internal/security    密码哈希、访问令牌和认证随机值
internal/service     认证、系统管理、工作流图与生命周期、监控和审计服务
plugin/manifest      严格插件清单、兼容性和源码路径校验
plugin/sdk           插件节点、处理器、作用域和注册协议
version              Core 与 SDK 兼容版本
```
