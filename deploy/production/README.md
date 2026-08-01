# Docker 发布包

该目录由手工 Release workflow 打包，并由生产 self-hosted Runner 部署到 DPanel Compose 目录。

- 镜像固定为版本标签，不使用 `latest`。
- `runtime.env` 只保存在服务器，禁止提交或上传到 GitHub Release。
- 部署前停止旧服务并备份 SQLite 数据卷；migration 或健康检查失败时恢复数据备份和上一版本镜像。
- 当前 A0 仍保留应用启动时的 GORM AutoMigrate，版本化 migration 只提供机制基线，不能替代 A1 的业务 schema 切换。
