# GitHub 仓库治理手册

远程仓库与组织权限只能由用户配置。Codex 可以在远程配置完成后推送 `codex/*` 分支并创建草稿 PR，但不能合并。

## 1. 连接远程仓库

```bash
git remote add origin <github-repository-url>
git push -u origin main
```

若远程默认分支不是 `main`，先统一默认分支名称，再启用保护规则。

## 2. 保护 `main`

在 GitHub Rulesets 或 Branch protection 中至少启用：

- Require a pull request before merging。
- 至少 1 次用户批准；新提交后旧批准失效。
- Require status checks to pass and require branches to be up to date。
- Require conversation resolution。
- Block force pushes and deletions。
- Do not allow bypassing for Codex 使用的账号或 Token。
- 禁止自动合并；合并动作由用户手工执行。

基础必需检查：

- `Go backend`
- `Vue frontend`
- `Python worker`
- `Container builds`
- `Secret scan`
- `Go vulnerability scan`
- `Python dependency scan`
- `Dependency and filesystem scan`

新增迁移、E2E、镜像扫描和发布检查后，应同步加入 Required checks。

## 3. Actions 权限

- Workflow 默认权限设为 Read repository contents。
- 发布 Workflow 需要 GHCR 时单独授予 `packages: write`，不复用生产部署凭据。
- 禁止把交易所密钥、Linux SSH 私钥或生产数据库凭据配置为 Actions Secret。
- Fork PR 不运行任何持有写权限或高权限 Secret 的步骤。

## 4. Renovate

安装 Renovate GitHub App，并确认读取根目录 `renovate.json`。Renovate 只允许创建依赖 PR，不允许自动合并。每周检查 Dependency Dashboard 中被阻塞或存在安全公告的更新。

## 5. PR 放行

1. Codex 创建草稿 PR 并填写模板。
2. CI 全部通过后，Codex 使用新上下文完成只读复审并处理发现。
3. 用户检查代码、迁移、截图、风险和回滚说明。
4. 用户将 PR 标记为 Ready 并手工合并。
5. 交易能力仍需单独阶段放行，代码合并不等于允许模拟或实盘运行。
