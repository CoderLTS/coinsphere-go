# Docker 发布包

该目录会进入 Docker Release 包，供独立 Compose 部署使用。当前生产服务器使用共享 DPanel Stack，由 `scripts/release/deploy-dpanel-stack.sh` 定向发布，不使用本目录脚本。

- 自动发布从已扫描的 `release-manifest.json` 读取 Backend、Web 和 Worker 的不可变 RepoDigest；手工恢复使用旧版本已扫描的 Manifest，不使用 `latest`。
- `deploy.sh` 默认在解压目录原地运行；仅在明确设置 `COINSPHERE_DEPLOY_DIR` 时复制到其他独立目录。
- `runtime.env` 只保存在服务器，禁止提交或上传到 GitHub Release。
- `runtime.env` 必须通过 `COINSPHERE_DATABASE__DSN` 指向外部 PostgreSQL/TimescaleDB；数据库备份与恢复由基础设施负责。
- `worker-runtime.env` 只包含 UTC 和 Worker 专用的 `COINSPHERE_WORKER_DATABASE_DSN`，不得放入认证、通知或交易所密钥。
- `executor-runtime.env` 只包含 UTC 和 Paper Executor 专用数据库身份；当前不得配置 Binance Testnet/Live 凭据。
- 部署脚本停止旧服务后执行镜像内 migration。失败时恢复上一 Compose 与镜像，但保留当前 schema，不自动执行 Down 或覆盖数据库。
- realtime/backtest 使用同一 Worker digest，均不暴露端口；只有 backtest 挂载独立产物卷并配置墙钟、CPU、内存和产物上限。
- 单一 PostgreSQL 基线建立完整业务 schema；应用启动只读校验版本，不执行 `AutoMigrate`。生产只部署 Paper Executor，Worker 与 Executor 均不具备 Binance Testnet/Live 私有接口能力。
- 运行容器不需要构建代理。`deploy.sh` 会拒绝 Docker 客户端 `config.json` 中的顶层 `proxies`；出站代理应只保留在 Runner 服务环境中，由发布构建脚本显式传给 BuildKit。Runner 的 `NO_PROXY`/`no_proxy` 必须覆盖本机 Registry、`127.0.0.1` 和 `localhost`。
