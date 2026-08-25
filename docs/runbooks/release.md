# 发布与回滚

## 当前拓扑

生产默认使用独立 Compose 项目 `coinsphere-go`：

- `timescaledb`：CoinSphere 独立 PostgreSQL/TimescaleDB 和持久卷。
- `backend`：认证、RBAC、工作流执行、系统管理和监控 API，以及独立上传、静态资源和制品卷。
- `web`：唯一对外 Web 入口。

Migration 由目标 Backend 镜像中的 `coinsphere-migrate` 在启动服务前一次性执行，不是常驻服务。Python Worker、Worker 卷和 Private Executor 不属于 V2 部署拓扑。

CoinSphere 不再部署到 sub2api 或其他应用的 Compose 项目。发布只操作旧的 CoinSphere 容器和新的 `coinsphere-go` 项目，不执行其他项目的 `down`，也不删除旧数据库或数据卷。

## 服务器准备

自托管 Runner 需要 Docker Compose v2、`jq`、`curl`、`openssl` 和本机 Registry。部署目录按以下顺序确定：

- `COINSPHERE_DEPLOY_DIR`：独立部署目录。
- `COINSPHERE_STACK_ROOT`：部署脚本使用其 `compose/coinsphere-go` 子目录，并在首次迁移时读取既有 CoinSphere Secret 和共享 Compose 位置。
- 未设置变量时，脚本从 Docker Compose 标签定位已经存在的独立项目；首次迁移则定位旧 CoinSphere Backend 所在的共享项目。

独立目录必须包含权限为 `0600` 的 `runtime.env`。首次由旧部署迁移时，脚本会复制既有 CoinSphere runtime Secret；手工部署则按 `deploy/production/runtime.env.example` 创建。真实 DSN、令牌和密钥不得进入仓库、Actions 日志、Issue 或 PR。

## 发布流程

1. 目标 PR 合并到最新 `main`，CI 和最终只读复审通过。
2. 涉及 Paper 时，按[数据库迁移手册](database-migrations.md)保存一致备份，并在发布记录中把目标 commit 记为 migration freeze 提交。
3. 在 GitHub Actions 手工运行 `Release and deploy`，输入未使用的 `vX.Y.Z`。
4. 工作流构建 Backend/Web 固定 digest，生成 SBOM 和校验文件，并完成 CRITICAL 扫描。
5. `deploy/production/deploy.sh` 拉取镜像，启动独立 TimescaleDB，执行目标镜像内 migration。
6. 首次迁移时，脚本只停止并移除旧共享 Compose 中实际运行的 CoinSphere 服务，然后启动独立项目。
7. 脚本检查 TimescaleDB、Backend、Web 和 `/health`；全部通过后保存新的 `.env`。

直接在已扫描 Manifest 上手工执行同一部署器：

```bash
bash deploy/production/deploy.sh vX.Y.Z /path/to/release-manifest.json
```

仅部署、不创建 GitHub Release 时，在 Actions 手工运行 `Deploy`，使用不会与正式 Tag 冲突的版本号，例如 `v0.2.0-deploy.1`。

## 首次独立部署

首次独立部署创建 `coinsphere-go-timescale-data`、`coinsphere-go-backend-artifacts`、上传和静态资源卷，并在空库应用当前 migration。后续部署只执行 Up，不自动重置数据库、执行 Down 或删除数据卷；V2 破坏性基线必须按独立 migration PR 和数据库 Runbook 执行。数据库和制品卷必须使用同一恢复点备份，避免保留引用指向缺失正文。

部署成功后确认：

```bash
docker compose --project-name coinsphere-go --project-directory "$COINSPHERE_DEPLOY_DIR" \
  --env-file "$COINSPHERE_DEPLOY_DIR/.env" -f "$COINSPHERE_DEPLOY_DIR/compose.yaml" ps
curl -fsS http://127.0.0.1:8080/health
```

旧 CoinSphere 容器应不存在，sub2api 和其他服务保持运行。旧数据库和卷先保留，确认不再需要后再由用户单独安排清理。

## 失败与回滚

- 镜像、Manifest 或扫描失败发生在停服务前，不改变运行环境。
- 首次独立部署失败时，脚本停止候选项目并恢复此前实际运行的旧 CoinSphere 服务。
- 后续部署失败时，脚本恢复上一份独立 Compose、固定 digest 和 `.env`。
- 回滚不执行 migration Down、不修改版本表，也不删除数据库、上传或制品卷。

若上一应用版本与当前 schema 不兼容，保持服务停止并使用已验证备份恢复；禁止手工伪造 migration 版本或删除交易事实。

## 安全边界

V2 部署不包含 Testnet、Live 或交易所私有接口。发布、CI、Codex 和工作流不得提供真实交易凭据、自动下单、打开交易开关或解除急停。

部署后的 Paper 重启、积压与账本验证按 [Paper 恢复与观察](paper-recovery.md)执行；发布成功不自动开始正式 Paper 观察。
