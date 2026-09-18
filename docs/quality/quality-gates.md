# CoinSphere 质量门禁

CI 只保留提交所需的基础语法与编译检查，避免为同一交付重复维护多套门禁。更完整的本地验证仍可按 `scripts/verify.sh` 或 `scripts/verify.ps1` 执行；领域验收和晋级证据放在对应测试与 GitHub Issue，不在本文件复制。

## Pull Request

- Ready PR 的 base 必须是 `main`；依赖未合并代码的 stacked PR 保持 Draft。
- `.github/workflows/ci.yml` 在 PR 或手工触发时并行运行两个基础检查：Backend 执行 `go mod tidy -diff`、`gofmt`、`go vet` 和构建；Frontend 执行锁定依赖安装、ESLint、类型检查和构建。
- 当前 GitHub CI 不运行 Secret、漏洞、浏览器或容器扫描；涉及金融、凭据、迁移、并发、恢复或外部协议的变更，仍须按对应 Runbook 额外验证并在 PR 记录命令和结果。
- 纯文档/治理变更只做相对链接、YAML 解析、`git diff --check` 和只读引用复审；这些检查目前由本地或审查执行，不宣称由 CI 自动完成。

## `main` 与容器

CI 不在合并后的 `main` push 上重复运行；合并前的 PR 检查是基础门禁。生产发布仍只通过手工触发的部署工作流执行。

## 发布

Release and deploy 默认不触发；用户在当前任务明确授权后，Codex 可从最新 `main` 触发既有手工工作流并监控验证。当前工作流构建并部署固定 digest 的应用镜像，不创建 GitHub Release 或额外扫描制品。生产流程不得接触真实交易所密钥、自动下单、启用真实策略或解除急停。

Migration 的冻结点、Up/Down 安全和备份恢复见[数据库迁移手册](../runbooks/database-migrations.md)；发布故障处理见[发布手册](../runbooks/release.md)。
