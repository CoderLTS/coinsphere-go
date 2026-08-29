# CoinSphere Go 后端

当前后端提供认证、RBAC、系统管理、监控和结构化系统日志，管理可信插件生命周期，执行批处理、事件与连续流工作流，并提供 Binance 公共行情、可信 Go 策略、回测、信号、Paper、通知和共享结果。Testnet、Live、Private Executor 和 Python Worker 不属于当前运行面。

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
- `workflow_runs`、`workflow_run_nodes`、`workflow_node_logs`、`workflow_run_checkpoints`、`workflow_node_states`
- `workflow_event_records`、`workflow_event_deliveries`、`workflow_event_outbox`、`workflow_human_tasks`
- `plugin_quant.instruments`、`plugin_quant.candles`、`plugin_quant.backtests`
- `plugin_quant.instrument_sources`、`plugin_quant.signals`、Paper 账户、订单、成交、费用、账本和持仓
- `result_views` 及用户/角色授权、`plugin_notification.deliveries`
- `system_log_settings`、`system_logs`
- Goose 管理的 `schema_migrations`

Quant 闭合 K 线使用普通 PostgreSQL 表和联合索引。项目不提供旧数据库运行时兼容层；重置和回滚步骤见[数据库迁移手册](../docs/runbooks/database-migrations.md)。

## 命令

```powershell
go run ./cmd/migrate -config ./config.yml -direction status
go run ./cmd/migrate -config ./config.yml -direction version
go run ./cmd/migrate -config ./config.yml -direction up
go run ./cmd/migrate -config ./config.yml -direction down -steps 1
go run ./cmd/admin -config ./config.yml -username coinsphere
go run ./cmd/coinsphere plugin validate <插件源码目录> [<插件源码目录>...]
go run ./cmd/coinsphere plugin install --config ./config.yml --backend-root . <插件源码目录>
go run ./cmd/coinsphere plugin upgrade --config ./config.yml --backend-root . <插件源码目录>
go run ./cmd/coinsphere plugin uninstall --config ./config.yml --backend-root . <插件ID>
go run ./cmd/coinsphere plugin purge-data --config ./config.yml --backend-root . --confirm "PURGE <插件ID>" <插件ID>
```

`plugin validate` 只读校验；`install`/`upgrade` 执行插件 migration、更新静态注册和构建输入，`uninstall` 保留数据，`purge-data` 需要精确确认且拒绝删除仍有引用的数据。插件包结构与 SDK 示例见[插件开发指南](../docs/plugin-development.md)。

执行后端门禁：

```powershell
go mod tidy -diff
go vet ./...
go build ./...
```

## 目录

```text
main.go              HTTP/工作流执行入口、migration 版本校验和有界关机
cmd/admin            管理员密码恢复命令
cmd/coinsphere       插件校验、安装、升级、卸载和数据清理命令
cmd/migrate          独立 migration 命令
internal/api         `/api/v1`、运行更新 WebSocket、中间件和 handlers
internal/config      YAML 配置与环境变量覆盖
internal/db          核心模型、连接和种子数据
internal/migration   Goose Runner、版本化核心 SQL 和迁移契约
internal/pluginbuild Go/Vue 静态注册表渲染器
internal/pluginregistry 编译进应用的 Go 插件注册表
internal/perm        基线权限码与菜单映射
internal/security    密码哈希、访问令牌和认证随机值
internal/service     认证、系统管理、工作流执行、结果视图、监控、日志和审计服务
plugin/official      Connector、AI、Quant 和 Notification 内置插件
plugin/manifest      严格插件清单、兼容性和源码路径校验
plugin/sdk           插件节点、处理器、作用域和注册协议
version              Core 与 SDK 兼容版本
```

更完整的模块与文件职责见[代码结构](../docs/code-structure.md)，运行数据流见[当前架构](../docs/architecture/overview.md)。
