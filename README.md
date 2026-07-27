# coinsphere-go

coinsphere 的 Go 版整合仓库:Go 后端(单二进制)+ Vue 前端。

```
backend/    Go 后端 —— 单进程内运行 HTTP API + 工作流调度/执行/事件分发(详见 backend/README.md)
frontend/   Vue 3 + Vite 前端(原 fronted/,权限走 backend 模式)
```

## 开发

两个终端分别启动:

```powershell
# 1) 后端(默认读 backend/config.yml,SQLite,监听 :6987)
cd backend
# ⚠️ 首次运行需要一个真实签名密钥,否则拒绝启动(见 backend/README.md 安全说明):
#    正式用:在 config.yml 或环境变量设 COINSPHERE_AUTH__SECRET_KEY(如 openssl rand -hex 32)
#    本地图快:$env:COINSPHERE_ALLOW_INSECURE_SECRET = '1'   # 临时放行默认密钥
go build -o coinsphere-server.exe .
.\coinsphere-server.exe

# 2) 前端(:3006,dev 代理把请求转发到 http://127.0.0.1:6987)
cd frontend
pnpm install
pnpm dev
```

默认超管账号:`coinsphere` / `coinsphere`(后端首次启动自动建表 + 写种子数据),**首登后请尽快改密**;可用 `COINSPHERE_AUTH__BOOTSTRAP_ADMIN_PASSWORD` 指定强初始密码。

## 生产部署(Docker 一键起)

拓扑:`web`(nginx)托管前端 dist 并把 `/api`、`/static`、`/uploads`、`/ws`、`/health` 反代到 `backend`(Go)。
前端生产环境 `VITE_API_URL = /`,与 nginx 同源,故无需改前端代码。

```bash
# 必须先给一个签名密钥(未设置则 compose 直接报错):
export COINSPHERE_AUTH__SECRET_KEY=$(openssl rand -hex 32)
docker compose up -d --build
# 浏览器打开 http://localhost:8080,默认超管 coinsphere / coinsphere(首登后尽快改密)
```

- 入口只有 `web`(默认 `8080`,可用 `COINSPHERE_WEB_PORT` 改);`backend` 不对外暴露,仅经 nginx 反代。
- 持久化:sqlite(`backend-data` 卷)、上传文件(`backend-uploads`)、后端静态(`backend-static`)。
- 换密钥/数据库:`COINSPHERE_AUTH__SECRET_KEY`、`COINSPHERE_DATABASE__*` 环境变量(见 `docker-compose.yml` 注释与 `backend/README.md`)。
- 前端构建用项目自带 `pnpm build`(含 `vue-tsc` 类型检查);若类型检查阻塞出镜像,把 `frontend/Dockerfile` 的 `pnpm build` 换成 `pnpm exec vite build`。

> 说明:Go 后端只在 `/static/`、`/uploads/` 提供文件、`/api` `/ws` `/health` 提供接口,**不在根路径托管 SPA**——所以由 nginx 托管前端并反代后端,而不是把 dist 塞进后端的 `volumes/static`。

数据库可切 sqlite / mysql / postgres,环境变量覆盖规则等见 `backend/README.md`。
