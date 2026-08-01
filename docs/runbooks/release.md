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

仓库当前为 GitHub 私有仓库，现有套餐不支持 Branch Protection 或 Environment required reviewers。`production` Environment 已用自定义部署分支策略限制为 `main`，工作流还会校验最新 `origin/main`；当前人工门禁由 `workflow_dispatch` 和用户不直接推送 `main` 的流程约束保证。如需 GitHub 强制 PR 审查或“触发后再审批”，必须升级支持私有仓库保护规则的套餐。

## 手工发布

1. 确认目标 PR 已由用户合并到 `main`，且 CI、安全检查和迁移说明通过。
2. 在 GitHub Actions 打开 `Release and deploy`，选择 `main`，输入符合 `vX.Y.Z` 的新版本号并手工运行。
3. 流水线确认执行 Commit 是最新 `origin/main`，且版本标签不存在。
4. 专用 Runner 构建并上传三个发布包：Windows x86、Linux amd64 和 Docker Compose；同时生成双镜像 SPDX JSON SBOM、`release-manifest.json` 与 `SHA256SUMS`。
5. 后端和前端镜像分别推送版本标签与 `sha-<commit>` 标签到私有 Registry，禁止使用 `latest`。
6. 部署脚本停止旧服务、备份 SQLite 数据卷、执行 migration、启动固定版本镜像并检查健康状态。
7. 部署成功后才创建 GitHub Release；失败的候选产物仅在 Actions Artifact 保留 14 天，不会创建版本标签。

Windows/Linux 包不是桌面应用：包内后端二进制可直接运行，`web` 目录需要 Nginx 或等价 Web Server 托管并反向代理到后端。

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
