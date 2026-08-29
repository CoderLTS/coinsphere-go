# CoinSphere

CoinSphere 是以可视化工作流为核心、由编译期可信插件提供业务能力的个人自托管量化平台。当前系统采用 Vue 3、单实例 Go 模块化单体和 PostgreSQL 16；超级管理员可以运行批次、事件与连续流工作流，普通用户通过固定范围的共享结果完成观察与授权操作。

## 当前能力

| 能力                                 | 当前入口        | 状态                                              |
| ------------------------------------ | --------------- | ------------------------------------------------- |
| 登录、用户、角色、菜单               | Web             | 可用，不开放公开注册                              |
| 系统监控                             | Web + `/api/v1` | 可用，展示 Go、HTTP、PostgreSQL 和 migration 状态 |
| 本地插件校验、安装、升级和卸载       | CLI             | 可用，编译期静态注册                              |
| 工作流、修订、事件、批次和活动 API   | `/api/v1`       | 超级管理员可用；Webhook 使用独立 Secret           |
| Schema 工作台、人工任务和历史制品    | Web             | 可用；移动端提供只读活动视图                      |
| Connector、AI 与连续流               | 工作流节点      | 默认外部域名白名单为空，不含交易私有接口          |
| Quant 公共行情、策略、回测与 Paper   | 工作流 + 结果页 | 支持 Binance Spot/USD-M 公共接口，不含真实交易    |
| Notification 与共享结果             | 工作流 + 结果页 | 站内幂等投递、固定范围授权与移动端审批            |
| 旧工作流、新闻、策略、交易和通知接口  | -               | 已从公开运行面移除                                |

详细操作见[使用手册](docs/user-guide.md)，接口语义见[公共契约](docs/contracts/README.md)。

## 架构

```mermaid
flowchart LR
    WEB["Vue Web"] --> APP["单实例 Go App"]
    APP --> DB["PostgreSQL 16"]
    MIGRATE["一次性 migration"] --> DB
```

生产部署只包含 Web、Go App 和一次性 migration，连接服务器现有 PostgreSQL 16 的独立 `coinsphere_go` 数据库，不依赖 Redis、消息中间件或 Kubernetes。核心 schema 保存认证、RBAC、菜单、审计、插件生命周期、工作流执行历史和制品引用；压缩制品保存在 Backend 持久目录。

## 快速启动

推荐使用 Docker Compose。需要 Docker Engine 或 Docker Desktop，并支持 Compose v2。

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

浏览器打开 <http://localhost:8080>。初始超级管理员是 `coinsphere` / `coinsphere`；首次登录后立即在“用户管理”中修改密码。`COINSPHERE_AUTH__SECRET_KEY` 必须长期保持一致，变更后已有登录令牌会失效。

本地 Compose 会启动 `postgresql`、一次性 `migrate`、`backend` 和 `web`。停止服务不会删除数据：

```bash
docker compose down
```

完整安装、首次配置、备份和排障步骤见[使用手册](docs/user-guide.md)。

## 目录

```text
backend/             Go App、版本化 migration、工作流与系统模块
frontend/            Vue 3 + Vite Web
deploy/production/   生产 Compose 模板
docs/                架构、契约、代码/插件指南、质量门禁和 Runbook
scripts/             验证、发布和部署脚本
```

## 开发

本地工具链为 Go 1.26.6、Node.js 24、pnpm 10.33 和 PostgreSQL 16。全量验证：

```powershell
.\scripts\verify.ps1
```

```bash
./scripts/verify.sh
```

按模块启动与诊断见[本地开发手册](docs/runbooks/development.md)，数据库变更见[迁移手册](docs/runbooks/database-migrations.md)。

## 文档

- [使用手册](docs/user-guide.md)：安装、系统管理、插件、备份、升级和排障
- [当前架构](docs/architecture/overview.md)：系统边界、组件职责、状态流与数据所有权
- [代码结构](docs/code-structure.md)：目录、模块职责和常见修改入口
- [插件开发指南](docs/plugin-development.md)：manifest、SDK、Vue、migration、测试和生命周期
- [公共契约](docs/contracts/README.md)：`/api/v1`、插件 SDK 和生命周期语义
- [质量门禁](docs/quality/quality-gates.md)：测试与验收要求
- [发布与回滚](docs/runbooks/release.md)：手工发布、固定 digest 部署和回滚
- [Paper 恢复与观察](docs/runbooks/paper-recovery.md)：重启、积压、账本重建与观察证据

## 安全边界

- 不把真实 API Key、Secret、令牌、DSN 或生产配置提交到仓库、日志、Issue、PR 或 AI 上下文。
- Codex、CI 和工作流不接触真实交易密钥，不启用 Live 开关，也不解除全局急停。
- 工作流和通用 HTTP 节点不能调用交易所私有接口、创建交易命令或绕过风控。
- 新交易能力默认关闭；缺少完整风控、匹配对账、管理员授权或 Owner 手工放行时保持禁用。
- 个人部署应优先使用 Paper。任何私有交易能力必须先建立独立 ADR、安全边界、观察证据和用户手工放行。
