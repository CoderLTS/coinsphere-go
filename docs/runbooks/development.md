# 本地开发与验证

## 本地快速验证

Windows 使用 PowerShell；脚本按现有工具验证 Go、Vue 和 Python Worker，本机存在 Docker 时才验证 Compose 配置：

```powershell
.\scripts\verify.ps1
```

Linux 使用：

```bash
./scripts/verify.sh
```

开发中优先运行受影响模块的最小命令，不在每个子步骤重复全仓验证。常用定向命令：

```powershell
Push-Location .\backend
go test -count=1 ./...
Pop-Location

Push-Location .\frontend
pnpm exec vitest run
pnpm exec playwright test --project=chromium
Pop-Location

Push-Location .\worker
uv run --frozen pytest
Pop-Location
```

Go 生命周期测试覆盖 `SIGINT`/`SIGTERM` 根 Context、HTTP、Runtime、数据库和 WebSocket 的 30 秒有界收尾。通知 WebSocket 定向契约覆盖单 writer、固定信封、连续序号、背压、Ping/Pong、失联超时、关闭、严格 Origin 和固定子协议鉴权；查询串 Token 被拒绝且不得进入日志。

本地运行后可用以下固定入口检查 A1 可观测性：

```powershell
Invoke-WebRequest http://127.0.0.1:6987/health/live
Invoke-WebRequest http://127.0.0.1:6987/health/ready
Invoke-WebRequest http://127.0.0.1:6987/metrics
```

`/health/live` 不访问数据库；`/health/ready` 与兼容 `/health` 执行有界 PostgreSQL Ping。`/metrics` 只有五个固定无标签进程指标，JSON 应用日志写标准输出。写请求审计通过 `request_id` 与日志关联；查询时不得把生产 DSN 或用户载荷拼入命令输出。

Worker 和数据库契约使用随机 PostgreSQL schema，覆盖 `FOR UPDATE SKIP LOCKED` 认领、数据库时间租约、fencing、过期恢复、尝试耗尽和取消。realtime/backtest 消费者按 lane 隔离；backtest Worker 还覆盖可信策略发布、受限子进程回测和内容寻址产物，且不包含真实交易能力。

本地缺少 Docker、WSL 或 PostgreSQL/TimescaleDB 时，将对应检查交给 GitHub Actions，或在统一环境主机的隔离目录和独立 Compose 项目中执行。

## 仅文档治理变更

只修改 `.gitignore`、`AGENTS.md` 和 `docs/**` 时，不运行应用测试或构建。以 PR base 为基线执行：

```powershell
$base = "origin/main" # stacked PR 改为实际父分支
git diff --check $base
git diff --name-only $base
git diff --quiet $base -- backend frontend worker .github deploy scripts docker-compose.yml
if ($LASTEXITCODE -ne 0) { throw "文档治理 PR 包含越界实现改动" }
```

随后只读检查相对链接、Mermaid 语法、术语、阶段依赖和权威文档归属；GitHub milestone、Issue 和 PR 状态直接在 GitHub 核对，不复制回仓库进度文档。

## PR 快速层

- Draft stacked PR 可以指向父分支；上游进入 `main` 后，下游变基到 `main` 并将 base 改为 `main`，才能标记 Ready。
- 密钥扫描始终运行；按变更路径选择模块，Go 后端模块运行 `go test -count=1 ./...`，其他模块运行格式、Lint、类型检查、构建和相关核心测试。
- 前端关键交互变更运行 Chromium 冒烟；依赖清单或锁文件变化时运行依赖扫描。
- 发布维护脚本变更运行 Bash 语法、ShellCheck 和脚本检查，不在普通 PR 构建容器。
- Migration、金融、安全、并发、恢复和外部契约变更必须运行对应定向检查；Bug 修复增加一个覆盖根因的回归检查。
- PR 模板记录实际命令和结果。快速层目标是尽快暴露本切片问题，不宣称覆盖完整矩阵。

## 波次完整层

根 Agent 从同一契约 base 建立临时集成分支，合入该波次所有切片头提交后执行一次：

- 全仓构建、核心测试、Go 竞态与生命周期检查。
- PostgreSQL/TimescaleDB Compose、migration、并发事务、恢复和跨模块集成。
- Chromium、Firefox、WebKit 关键浏览器冒烟。
- Backend、Frontend、Worker 镜像构建、健康检查和端到端冒烟。
- 一次最终只读复审。

在 GitHub Actions 中通过 `workflow_dispatch` 选择临时集成分支，即可启用完整层；普通功能 PR 不运行该层。

集成分支不直接交付，也不替代独立 PR。每个 PR 仍由用户审查并手工合并，禁止自动合并。

## `main` 与发布层

`main`、定时任务或发布运行完整跨模块回归和供应链扫描，包括依赖、源代码、文件系统、镜像、发布归档、SBOM 和校验和。失败时不得上传发布产物或部署。

生产部署只能由用户手工触发并通过受保护的 GitHub `production` Environment 审批。发布流程不得接触真实交易所密钥、自动执行真实交易或绕过权限、审计和风控。

## Migration

- Migration 默认跟随所属纵向能力，仅共享基线、破坏性变更或跨领域 schema 使用独立 PR。
- 正式 Paper 观察前可在重置未投产开发/CI 数据库的前提下整理历史 migration；开始记录 Paper 晋级证据前永久冻结，此后只能追加版本化 SQL。
- 验证 PostgreSQL 升级、必要的保数与约束、重复执行、失败原子性和回滚；测试使用随机隔离 schema，不清空固定外部表。
- 每个 migration PR 或含 migration 的能力 PR 必须写明回滚步骤。

具体命令、SQL 约束和回滚流程见[数据库迁移手册](./database-migrations.md)。

## 浏览器排障

Playwright 版本和浏览器修订号由 `pnpm-lock.yaml` 锁定。首次运行或锁定版本变化后安装浏览器：

```powershell
Push-Location .\frontend
pnpm install --frozen-lockfile
pnpm exec playwright install chromium firefox webkit
pnpm exec playwright test --project=chromium
pnpm exec playwright show-trace .\test-results\<失败用例>\trace.zip
Pop-Location
```

浏览器测试使用本地 Web Server 和后端路由 Mock，不访问公网、真实凭据或生产服务。Chromium/WebKit 不能替代 Edge、Safari 或 iOS 真机兼容性验收。

## 安全约束

- 本地开发只使用虚假或公开测试数据。
- 不把交易所密钥写入 `.env`、仓库脚本、终端截图或测试输出。
- 任何真实账户验证命令由用户在目标主机执行，并只反馈脱敏结果。
