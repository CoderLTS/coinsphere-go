# 发布与回滚

## 当前边界

生产发布目前只构建和部署 Backend/Web。Python Worker 和 Go Executor 尚未进入生产拓扑，因此当前发布不代表 Paper、Testnet 或 Live 交易能力已交付，也不能据此启用真实策略。

生产 Backend 通过主机 Secret 连接外部 PostgreSQL/TimescaleDB；应用启动只读校验 migration 版本。部署脚本停止 CoinSphere 服务、执行目标镜像内的 Up、启动固定 digest 镜像并检查健康状态，不自动执行 Down 或覆盖数据库。

发布只能由用户从最新 `main` 手工触发。GitHub Actions、Codex、普通应用角色和工作流不得接触真实交易所密钥、发起真实订单或解除急停。当前私有仓库套餐不提供强制 Environment reviewer，`workflow_dispatch`、`main` 校验和用户审查是现行门禁。

## 生产位置

- Runner 标签：`self-hosted`、`Linux`、`X64`、`coinsphere-release`、`production`；工作目录：`/home/lts/actions-runner`。
- 本机 Registry：`127.0.0.1:5000`。
- DPanel 根目录：`/srv/dpanel-stack`；共享 Compose 项目为 `apps`，文件为 `compose/apps/docker-compose.yaml`，网络为 `dpanel_stack`。
- 插值配置：`/srv/dpanel-stack/secrets/apps.env`；运行配置：`/srv/dpanel-stack/secrets/coinsphere-runtime.env`；数据目录：`/srv/dpanel-stack/data/coinsphere-go`。
- CoinSphere 只能操作 `coinsphere-backend` 和 `coinsphere-web`，不得执行共享项目级 `down` 或操作其他服务。

以上路径和凭据只存在于服务器，不进入仓库、日志、Issue、PR 或 Release。生产数据库备份、恢复和保留策略由基础设施负责。

## 手工发布

1. 确认目标 PR 已合并到最新 `main`，CI、安全检查和迁移说明已通过；涉及数据库时先创建并验证 PostgreSQL 备份。
2. 在 GitHub Actions 手工运行 `Release and deploy`，选择 `main` 和新版本号 `vX.Y.Z`。
3. 工作流确认 Commit 等于最新 `origin/main`，且版本标签不存在。
4. 构建固定版本的 Windows/Linux/Compose 包和 Backend/Web 镜像，记录 Manifest、RepoDigest、SBOM 和 SHA-256。
5. 镜像、归档和 Manifest 扫描通过后才上传 Artifact、部署或创建 GitHub Release；扫描失败不得继续。
6. 用已扫描的 Manifest 执行部署脚本：

   ```bash
   bash scripts/release/deploy-dpanel-stack.sh vX.Y.Z /path/to/release-manifest.json
   ```

7. 检查 `coinsphere-backend`、`coinsphere-web` 状态以及 `http://127.0.0.1:8080/health`；健康检查通过后才认为发布完成。

Windows/Linux 包内的 Web 目录需要 Nginx 或等价 Web Server 托管，并反向代理到 Backend；它们不是桌面应用。

## 失败与回滚

扫描失败发生在部署前，不需要服务回滚；停止后续上传和部署即可。构建阶段写入 Registry 的候选标签按保留策略处理，不自动删除可能被其他标签引用的 Manifest。

Migration、启动或健康检查失败时，部署脚本会停止新版本、恢复上一份 CoinSphere 镜像环境文件并重新启动上一固定 digest。它保留当前 PostgreSQL schema，不执行 Down、不修改 `schema_migrations`、不清空业务数据。

若上一版本与新 schema 不兼容，保持服务停止，按[数据库迁移手册](./database-migrations.md)使用发布前备份恢复；禁止用手工改版本表或删除业务行“适配”旧代码。任何回滚都记录失败版本、时间线、备份标识、migration 版本、健康检查和恢复结果。

## 手工恢复

先保持服务停止并确认当前版本：

```bash
cd /srv/dpanel-stack/compose/apps
docker compose --project-name apps --env-file /srv/dpanel-stack/secrets/apps.env \
  -f docker-compose.yaml ps coinsphere-backend coinsphere-web
coinsphere_backend_image=$(sed -n 's/^COINSPHERE_BACKEND_IMAGE=//p' \
  /srv/dpanel-stack/secrets/apps.env)
docker run --rm --network dpanel_stack \
  --env-file /srv/dpanel-stack/secrets/coinsphere-runtime.env \
  "$coinsphere_backend_image" /app/coinsphere-migrate -config /app/config.yml -direction version
```

恢复 Registry 中仍存在的上一版本时，使用该版本已扫描的 Release Manifest 和同一双参数部署脚本；不要按标签猜测 digest。部署成功后再更新服务器上的镜像环境文件。

管理员首次登录密码只在 SSH 终端读取并立即修改，随后从服务器 Secret 文件移除；不得发送到聊天、Issue、PR 或 Actions 日志。
