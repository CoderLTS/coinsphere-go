# Docker 发布包

该目录由手工 Release workflow 打包，并由生产 self-hosted Runner 部署到 DPanel Compose 目录。

- 自动发布从已扫描的 `release-manifest.json` 读取不可变 RepoDigest；手工恢复可继续按版本标签选择旧版本，不使用 `latest`。
- `runtime.env` 只保存在服务器，禁止提交或上传到 GitHub Release。
- `runtime.env` 必须通过 `COINSPHERE_DATABASE__DSN` 指向外部 PostgreSQL/TimescaleDB；数据库备份与恢复由基础设施负责。
- 部署脚本停止旧服务后执行镜像内 migration。失败时恢复上一 Compose 与镜像，但保留当前 schema，不自动执行 Down 或覆盖数据库。
- 单一 PostgreSQL 基线建立完整业务 schema；应用启动只读校验版本，不执行 `AutoMigrate`。生产暂不部署 Python Worker。
- 运行容器不需要构建代理。`deploy.sh` 会拒绝 Docker 客户端 `config.json` 中的顶层 `proxies`；出站代理应只保留在 Runner 服务环境中，由发布构建脚本显式传给 BuildKit。Runner 的 `NO_PROXY`/`no_proxy` 必须覆盖本机 Registry、`127.0.0.1` 和 `localhost`。
