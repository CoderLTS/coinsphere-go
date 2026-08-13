# CoinSphere 质量门禁

门禁按变更范围选择已有 CI 检查；不要为同一交付或同一模块重复运行完整矩阵。领域验收和晋级证据放在对应测试与 GitHub Issue，不在本文件复制。

## Pull Request

- Ready PR 的 base 必须是 `main`；依赖未合并代码的 stacked PR 保持 Draft。
- `changes` 按路径选择 Backend、Frontend、Worker 和发布脚本检查，并拒绝 Ready PR 指向非 `main`。
- Backend 变更运行 `go mod tidy -diff`、`gofmt`、`go vet`、Staticcheck、全量测试和构建；Frontend 变更运行锁定依赖安装、ESLint、Stylelint、单元测试、类型检查/构建和 Chromium 冒烟；Worker 变更运行 Ruff、Mypy 和 Pytest；发布脚本变更运行 Bash 语法、ShellCheck 和脚本测试。
- `.github/workflows/ci.yml` 变更会选择全部模块。依赖或锁文件变化时，Security workflow 选择对应漏洞/文件系统扫描。
- Secret scan 每个 PR 都运行。金融、凭据、迁移、并发、恢复或外部协议变更必须在本切片补充覆盖边界行为的测试，并在 PR 记录命令和结果。
- 纯文档/治理变更只做相对链接、YAML 解析、`git diff --check` 和只读引用复审；这些检查目前由本地或审查执行，不宣称由 CI 自动完成。

## `main` 与容器

推送到 `main` 时 CI 选择全部模块：Backend 使用 Race 测试，Frontend 运行 Chromium/Firefox/WebKit 冒烟，Worker 运行全量测试，容器 Job 构建 Compose 镜像、检查代理隔离、运行健康/`/health` 冒烟，并扫描 Backend、Web、Worker 镜像的 CRITICAL 漏洞。

Security workflow 在 `main` 和每周计划任务运行 Secret、Go、Python 依赖及文件系统扫描；按变更范围的 PR 只运行适用扫描。

## 发布

Release 和 Deploy 只能由用户从最新 `main` 手工触发。两者复用同一流程构建固定版本和 digest，生成 Backend/Web/Worker SBOM，扫描镜像和归档并校验 Manifest/SHA-256；Deploy 保留 14 天 Artifact，但不创建 GitHub Release。生产流程不得接触真实交易所密钥、自动下单、启用真实策略或解除急停。

Migration 的冻结点、Up/Down 安全和备份恢复见[数据库迁移手册](../runbooks/database-migrations.md)；发布故障处理见[发布手册](../runbooks/release.md)。
