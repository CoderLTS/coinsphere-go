# 发布与回滚

## 当前边界

A0 当前生产 Compose 仍使用 SQLite，版本化 migration 只建立机制基线，应用启动仍保留 GORM `AutoMigrate`。A1 完成业务 schema 切换前，本流水线只用于现有管理平台，不得据此启用模拟盘或实盘交易能力。

发布允许 Codex 和 GitHub Actions 连接生产主机，但不得接触真实交易所密钥或发起真实订单。生产发布必须由用户从 `main` 手工触发；PR、push 和定时任务不会使用生产 Runner。

## 生产基础设施

- Runner：`coinsphere-production`，标签为 `self-hosted`、`Linux`、`X64`、`coinsphere-release`、`production`。
- Registry：`127.0.0.1:5000`，Runner 使用服务器本地 `coinsphere-ci` 登录信息，GitHub 不保存 Registry 密码。
- DPanel Compose：`/home/infrastructure/dpanel/compose/coinsphere-go`。
- Web 健康检查：生产主机 `http://127.0.0.1:8080/health`。
- 生产运行配置：部署目录的 `runtime.env`，权限固定为 `0600`，不会进入仓库、日志或 Release。
- 出站代理只配置在 Runner 服务环境中；Action 下载和其他出站工具仍会继承该环境。`build.sh` 统一大小写变量，并仅通过 BuildKit 预定义构建参数把代理传入构建步骤，镜像和运行容器不保存代理配置。
- `${DOCKER_CONFIG:-$HOME/.docker}/config.json` 禁止包含顶层 `proxies`。发布前置检查发现该配置时会在调用 Docker 前终止，避免代理自动注入生产容器。
- Runner 的 `NO_PROXY`/`no_proxy` 必须至少包含 `127.0.0.1`、`localhost` 和本机 Registry 地址，确保推送、部署及健康检查不经过出站代理。

仓库当前为 GitHub 私有仓库，现有套餐不支持 Branch Protection 或 Environment required reviewers。`production` Environment 已用自定义部署分支策略限制为 `main`，工作流还会校验最新 `origin/main`；当前人工门禁由 `workflow_dispatch` 和用户不直接推送 `main` 的流程约束保证。如需 GitHub 强制 PR 审查或“触发后再审批”，必须升级支持私有仓库保护规则的套餐。

## 手工发布

1. 确认目标 PR 已由用户合并到 `main`，且 CI、安全检查和迁移说明通过。
2. 在 GitHub Actions 打开 `Release and deploy`，选择 `main`，输入符合 `vX.Y.Z` 的新版本号并手工运行。
3. 流水线确认执行 Commit 是最新 `origin/main`，且版本标签不存在。
4. 专用 Runner 构建并上传三个发布包：Windows x86、Linux amd64 和 Docker Compose；同时生成双镜像 SPDX JSON SBOM、`release-manifest.json` 与 `SHA256SUMS`。
5. 后端和前端镜像分别推送版本标签与 `sha-<commit>` 标签到私有 Registry，禁止使用 `latest`。
6. 部署脚本停止旧服务、备份 SQLite 数据卷、执行 migration、启动固定版本镜像并检查健康状态。
7. 部署成功后才创建 GitHub Release；失败的候选产物仅在 Actions Artifact 保留 14 天，不会创建版本标签。
8. Release 创建后，Registry 对 backend/web 分别保留最近 10 个版本和当前部署版本，并删除对应的本地旧镜像标签。
9. 无论发布成功或失败，最终步骤都会删除 `dist`、清理超过 24 小时的 Runner 临时文件、清理 CoinSphere 专用 Builder 中超过 7 天的缓存并停止 Builder 容器。

Windows/Linux 包不是桌面应用：包内后端二进制可直接运行，`web` 目录需要 Nginx 或等价 Web Server 托管并反向代理到后端。

## 持久型 Runner 清理

- CoinSphere 使用独立的 `coinsphere-release` Buildx Builder，并通过宿主机网络和项目内 BuildKit 镜像源配置复用服务器现有出站链路；缓存清理不会操作其他项目的 Builder。
- Go 模块、Go 编译结果和 pnpm store 使用 CoinSphere 专用 BuildKit cache mount；发布版本号变化只会重建前端产物，不会使依赖安装层失效。缓存仍在保留期内时，依赖锁文件变化只下载缺失内容。
- Runner 的 `HTTP_PROXY`、`HTTPS_PROXY` 和 `NO_PROXY`（或对应小写变量）供发布作业的出站工具使用；只有构建脚本会把这些变量显式转交给 BuildKit。不得通过 Docker 客户端全局 `proxies` 为 Builder 配置代理，因为该配置也会注入 Compose 运行容器。
- `scripts/release/cleanup-runner.sh` 只删除当前仓库的 `dist`、过期的 `RUNNER_TEMP` 内容和 CoinSphere Builder 缓存，不执行 `docker system prune`，不删除容器、数据卷或其他仓库镜像。
- `scripts/release/prune-registry.sh` 只允许访问 `127.0.0.1:5000`，默认仅预演；发布工作流显式传入 `--apply`。认证复用 Runner 本地 Docker 登录配置，凭据不会进入命令参数或日志。
- Registry Manifest 删除后，未引用 Blob 仍需在独立维护窗口执行 Registry garbage collection。共享 Registry 运行期间禁止自动执行 GC，避免与其他项目推送并发造成数据损坏。
- Action 下载目录、工具缓存和 Runner 诊断日志保留给 Runner 自身管理；GitHub Artifact 按 14 天策略由 GitHub 清理，GitHub Release 长期保留。

## 自动回滚

migration、Compose 启动或 `/health` 任一步失败时，`deploy.sh` 会：

1. 停止失败版本。
2. 删除失败版本使用的 SQLite 数据卷，并从部署前 tar 备份恢复；首次部署前没有数据卷时直接清理新卷。
3. 恢复上一份 Compose 和镜像版本文件。
4. 拉取并重新启动上一固定版本。

脚本保留最近 10 份 SQLite 备份。它不会执行 migration Down，也不会修改 `schema_migrations` 伪造回滚。

## 手工恢复

自动回滚失败时，先保持服务停止，再检查：

```bash
cd /home/infrastructure/dpanel/compose/coinsphere-go
docker compose --env-file .env -f compose.yaml ps
ls -lt backups/
```

恢复到 Registry 中仍存在的上一版本，可在部署目录执行：

```bash
./deploy.sh vX.Y.Z
```

管理员初始密码由服务器首次准备时随机生成。需要首次登录时，只在 SSH 终端读取 `runtime.env`，登录并改密后从该文件移除 `COINSPHERE_AUTH__BOOTSTRAP_ADMIN_PASSWORD`；不要把值发送到聊天、Issue、PR 或 Actions 日志。

任何回滚都要记录失败版本、时间线、备份文件、健康检查和恢复结果。交易能力落地后，发布前还必须先停止新增敞口并按交易应急手册处理活动订单。
