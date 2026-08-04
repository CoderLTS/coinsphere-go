# GitHub 仓库治理手册

Codex 可以推送 `codex/*` 分支并创建草稿 PR，但不能合并。仓库为私有且当前套餐不提供 Branch Protection/Environment required reviewers，因此用户审查、手工合并、最新 `main` 校验和最终只读复审是现阶段补偿控制。

## 动态进度

- 建立 A0-A8 Milestone，不设置缺少估算依据的目标日期。
- Milestone 和父跟踪 Issue 的开关状态必须依据已合并 PR 与验收证据直接在 GitHub 维护，本手册不硬编码当前阶段。
- A2 使用 A2.0-A2.4 五个父跟踪 Issue；A3 使用 A3-core/A3-news。父 Issue 只组织能力门，实际开发 Issue 按可独立验收和回滚的纵向能力创建。
- 仓库文档不得记录完成百分比、当前分支或 PR 状态。Milestone、Issue 和 PR 是唯一动态来源。
- 每个验收 Issue 链接适用的 CI run、Manifest/哈希、质量报告、演练和用户审批；仓库只保存稳定模板和指标口径。

## main 与 PR

- 分支使用 `codex/<phase>-<slug>`，PR 标题使用 `[type] 中文描述`。
- 独立 PR 以 `main` 为 base；stacked PR 在依赖未合并时保持 Draft 并指向父分支。所有 Ready PR 必须以 `main` 为 base。
- CI 通过后由 Codex 完成一次最终只读复审；用户检查代码、migration、风险、回滚和证据后手工合并。
- 交易能力需要独立阶段放行，代码合并不等于允许模拟或实盘运行。

平台支持后，`main` 至少启用 PR、一次用户批准、Required checks、conversation resolution、禁止 force push/delete 和禁止 bypass。稳定 Required checks 只使用：

- `PR summary gate`
- `Secret scan`

## Actions 与发布权限

- 普通 Workflow 默认只有 repository contents 只读权限。
- Release Workflow 只保留创建 GitHub Release 所需的 `contents: write`。
- 交易所密钥、SSH 私钥和生产数据库凭据不得进入 Actions Secret；私有 Registry 凭据只保存在生产 Runner 本机。
- Fork PR 不运行持有写权限或高权限 Secret 的步骤。
- 生产发布仅允许用户从最新 `main` 手工触发。进入 R2 前按发布 Runbook 分离构建与固定部署器。

## 依赖与清理

- Renovate 只创建依赖 PR，不自动合并；每周检查被阻塞或存在安全公告的更新。
- 用户合并后，确认 PR 为 `MERGED`、远端 `main` 已包含提交且分支未被活跃 worktree 使用，再删除对应本地和远端分支。
- 不得删除 `main`、未合并分支或版本 Tag。
