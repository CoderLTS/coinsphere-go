# 本地开发与验证

## Windows

```powershell
.\scripts\verify.ps1
```

脚本会验证 Go、Vue 和 Python Worker；若本机存在 Docker，还会验证 Compose 配置。脚本优先使用 `PATH` 或 `GOROOT` 中的 Go，并兼容 `%USERPROFILE%\go\go1.26.5` 的本地登记路径。

迁移契约默认在临时 SQLite 数据库执行。要同时验证 PostgreSQL，先设置仅供本地测试的 DSN：

```powershell
$env:COINSPHERE_TEST_POSTGRES_DSN = 'postgres://coinsphere:test-only@127.0.0.1:5432/coinsphere_test?sslmode=disable'
Push-Location .\backend
go test -count=1 ./internal/migration ./cmd/migrate
Pop-Location
```

## Linux 与 CI

```bash
./scripts/verify.sh
```

GitHub Actions 负责 Linux、容器和安全检查。本地缺少 Docker 时可以继续开发，但 PR 在容器 Job 通过前不得合并。

CI 使用固定 PostgreSQL 17 镜像执行迁移契约。本地与发布环境的迁移命令、编写约束和回滚步骤见[数据库迁移手册](./database-migrations.md)。

## 安全约束

- 本地开发只使用虚假或公开测试数据。
- 不把交易所密钥写入 `.env`、仓库脚本、终端截图或测试输出。
- 任何真实账户验证命令由用户在目标主机执行，并只反馈脱敏结果。
