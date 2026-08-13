# GitHub 仓库治理手册

用户明确要求交付并授权外部同步时，Codex 可以推送 `codex/*` 分支并创建 Draft PR，但不能合并；仅分析、复审、状态报告或本地整理请求不推送、不建 PR。仓库为私有且当前套餐不提供完整 Branch Protection/Environment required reviewers，因此用户审查、手工合并、最新 `main` 校验和最终只读复审是现阶段补偿控制。

## 动态进度

- 使用 M1 研究闭环、M2 信号闭环、M3 Testnet 闭环和 M4 小额实盘四个 Milestone，不设置缺少估算依据的目标日期。
- 每个实际开发 Issue 对应一个可使用、可独立验收和回滚的纵向能力；不再创建只组织层级但不能交付的父跟踪 Issue。
- Milestone 和 Issue 状态必须依据已合并 PR、验收证据和用户放行直接在 GitHub 维护。仓库文档不得记录完成百分比、当前分支或 PR 状态。
- 每个验收 Issue 链接适用的 CI run、Manifest/SHA-256、质量报告、恢复或急停演练和用户审批；仓库只保存稳定模板和指标口径。

## `main` 与 PR

- 分支使用 `codex/<phase>-<slug>`，PR 标题使用 `[type] 中文描述`。
- 独立 PR 以 `main` 为 base；stacked PR 在依赖未合并时保持 Draft 并指向父分支。所有 Ready PR 必须以 `main` 为 base。
- CI 通过后由 Codex 完成一次最终只读复审；用户检查代码、migration、风险、回滚和证据后手工合并。
- 交易能力需要独立阶段放行。代码合并或部署不等于允许 Paper、Testnet 或 Live 运行。

## Actions 与发布权限

- 普通 Workflow 默认只有 repository contents 只读权限。
- Release Workflow 只保留创建 GitHub Release 所需的 `contents: write`。
- Deploy Workflow 只保留 `contents: read`，并复用 Release Workflow 中的构建、扫描和部署步骤，不创建 GitHub Release。
- 交易所密钥、SSH 私钥和生产数据库凭据不得进入 Actions Secret；私有 Registry 凭据只保存在生产 Runner 本机。
- Fork PR 不运行持有写权限或高权限 Secret 的步骤。
- 生产发布和部署仅允许用户从最新 `main` 手工触发。进入 Live 前按发布 Runbook 分离构建与固定部署器。

## 依赖与清理

- Renovate 只创建依赖 PR，不自动合并；每周检查被阻塞或存在安全公告的更新。
- 用户合并后，确认 PR 为 `MERGED`、远端 `main` 已包含提交且分支未被活跃 worktree 使用，再删除对应本地和远端分支。
- 不得删除 `main`、未合并分支、历史 Issue/Milestone 或版本 Tag。
