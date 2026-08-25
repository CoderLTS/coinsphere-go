# CoinSphere 使用手册

本文面向当前 V2 个人自托管用户和管理员。当前可用能力是登录、RBAC、系统管理、系统监控、可信本地插件维护，以及超级管理员工作流生命周期 API；工作流画布、执行、Quant、回测与 Paper 尚未交付。

## 1. 使用范围

- 只支持 PostgreSQL/TimescaleDB。
- 不开放公开注册；首次启动创建内置超级管理员。
- 默认部署不包含 Python Worker、Private Executor、Redis 或消息代理。
- 当前版本不会连接 Binance 私有 API，不提供 Testnet/Live 放行或真实交易。

## 2. Docker Compose 安装

需要 Docker Engine 或 Docker Desktop，并支持 Compose v2。先生成并长期保存至少 32 字节随机签名密钥。

Linux/macOS：

```bash
export COINSPHERE_AUTH__SECRET_KEY="$(openssl rand -hex 32)"
docker compose up -d --build
docker compose ps
```

PowerShell：

```powershell
$bytes = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
$env:COINSPHERE_AUTH__SECRET_KEY = [BitConverter]::ToString($bytes).Replace('-', '').ToLowerInvariant()
docker compose up -d --build
docker compose ps
```

Compose 依次启动 `timescaledb`、一次性 `migrate`、`backend` 和 `web`。浏览器打开 <http://localhost:8080>。

停止服务不会删除数据：

```bash
docker compose down
```

不要把 `down -v` 当作停止或升级命令；它会删除 CoinSphere 数据卷。

## 3. 首次登录

初始超级管理员为 `coinsphere` / `coinsphere`。首次登录后立即在用户管理中修改密码，并完成以下检查：

1. 首页显示 Backend 和 PostgreSQL 正常。
2. 用户、角色和菜单管理页可打开。
3. 普通角色只获得需要的菜单和按钮权限。
4. 重启后原 `COINSPHERE_AUTH__SECRET_KEY` 仍保持一致。

系统不提供匿名注册。Access Token 只保存在当前前端会话内；退出登录会撤销当前令牌。

## 4. 当前页面

| 页面     | 用途                                                     |
| -------- | -------------------------------------------------------- |
| 首页     | 查看 Go 进程、HTTP、PostgreSQL、migration 和插件版本状态 |
| 用户管理 | 创建、停用和维护本地用户                                 |
| 角色管理 | 创建角色并分配菜单、按钮权限                             |
| 菜单管理 | 维护后台导航与权限码                                     |

旧行情、新闻、策略、工作流、通知和交易页面不属于当前 Web 运行面。工作流 P1-A 只有 `/api/v1` 管理接口，不恢复旧工作流页面或旧图格式。

## 5. 工作流生命周期 API

只有超级管理员可以使用 `/api/v1/workflows`。当前 `blank` 模板原子创建一个 `paused` 批处理工作流、初始不可变修订和唯一运行实例；生命周期支持 `start`、`pause`、`archive`。

`running` 当前不代表已经执行节点。P1-C 批次执行器尚未交付，定时触发、插件 Action、重试和历史也不可用。归档后工作流只读且不能重新启动。

具体请求、图格式和冲突语义见[公共契约](contracts/README.md)。生产 Web 工作台会在 P1-B/P1-D 交付，不要求管理员直接编辑原始 JSON。

## 6. 插件维护

插件会参与主 Go 进程和主 Vue 前端编译，只安装已经审查过的本地可信源码。维护前停止应用并备份数据库。

在 `backend` 目录先执行只读校验：

```powershell
go run ./cmd/coinsphere plugin validate D:\plugins\connector
```

校验通过后，在维护窗口使用：

```powershell
go run ./cmd/coinsphere plugin install --config ./config.yml --backend-root . D:\plugins\connector
go run ./cmd/coinsphere plugin upgrade --config ./config.yml --backend-root . D:\plugins\connector
go run ./cmd/coinsphere plugin uninstall --config ./config.yml --backend-root . official.connector
go run ./cmd/coinsphere plugin purge-data --config ./config.yml --backend-root . --confirm "PURGE official.connector" official.connector
```

- `install`/`upgrade` 执行插件 migration、生成注册表并构建 Backend/Web 镜像，但不启动候选镜像。
- 同 major 升级必须保留所有旧 migration 字节不变；major 升级当前拒绝。
- 有活动工作流、修订或结果视图引用时不能卸载。
- 卸载保留插件 schema；`purge-data` 只有在无任何活动或历史引用时才删除 schema。
- 同一 checkout 一次只运行一个插件维护命令。

仓库内 `plugins/contract-test` 仅用于 SDK 自动化验收，不应安装到生产环境，也不会出现在生产菜单。

## 7. 数据、备份与升级

持久数据位于 TimescaleDB 卷和上传目录。升级前至少保存：

- PostgreSQL 一致性备份及恢复命令。
- 上传目录备份。
- 当前应用版本、Compose 配置和镜像 digest。
- `COINSPHERE_AUTH__SECRET_KEY` 的安全离线副本。

升级使用最新固定版本镜像先运行 migration，再启动 Backend/Web。应用启动只校验 schema，不自动建表。失败时停止候选版本并按[发布 Runbook](runbooks/release.md)恢复匹配的应用镜像；不要手工改 migration 账本或自动执行 Down。

正式 Paper 观察尚未开始，因此 migration 历史尚未冻结。任何开发数据重置都必须按[数据库迁移 Runbook](runbooks/database-migrations.md)确认目标和备份，不得触及生产或需要保留的证据。

## 8. 健康检查与排障

```bash
curl --fail http://127.0.0.1:8080/health/live
curl --fail http://127.0.0.1:8080/health/ready
docker compose ps
docker compose logs --tail=200 migrate backend web
```

| 现象                     | 优先检查                                                |
| ------------------------ | ------------------------------------------------------- |
| Compose 提示缺少签名密钥 | 当前环境是否提供稳定的 `COINSPHERE_AUTH__SECRET_KEY`    |
| `migrate` 失败           | TimescaleDB 健康、核心版本、目标数据库是否包含旧 schema |
| Backend 不 ready         | PostgreSQL 网络、凭据、migration 是否落后或超前         |
| Web 打不开               | Web 端口、Backend 健康和反向代理配置                    |
| 登录立即失效             | 签名密钥是否变化、浏览器时间是否异常                    |
| 插件安装失败             | manifest、Core/SDK 版本、migration、Go/Vue 构建输出     |
| 插件无法卸载             | CLI 输出的活动引用，先在拥有模块解除引用                |
| 工作流返回 `409`         | 客户端修订指针已过期，或当前状态不允许该生命周期动作    |

日志和截图必须移除 DSN、Token、密码、原始载荷和个人数据。

## 9. 安全边界

- 不把真实 API Key、Secret、令牌、DSN 或生产配置提交到代码、日志、Issue、PR、CI 或 AI 上下文。
- 不安装不可信、远程下载或未经审查的插件源码。
- AI、工作流和通用 HTTP 节点不得调用交易所私有接口或绕过风控。
- 完成 P0 不会自动创建 Testnet/Live 阶段，也不会启用 Paper 或真实交易。

## 10. 文档索引

- [架构概览](architecture/overview.md)
- [公共契约](contracts/README.md)
- [V2 路线图](roadmap/README.md)
- [质量门禁](quality/quality-gates.md)
- [本地开发](runbooks/development.md)
- [数据库迁移](runbooks/database-migrations.md)
- [发布与回滚](runbooks/release.md)
