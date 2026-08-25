# CoinSphere Go 后端

当前 V2 P0 基线只提供认证、RBAC、用户/角色/菜单管理和系统监控。工作流与插件能力按[V2 路线图](../docs/roadmap/README.md)逐步加入；旧新闻、行情、策略、Paper、Testnet/Live、Private Executor 和 Python Worker 不属于当前源码或数据模型。

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

`internal/migration/sql/00001_initial.sql` 是唯一 V2 初始化 migration，只创建：

- `roles`、`users`、`user_roles`
- `menus`、`menu_buttons`、`role_menus`、`role_menu_buttons`
- `i18n_texts`、`audit_records`
- Goose 管理的 `schema_migrations`

基线启用 TimescaleDB 扩展，但不创建 hypertable。项目不提供旧数据库转换或兼容路径；重置和回滚步骤见[数据库迁移手册](../docs/runbooks/database-migrations.md)。

## 命令

```powershell
go run ./cmd/migrate -config ./config.yml -direction status
go run ./cmd/migrate -config ./config.yml -direction version
go run ./cmd/migrate -config ./config.yml -direction up
go run ./cmd/migrate -config ./config.yml -direction down -steps 1
go run ./cmd/admin -config ./config.yml -username coinsphere
```

设置测试数据库后执行完整后端门禁：

```powershell
$env:COINSPHERE_TEST_POSTGRES_DSN = 'postgres://coinsphere:test-only@127.0.0.1:5432/coinsphere_test?sslmode=disable'
go mod tidy -diff
go vet ./...
go test -count=1 ./...
go build ./...
```

## 目录

```text
main.go              HTTP 服务入口、migration 版本校验和有界关机
cmd/admin            管理员密码恢复命令
cmd/migrate          独立 migration 命令
internal/api         V2 基线路由、中间件和 handlers
internal/config      YAML 配置与环境变量覆盖
internal/db          V2 基线模型、连接和种子数据
internal/migration   Goose Runner、单一初始化 SQL 和迁移契约
internal/perm        基线权限码与菜单映射
internal/security    密码哈希、访问令牌和认证随机值
internal/service     认证、系统管理、监控和审计服务
```
