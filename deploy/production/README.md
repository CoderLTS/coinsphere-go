# Docker 发布包

该目录由手工 Release workflow 打包，并由生产 self-hosted Runner 部署到 DPanel Compose 目录。

- 自动发布从已扫描的 `release-manifest.json` 读取不可变 RepoDigest；手工恢复可继续按版本标签选择旧版本，不使用 `latest`。
- `runtime.env` 只保存在服务器，禁止提交或上传到 GitHub Release。
- 部署前停止旧服务并备份 SQLite 数据卷；migration 或健康检查失败时恢复数据备份和上一版本镜像。
- 当前仍保留应用启动时的 GORM AutoMigrate；版本化 migration 已包含机制基线和未启用消费的 `worker_tasks`，不能替代 A1 后续的完整业务 schema 切换。
- 运行容器不需要构建代理。`deploy.sh` 会拒绝 Docker 客户端 `config.json` 中的顶层 `proxies`；出站代理应只保留在 Runner 服务环境中，由发布构建脚本显式传给 BuildKit。Runner 的 `NO_PROXY`/`no_proxy` 必须覆盖本机 Registry、`127.0.0.1` 和 `localhost`。
