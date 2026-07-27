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
go build -o coinsphere-server.exe .
.\coinsphere-server.exe

# 2) 前端(:3006,dev 代理把请求转发到 http://127.0.0.1:6987)
cd frontend
pnpm install
pnpm dev
```

默认超管账号:`coinsphere` / `coinsphere`(后端首次启动自动建表 + 写种子数据)。

## 生产部署

前端为纯静态资源,由后端同源托管(生产环境 `VITE_API_URL = /`):

```powershell
cd frontend
pnpm install && pnpm build          # 产物在 frontend/dist/
# 把 dist/ 内容放到后端可执行文件同级的 volumes/static/
cd ..\backend
go build -o coinsphere-server.exe .
mkdir volumes\static -Force
Copy-Item ..\frontend\dist\* volumes\static\ -Recurse -Force
.\coinsphere-server.exe             # 单进程同时提供 API 与前端静态页
```

数据库可切 sqlite / mysql / postgres,环境变量覆盖规则等见 `backend/README.md`。
