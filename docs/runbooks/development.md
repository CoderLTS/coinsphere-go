# 本地开发与诊断

## 全量验证

Windows 使用 PowerShell：

```powershell
.\scripts\verify.ps1
```

Linux 使用：

```bash
./scripts/verify.sh
```

脚本会验证 Go 与 Vue，并在本机存在 Docker 时检查 Compose 配置。缺少 PostgreSQL/TimescaleDB、Docker 或浏览器依赖时，按[质量门禁](../quality/quality-gates.md)把对应检查交给 CI。

## 定向命令

开发中先运行受影响模块的最小检查：

```powershell
Push-Location .\backend
go test -count=1 ./...
Pop-Location

Push-Location .\frontend
pnpm exec vitest run
pnpm exec playwright test --project=chromium
Pop-Location

```

需要数据库的 Go 测试使用仅供测试的 PostgreSQL DSN 和随机隔离 schema；不要连接生产数据库或固定外部表。迁移命令和回滚见[数据库迁移手册](./database-migrations.md)。

## 运行时诊断

启动 Backend 后检查：

```powershell
Invoke-WebRequest http://127.0.0.1:6987/health/live
Invoke-WebRequest http://127.0.0.1:6987/health/ready
Invoke-WebRequest http://127.0.0.1:6987/metrics
```

`/health/live` 只表示进程能响应；`/health/ready` 和兼容 `/health` 在一秒预算内 Ping PostgreSQL。`/metrics` 只包含固定的无标签进程指标。日志写标准输出，以 `request_id` 关联请求；不得把 DSN、原始载荷、令牌或凭据拼入命令和日志。

浏览器测试使用本地 Web Server 和后端路由 Mock，不访问公网、真实凭据或生产服务：

```powershell
Push-Location .\frontend
pnpm install --frozen-lockfile
pnpm exec playwright install chromium firefox webkit
pnpm exec playwright test --project=chromium
pnpm exec playwright show-trace .\test-results\<失败用例>\trace.zip
Pop-Location
```

## 安全

- 本地只使用虚假或公开测试数据。
- 不把交易所密钥写入 `.env`、脚本、终端截图、测试输出或 AI 上下文。
- 真实账户验证和生产发布由用户在目标主机手工执行；发布边界见[发布手册](./release.md)。
