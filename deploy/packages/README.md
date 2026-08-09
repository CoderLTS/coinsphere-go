# CoinSphere 二进制发布包

包内包含后端服务、migration CLI、Paper Executor、默认配置、前端静态文件和 Nginx 配置。

这不是桌面应用。启动后端前必须设置安全的 `COINSPHERE_AUTH__SECRET_KEY` 等环境变量；前端目录需要由 Nginx 或等价 Web Server 托管，并按随包 `nginx.conf` 将 API、WebSocket 和健康检查反向代理到后端 `6987` 端口。

Windows 包的后端目标架构为 `GOOS=windows GOARCH=386`；Linux 包为 `GOOS=linux GOARCH=amd64`。

`coinsphere-executor` 只消费 PostgreSQL 中的 Paper 意图，不包含 Testnet/Live 私有接口或交易所凭据。启动前必须先执行 migration，并为 Executor 配置专用的 PostgreSQL 身份。
