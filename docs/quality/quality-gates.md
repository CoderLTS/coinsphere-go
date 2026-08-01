# 质量门禁

## PR 阻塞检查

- Go：格式、Vet、Staticcheck、测试、竞态检查和构建。
- Vue：ESLint、Stylelint、类型检查、单元测试和生产构建。
- Python：Ruff、Mypy、Pytest 和锁文件一致性。
- 容器：Compose 配置以及 Backend、Frontend 镜像构建。
- 安全：密钥、Go/Python 依赖、源代码、文件系统和 Backend/Frontend 镜像漏洞扫描。

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
- 项目已使用 Tailwind CSS 4、OKLCH、相对颜色、`:has()` 和渐进增强的 View Transition；关键 Playwright 浏览器矩阵仍属于 A0 待交付项。

## 已知基线债务

- 数据库迁移框架、关键 Playwright 场景、Worker 容器和发布产物扫描在对应 A0 独立 PR 建立前仍是阻塞 A0 退出的缺口。
- 当前 Go 测试只覆盖配置安全校验，前端和 Worker 仅有测试骨架；认证、工作流并发、取消和恢复的行为覆盖必须在 A1 对应 PR 中补齐。
- 本机缺少 Docker 时，Compose 启动、健康检查和 API 冒烟必须由 GitHub Actions 完成。
