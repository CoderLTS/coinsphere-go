# CoinSphere 生产 Compose

该目录是 CoinSphere 唯一的生产 Compose 模板。它以独立项目 `coinsphere-go` 运行一个内置 Vue 静态产物的 Go App 容器，并通过外部 `dpanel_stack` 网络连接服务器现有 PostgreSQL 16 的独立 `coinsphere_go` 数据库。服务器现有 Nginx 反向代理到 `127.0.0.1:8080` 即可。

## 文件

- `compose.yaml`：单应用容器与共享 PostgreSQL 网络。
- `deploy.sh`：固定 digest 部署、migration、健康检查、旧 CoinSphere 服务清理和失败回滚。
- `runtime.env.example`：Backend 运行 Secret 模板，不包含数据库 DSN。
- `data/`：上传文件、静态文件和工作流制品的持久目录。

`runtime.env`、`.env` 和真实 Secret 只保存在服务器。首次部署会生成独立数据库密码并写入权限为 `0600` 的 `.env`；后续部署复用该密码和 `data/`。共享 PostgreSQL 中的 `coinsphere_go` 用户必须使用同一密码，数据库只归 CoinSphere 使用。

`COINSPHERE_WORKFLOW__HTTP_ALLOWED_HOSTS` 使用 YAML/JSON 数组保存 Connector 与 AI 可访问的精确公共域名，例如 `[api.example.com]`。默认 `[]` 拒绝全部外部目标；不接受通配符、IP、子域继承或私网地址。

```bash
cp runtime.env.example runtime.env
chmod 600 runtime.env
COINSPHERE_DEPLOY_DIR=/path/to/coinsphere-go ./deploy.sh vX.Y.Z release-manifest.json
```

自动发布在服务器已配置 `COINSPHERE_STACK_ROOT` 时，将独立项目放在其 `compose/coinsphere-go` 子目录，并从既有 CoinSphere Secret 初始化 `runtime.env`。部署只操作独立 `coinsphere-go` 项目的服务，不执行共享项目级 `down`，也不修改 PostgreSQL 数据栈或其他服务。

应用 schema 来自 Backend 镜像内的版本化 migration。数据库由服务器 PostgreSQL 数据栈持久化，Backend 文件绑定到部署目录下的 `data/backend`；回滚不会自动执行 migration Down。

完整发布、失败恢复和 migration 规则见[发布与回滚](../../docs/runbooks/release.md)和[数据库迁移](../../docs/runbooks/database-migrations.md)。
