# 质量门禁

## PR 阻塞检查

- Go：格式、Vet、Staticcheck、测试、竞态检查和构建。
- 生命周期：`SIGINT`/`SIGTERM` 必须取消同一个根 Context；HTTP、Runtime、数据库和 WebSocket 在 30 秒应用总预算内收尾，Compose 给予 40 秒宽限，且停止后不再接收请求或认领执行。
- 数据库：SQLite 与 PostgreSQL 的空库升级、旧版本保数升级、回滚重放、重复 Up 和失败原子性契约；Worker 任务表必须覆盖七态、尝试次数、租约唯一性及非空表拒绝 Down，Outbox 必须覆盖五态、活跃租约字段一致性、尝试次数、可用/终态时间、必要索引、租约唯一性及非空表拒绝 Down。
- Vue：ESLint、Stylelint、类型检查、Vitest 单元测试和生产构建。
- 浏览器：Chromium、Firefox、WebKit 的关键 Playwright 冒烟；失败时保留截图、trace 和 HTML 报告。
- Python：Ruff、Mypy、Pytest、锁文件一致性，以及真实 PostgreSQL 的并发认领、续租、旧租约 fencing、过期恢复、尝试耗尽、正常取消与 Owner 崩溃后的 5 秒取消截止时间。
- 容器：Compose 配置以及 Backend、Frontend、Worker 镜像构建与健康检查。
- 安全：密钥、Go/Python 依赖、源代码、文件系统和 Backend/Frontend/Worker 镜像漏洞扫描。
- 发布产物：最终 ZIP、tar.gz、Manifest、SPDX JSON SBOM 和 `SHA256SUMS` 必须通过精确清单、校验和、远端镜像 digest 与 SBOM 根组件绑定、危险归档路径及敏感内容扫描，失败时禁止上传 Artifact 和部署。

## 领域验收目标

- 修复后的 K 线完整率不低于 99.99%，唯一键重复为零。
- 行情连接中断后 30 秒内恢复，任务取消在 5 秒内生效。
- 相同策略、数据集、参数和镜像生成相同交易账本。
- 重试不产生重复订单，急停后不产生新增敞口。
- 重启后账户、订单、成交、仓位和余额完成自动对账。

领域指标在对应里程碑实现前作为目标保留，不得使用空测试或固定成功结果伪造通过。

## 前端浏览器基线

- 受支持范围以 `frontend/package.json` 的 `browserslist` 为准：Chrome、Edge、Firefox 最近两个稳定版本，以及 Safari/iOS 16.4 及以上版本。
- 不支持 IE、停止维护的浏览器和旧版内嵌 WebView。`vite.config.ts` 中的 `build.target` 仅定义 JavaScript 转译目标，不替代浏览器支持契约。
- Playwright 使用固定本地 Web Server 和后端路由 Mock，在 Chromium、Firefox、WebKit 中验证游客工作流权限边界，以及已认证会话创建并保存默认工作流；用例不访问公网、真实凭据或生产服务。
- Chromium 只覆盖 Playwright 锁定的 Chromium 引擎，不代表 Microsoft Edge 最近两个稳定版；WebKit 只覆盖 Playwright WebKit，不代表 macOS Safari 或 iOS 真机 Safari。`browserslist` 中 Edge、Safari/iOS 的品牌浏览器和真机契约仍需对应环境验收。
- 工作流编辑器按产品约束只支持桌面端，本门禁不宣称移动端编辑器覆盖。

## 已知基线债务

- 版本化 SQL migration 已包含机制基线、Worker 队列表和逻辑版本 `00003` 的 SQLite/PostgreSQL Outbox schema 契约；应用启动以同表名占位模型隔离已版本化 Outbox DDL，并继续迁移其他业务表及关系，整体切换必须在 A1-10 独立 PR 完成。
- Outbox schema 已为原子认领、租约 fencing、过期恢复、最大尝试、死信和告警留存提供字段与约束；现有 dispatcher 仍直接扫描 `pending`，尚未实现上述运行时语义。新增日志不得输出事件 payload、metadata、环境值、凭据或异常中的敏感正文。
- Go 进程生命周期已覆盖信号、超时、取消传播、有界收尾和竞态；被取消的既有工作流执行按当前策略进入 `retry_waiting` 或 `failed`。
- Worker 任务队列 schema 与 A1 PostgreSQL 运行时已建立；运行日志必须覆盖启动停止、认领、状态变化、心跳异常、恢复、取消和终态，且不得输出 DSN、环境值、原始任务载荷或凭据。当前仅有契约伪任务，数据集、回测与生产部署仍是后续里程碑。
- 本机缺少 Docker 时，Compose 启动、健康检查和 API 冒烟必须由 GitHub Actions 完成。
