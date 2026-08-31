# 发布与回滚

## 当前拓扑

生产默认使用独立 Compose 项目 `coinsphere-go`：

- `postgresql`：服务器数据栈中的共享 PostgreSQL 16；CoinSphere 独占 `coinsphere_go` 数据库和用户。
- `backend`：内置 Vue 静态产物，同时提供认证、RBAC、工作流执行、系统管理和监控 API；文件位于部署目录 `data/backend`。
- 宿主 Nginx：服务器现有入口，反向代理到 Compose 暴露的 `127.0.0.1:8080`，不运行 CoinSphere Web 容器。

Migration 由目标 Backend 镜像中的 `coinsphere-migrate` 在启动服务前一次性执行，不是常驻服务。Python Worker、Worker 卷和 Private Executor 不属于当前部署拓扑。

CoinSphere 不再部署到 sub2api 或其他应用的 Compose 项目。发布只操作旧的 CoinSphere 容器和新的 `coinsphere-go` 项目，不执行其他项目的 `down`，也不删除旧数据库或数据目录。

## 服务器准备

自托管 Runner 需要 Docker Compose v2、`jq`、`curl`、`openssl` 和本机 Registry。宿主 Nginx 必须已把站点请求反向代理到 `127.0.0.1:8080`；自定义端口时同步设置 `COINSPHERE_WEB_PORT` 和 Nginx upstream。部署目录按以下顺序确定：

- `COINSPHERE_DEPLOY_DIR`：独立部署目录。
- `COINSPHERE_STACK_ROOT`：部署脚本使用其 `compose/coinsphere-go` 子目录，并读取既有 CoinSphere runtime Secret。
- 未设置变量时，脚本只从 Docker Compose 标签定位已经存在的独立项目。

独立目录必须包含权限为 `0600` 的 `runtime.env`。外部 `dpanel_stack` 网络、`postgresql` 服务以及独立 `coinsphere_go` 数据库和用户必须已存在；`.env` 中的数据库密码与该用户一致。脚本可从 `COINSPHERE_STACK_ROOT/secrets/coinsphere-runtime.env` 初始化 runtime Secret；手工部署则按 `deploy/production/runtime.env.example` 创建。真实 DSN、令牌和密钥不得进入仓库、Actions 日志、Issue 或 PR。

## 发布流程

1. 目标 PR 合并到最新 `main`，CI 和最终只读复审通过。
2. 涉及 Paper 时，按[数据库迁移手册](database-migrations.md)保存一致备份；只有首次进入正式 Paper 观察时，才在发布记录中把目标 commit 记为 migration freeze 提交。
3. 在 GitHub Actions 手工运行 `Release and deploy`，输入未使用的 `vX.Y.Z`。
4. 工作流构建并推送包含前后端的应用镜像固定 digest。
5. `deploy/production/deploy.sh` 拉取镜像，停止 CoinSphere 旧服务，并对共享 PostgreSQL 执行目标镜像内 migration。
6. 脚本启动单应用容器，检查容器健康和 `/health`；全部通过后保存新的 `.env`。

直接在已扫描 Manifest 上手工执行同一部署器：

```bash
bash deploy/production/deploy.sh vX.Y.Z /path/to/release-manifest.json
```

仅部署、不创建 GitHub Release 时，在 Actions 手工运行 `Deploy`，使用不会与正式 Tag 冲突的版本号，例如 `v0.2.0-deploy.1`。

## 首次独立部署

首次独立部署创建 `data/backend`，并在空的 `coinsphere_go` 数据库应用当前 migration。后续部署默认只执行 Up，不自动重置数据库、执行 Down 或删除数据；任何破坏性基线变更必须按独立 migration PR 和数据库 Runbook 获得明确授权。

部署成功后确认：

```bash
docker compose --project-name coinsphere-go --project-directory "$COINSPHERE_DEPLOY_DIR" \
  --env-file "$COINSPHERE_DEPLOY_DIR/.env" -f "$COINSPHERE_DEPLOY_DIR/compose.yaml" ps
curl -fsS http://127.0.0.1:8080/health
```

旧 CoinSphere 数据库容器应不存在，服务器共享 PostgreSQL、sub2api 和其他服务保持运行。旧数据库目录先保留为人工回滚备份，确认共享 PostgreSQL 备份可恢复后再由用户单独安排清理。

## 失败与回滚

- 镜像、Manifest 或扫描失败发生在停服务前，不改变运行环境。
- 首次独立部署失败时，脚本停止候选项目并恢复此前实际运行的旧 CoinSphere 服务。
- 后续部署失败时，脚本恢复上一份独立 Compose、固定 digest 和 `.env`。
- 回滚不执行 migration Down、不修改版本表，也不删除数据库、上传或制品目录。

若上一应用版本与当前 schema 不兼容，保持服务停止并使用已验证备份恢复；禁止手工伪造 migration 版本或删除交易事实。

## 安全边界

当前部署不包含 Testnet、Live 或交易所私有接口。发布、CI、Codex 和工作流不得提供真实交易凭据、自动下单、打开交易开关或解除急停。

部署后的 Paper 重启、积压与账本验证按 [Paper 恢复与观察](paper-recovery.md)执行；发布成功不自动开始正式 Paper 观察。
