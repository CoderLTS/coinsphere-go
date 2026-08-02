# 质量门禁

## PR 阻塞检查

- Go：格式、Vet、Staticcheck、测试、竞态检查和构建。
- 生命周期：`SIGINT`/`SIGTERM` 必须取消同一个根 Context；HTTP、Runtime、数据库和 WebSocket 在 30 秒应用总预算内收尾，Compose 给予 40 秒宽限，且停止后不再接收请求或认领执行。
- 数据库：SQLite 与 PostgreSQL 的空库升级、旧版本升级、回滚重放、幂等和失败原子性契约；Worker 任务表还必须覆盖七态、尝试次数、租约唯一性及非空表拒绝 Down。
- Vue：ESLint、Stylelint、类型检查、Vitest 单元测试和生产构建。
- 浏览器：Chromium、Firefox、WebKit 的关键 Playwright 冒烟；失败时保留截图、trace 和 HTML 报告。
- Python：Ruff、Mypy、Pytest 和锁文件一致性。
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

- 版本化 SQL migration 的工具与测试骨架已经建立；应用启动仍由 GORM `AutoMigrate` 管理业务表，切换必须在 A1 独立 PR 完成。
- Go 进程生命周期已覆盖信号、超时、取消传播、有界收尾和竞态；被取消的既有工作流执行按当前策略进入 `retry_waiting` 或 `failed`。
- Worker 任务队列 schema 已由 `00002` 建立并通过 SQLite/PostgreSQL migration 契约验证；Worker 仍仅覆盖 A0 空闲运行与健康契约，认领、心跳、租约 fencing、崩溃回收和 5 秒内取消由下一独立 PR 补齐。
- 本机缺少 Docker 时，Compose 启动、健康检查和 API 冒烟必须由 GitHub Actions 完成。
