# 质量门禁

门禁按变更路径和风险分层执行。PR 获取快速反馈，开发波次集中验证跨模块行为，`main`、定时任务和发布承担完整供应链检查；同一检查不在子步骤之间重复运行。

## PR 快速层

- 基线：Draft stacked PR 可以指向父分支；Ready PR 必须以 `main` 为 base，且依赖 PR 已进入 `main`。
- 通用：密钥扫描始终运行；只检查受影响模块的格式、Lint、类型、构建和核心测试。
- Go：涉及 Go 时运行格式、Vet、Staticcheck、`go test -count=1 ./...` 和构建，竞态检查留到完整层。
- Vue：涉及前端时运行 ESLint、Stylelint、Vitest、类型检查和生产构建；影响关键交互时增加 Chromium 冒烟。
- Python：涉及 Worker 时运行 Ruff、Mypy、Pytest 和锁文件一致性检查。
- 数据库：涉及 migration、事务或约束时验证 PostgreSQL 升降级、失败原子性、并发与回滚契约。
- 依赖：仅在依赖清单或锁文件变化时运行对应依赖扫描。
- 发布：涉及发布维护脚本时运行 Bash 语法、ShellCheck 和现有脚本检查；Compose 构建与启动留到完整层。

核心测试按风险投入，必须覆盖金融 Decimal/UTC、数据库约束与 migration、并发事务、恢复、外部契约、SSRF/Auth、采集去重补数、订单状态机、风控、账务和对账。普通 CRUD、DTO 映射、简单配置、日志包装、普通 UI 和工作流/通知胶水不强制新增单元测试；Bug 修复增加一个覆盖根因的回归检查。

## 波次完整层

同一波次的独立切片合入临时集成分支后执行一次完整门禁和一次只读复审：

- Go 全量测试、竞态检查和进程生命周期契约。
- PostgreSQL/TimescaleDB Compose、migration、并发事务、恢复与跨模块集成。
- Chromium、Firefox、WebKit 关键 Playwright 冒烟；失败时保留截图、trace 和 HTML 报告。
- Backend、Frontend、Worker 镜像构建、Compose 健康检查和端到端冒烟。
- 安全、交易、权限或订单状态变更对应的专项契约。

波次集成分支只验证组合结果，不替代各 PR 的用户审查和手工合并。

## `main` 与发布层

- `main`、定时任务或发布运行完整依赖、源代码、文件系统和镜像漏洞扫描，以及完整跨模块回归。
- 发布产物 ZIP、tar.gz、Manifest、SPDX JSON SBOM 和 `SHA256SUMS` 必须通过精确清单、校验和、远端镜像 digest 与 SBOM 根组件绑定、危险归档路径及敏感内容扫描；失败时禁止上传 Artifact 和部署。
- 生产部署只能由用户手工触发并通过受保护的 `production` Environment 审批；禁止自动合并、自动真实交易或绕过风控。

## 关键契约

- 生命周期：`SIGINT`/`SIGTERM` 取消同一个根 Context；HTTP、Runtime、数据库和 WebSocket 在 30 秒应用总预算内收尾，Compose 给予 40 秒宽限，停止后不再接收请求或认领执行。
- WebSocket：同一连接由单 writer 顺序写出固定信封和连续序号；覆盖背压、健康连接隔离、RFC6455 Ping/Pong、失联超时、关闭、晚注册拒绝、Origin 校验和前端序号处理。
- 数据库与队列：固定 TimescaleDB 镜像验证单一 PostgreSQL 基线、重复 Up、空库 Down/重放、非空 Down 保护、并发写入锁、租约唯一性、并发认领、续租、fencing、过期恢复、退避、尝试耗尽和事务回滚。
- Worker：真实 PostgreSQL 覆盖并发认领、续租、旧租约 fencing、过期恢复、尝试耗尽、正常取消和 Owner 崩溃后的 5 秒取消截止时间。
- 浏览器：支持范围以 `frontend/package.json` 的 `browserslist` 为准；Playwright Chromium/WebKit 不替代 Edge、Safari 或 iOS 真机验收，工作流编辑器只支持桌面端。

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
- Playwright 使用固定本地 Web Server 和后端路由 Mock，在 Chromium、Firefox、WebKit 中验证游客工作流权限边界和已认证会话关键路径；用例不访问公网、真实凭据或生产服务。
- Chromium 不代表 Microsoft Edge，WebKit 不代表 macOS Safari 或 iOS 真机 Safari；品牌浏览器和真机契约仍需对应环境验收。
