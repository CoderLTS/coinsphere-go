# GitHub 仓库治理手册

配置远程仓库后，Codex 可以推送 `codex/*` 分支并创建草稿 PR，但不能合并。

若当前 GitHub 套餐不支持私有仓库保护规则，用户必须继续执行 PR 审查和手工合并；发布 Workflow 通过 `production` Environment 的 `main` 分支白名单与脚本内最新 `origin/main` 校验阻止其他分支发布。平台支持后按下节启用强制规则。

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

Required checks 只配置以下两个稳定名称：

- `PR summary gate`
- `Secret scan`

`PR summary gate` 汇总按路径选择的 Go、Vue、Chromium、Python 和发布脚本快速检查；未受影响 Job 可以正常跳过。依赖扫描只在锁文件变化、`main`、定时或发布时执行，容器与三浏览器矩阵只在波次集成及 `main` 执行，因此不把这些动态或完整层 Job 单独设为 Required checks。

## 3. Actions 权限

- Workflow 默认权限设为 Read repository contents。
- 发布 Workflow 仅授予创建 GitHub Release 所需的 `contents: write`；私有 Registry 凭据只保存在生产 Runner 本机，不配置为 Actions Secret。
- 禁止把交易所密钥、Linux SSH 私钥或生产数据库凭据配置为 Actions Secret。
- Fork PR 不运行任何持有写权限或高权限 Secret 的步骤。

## 4. Renovate

安装 Renovate GitHub App，并确认读取根目录 `renovate.json`。Renovate 只允许创建依赖 PR，不允许自动合并。每周检查 Dependency Dashboard 中被阻塞或存在安全公告的更新。

## 5. PR 放行

1. Codex 使用 `codex/<phase>-<slug>` 分支创建草稿 PR，标题使用 `[type] 中文描述` 并填写模板。
2. 独立 PR 以 `main` 为 base；stacked PR 在依赖未合并时必须保持 Draft 并指向父分支。所有 Ready PR 必须以 `main` 为 base，CI 会拒绝例外。
3. CI 全部通过后，Codex 使用新上下文完成只读复审并处理发现。
4. 用户检查代码、迁移、截图、风险和回滚说明。
5. 用户将 PR 标记为 Ready 并手工合并。
6. 交易能力仍需单独阶段放行，代码合并不等于允许模拟或实盘运行。

## 6. 分支清理

用户手工合并后，先确认 PR 状态为 `MERGED`、远端 `main` 包含合并提交、本地 `main` 已同步，并且分支未被活跃 worktree 使用，再删除对应本地和远端 `codex/*` 分支。不得删除 `main`、未合并分支或版本 Tag。
