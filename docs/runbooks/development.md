# 开发与诊断

默认验证来自远端 CI，不运行本地测试、构建、应用、迁移演练或浏览器。静态审查、编辑、格式化无需启动系统；已通过且未受新改动影响的门禁不重复手工执行。用户当前任务明确要求本地验证时，才使用现有 scripts/verify.ps1 或 scripts/verify.sh，并只连接隔离环境。

## 插件工具

源码工具需要 Go 1.26.6、Node 24/pnpm 10.33、Docker Compose v2 与 PostgreSQL 16。安装前停止目标应用并保存一致恢复点；真实配置只在目标服务器，不进入命令输出或 AI 上下文。

```bash
coinsphere plugin validate /reviewed/business
coinsphere plugin install --config /server/target.yml --backend-root /checkout/backend /reviewed/business
coinsphere plugin upgrade --config /server/target.yml --backend-root /checkout/backend /reviewed/business
coinsphere plugin uninstall --config /server/target.yml --backend-root /checkout/backend example.business
coinsphere plugin purge-data --config /server/target.yml --backend-root /checkout/backend --confirm 'PURGE example.business' example.business
```

validate 只读解析 manifest/目录/版本/依赖/迁移；安装/升级复制源码、迁移、生成静态表、更新 Go 依赖并构建应用镜像。命令不启动镜像、不重启应用。升级版本必须递增，旧 SQL 不变，当前定义/Run/视图活动引用全部解除；跨 major 没有旧图兼容层。失败恢复源码与新增 migration，恢复失败使用配对恢复点。

卸载保留领域 schema；purge-data 还要求已卸载、全部历史引用解除与匹配确认文本。不要用删除业务事实或改版本表绕过条件。完整扩展契约和可编译示例见[插件开发指南](../plugin-development.md)。

## 诊断

健康检查 /health/live 证明进程响应，/health/ready 与 /health 验证数据库可达；/metrics 与首页系统观察要求 system.observe。应用启动会拒绝旧 generation、错误数据库版本和落后/超前 migration。

工作流列表显示真实 ID/状态，Run 详情、待办与制品均按当前资源授权读取。插件管理的安装/编译/加载原因可以定位未编译或版本不匹配；unknown_result 需业务对账，不能直接重试。

长连接定期验证会话与能力；连接关闭后 UI 从 HTTP 刷新持久事实。日志只包含 requestId 和受控分类，不复制原始载荷、Cookie、Authorization、DSN、个人数据或密钥。

远端 CI 失败时先读取对应结果和产物，按实际失败范围修复。浏览器证据使用隔离 Web 产物与传输替身；不在生产或本地启动验证来绕过默认规则。

一次性工作流转换和恢复见[数据库迁移手册](database-migrations.md)，发布见[发布手册](release.md)。
