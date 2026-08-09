# 发布与回滚

## 当前边界

生产发布构建和部署 Backend/Web、realtime/backtest 两个 Python Worker以及 Paper Executor。Worker 只消费 PostgreSQL 任务、产生信号和回测产物；当前生产配置保持 `trading.testnet_private_api_enabled=false`，因此 Executor 只消费 Paper 意图并生成追加事件与投影，不调用交易所私有接口。Testnet 凭据验证、首次对账和确定性主市价单代码已存在但尚未进入生产放行；保护单与持续权威对账尚未交付，Live 私有访问仍不可用。

生产 Backend 通过主机 Secret 连接外部 PostgreSQL/TimescaleDB；应用启动只读校验 migration 版本。部署脚本停止 CoinSphere 服务、执行目标镜像内的 Up、启动固定 digest 镜像并检查健康状态，不自动执行 Down 或覆盖数据库。

发布只能由用户从最新 `main` 手工触发。GitHub Actions、Codex、普通应用角色和工作流不得接触真实交易所密钥、发起真实订单或解除急停。当前私有仓库套餐不提供强制 Environment reviewer，`workflow_dispatch`、`main` 校验和用户审查是现行门禁。

## 生产位置

- Runner 标签：`self-hosted`、`Linux`、`X64`、`coinsphere-release`、`production`；工作目录：`/home/lts/actions-runner`。
- 本机 Registry：`127.0.0.1:5000`。
- DPanel 根目录：`/srv/dpanel-stack`；共享 Compose 项目为 `apps`，文件为 `compose/apps/docker-compose.yaml`，网络为 `dpanel_stack`。
- 插值配置：`/srv/dpanel-stack/secrets/apps.env`；运行配置：`/srv/dpanel-stack/secrets/coinsphere-runtime.env`；数据目录：`/srv/dpanel-stack/data/coinsphere-go`。
- Worker 运行配置单独保存在 `/srv/dpanel-stack/secrets/coinsphere-worker-runtime.env`；该文件只包含 Worker 专用 PostgreSQL DSN。
- Paper Executor 运行配置单独保存在 `/srv/dpanel-stack/secrets/coinsphere-executor-runtime.env`；该文件只包含 `TZ=UTC` 和 Executor 专用 PostgreSQL DSN，不得放入交易所凭据。
- CoinSphere 只能操作 `coinsphere-backend`、`coinsphere-web`、`coinsphere-worker`、`coinsphere-worker-backtest` 和 `coinsphere-executor`，不得执行共享项目级 `down` 或操作其他服务。

以上路径和凭据只存在于服务器，不进入仓库、日志、Issue、PR 或 Release。生产数据库备份、恢复和保留策略由基础设施负责。

## 手工发布

1. 确认目标 PR 已合并到最新 `main`，CI、安全检查和迁移说明已通过；涉及数据库时先创建并验证 PostgreSQL 备份。
2. 在 GitHub Actions 手工运行 `Release and deploy`，选择 `main` 和新版本号 `vX.Y.Z`。
3. 工作流确认 Commit 等于最新 `origin/main`，且版本标签不存在。
4. 构建固定版本的 Windows/Linux/Compose 包和 Backend/Web/Worker 镜像，记录 Manifest、RepoDigest、三份 SBOM 和 SHA-256。
5. 镜像、归档和 Manifest 扫描通过后才上传 Artifact、部署或创建 GitHub Release；扫描失败不得继续。
6. 用已扫描的 Manifest 执行部署脚本：

   ```bash
   bash scripts/release/deploy-dpanel-stack.sh vX.Y.Z /path/to/release-manifest.json
   ```

7. 检查五个 CoinSphere 服务状态，运行两个 Worker 的 `health`，确认 Executor 日志只包含启动/脱敏运行状态且数据库全局急停仍为开启或用户预期状态；再检查 `http://127.0.0.1:8080/health`，全部通过后才认为发布完成。

### 首次加入 Worker

在第一次把 Worker 交给共享 DPanel Stack 前，由用户手工完成一次配置准备：

- 将 `deploy/production/compose.yaml` 中 Worker 的只读文件系统、capability drop、`no-new-privileges`、专用产物卷和 backtest 资源上限同步到共享 Compose，并使用服务名 `coinsphere-worker`、`coinsphere-worker-backtest`。
- 在 `coinsphere-worker-runtime.env` 中只设置 `COINSPHERE_WORKER_DATABASE_DSN` 和 `TZ=UTC`；该 DSN 使用只授予 Worker 所需表权限的数据库身份。
- 确认共享 Compose 使用 `COINSPHERE_WORKER_IMAGE`，并让 Worker 服务加入已有基础设施网络且不发布端口。首次发布时 `apps.env` 可以暂时没有该键，部署脚本会在临时环境中补齐；成功后才写入已扫描的 Worker digest。

首次 Worker 发布失败时脚本只恢复原有 Backend/Web；Worker 容器和回测产物卷保持停止或保留，不删除任务、产物或数据库 schema。后续发布会恢复此前已成功接入的全部 CoinSphere 服务；尚未成功接入的 Worker 或 Executor 不得被当作可回滚基线。

### 首次加入 Paper Executor

在第一次把 Paper Executor 交给共享 DPanel Stack 前，由用户手工完成一次配置准备：

- 将 `deploy/production/compose.yaml` 中 Executor 的只读文件系统、capability drop、`no-new-privileges` 和无端口配置同步到共享 Compose，服务名固定为 `coinsphere-executor`，镜像复用 `COINSPHERE_BACKEND_IMAGE`。
- 创建 `/srv/dpanel-stack/secrets/coinsphere-executor-runtime.env`，只设置 `TZ=UTC` 和 `COINSPHERE_DATABASE__DSN`；数据库身份只授予读取执行输入以及写入 Paper 意图状态、事件和投影所需权限。
- 在首次启动前确认 migration 已应用、全局急停保持开启、所有 Paper 账户保持暂停。部署完成不等于允许 Paper 自动化，账户恢复、管理员授权和用户开关仍需分别手工执行。

首次接入失败时脚本停止候选 Executor 并恢复此前已部署的服务。数据库 schema、`trading_events` 和现有投影全部保留，不执行 Down；若投影不一致，修复后由 Executor 从追加事件重建，禁止删除事件来回滚。

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
  -f docker-compose.yaml ps coinsphere-backend coinsphere-web coinsphere-worker coinsphere-worker-backtest coinsphere-executor
coinsphere_backend_image=$(sed -n 's/^COINSPHERE_BACKEND_IMAGE=//p' \
  /srv/dpanel-stack/secrets/apps.env)
docker run --rm --network dpanel_stack \
  --env-file /srv/dpanel-stack/secrets/coinsphere-runtime.env \
  "$coinsphere_backend_image" /app/coinsphere-migrate -config /app/config.yml -direction version
```

恢复 Registry 中仍存在的上一版本时，使用该版本已扫描的 Release Manifest 和同一双参数部署脚本；不要按标签猜测 digest。部署成功后再更新服务器上的镜像环境文件。

管理员首次登录密码只在 SSH 终端读取并立即修改，随后从服务器 Secret 文件移除；不得发送到聊天、Issue、PR 或 Actions 日志。
