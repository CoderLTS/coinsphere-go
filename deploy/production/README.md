# CoinSphere 生产 Compose

该目录是 CoinSphere 唯一的生产 Compose 模板。它以独立项目 `coinsphere-go` 运行 TimescaleDB、Backend 和 Web，不与 sub2api 或其他应用共享 Compose。

## 文件

- `compose.yaml`：V2 基线三服务拓扑。
- `deploy.sh`：固定 digest 部署、migration、健康检查、旧 CoinSphere 容器迁移和失败回滚。
- `runtime.env.example`：Backend 运行 Secret 模板，不包含数据库 DSN。
- `data/`：TimescaleDB、上传文件、静态文件和工作流制品的持久目录。

`runtime.env`、`.env` 和真实 Secret 只保存在服务器。首次部署会生成独立数据库密码并写入权限为 `0600` 的 `.env`；后续部署复用该密码和 `data/`。从旧版升级时，部署脚本会在目标目录为空时将已有 CoinSphere 命名卷复制到 `data/`，旧卷保留为回滚备份但不再挂载。

`COINSPHERE_WORKFLOW__HTTP_ALLOWED_HOSTS` 使用 YAML/JSON 数组保存 Connector 与 AI 可访问的精确公共域名，例如 `[api.example.com]`。默认 `[]` 拒绝全部外部目标；不接受通配符、IP、子域继承或私网地址。

```bash
cp runtime.env.example runtime.env
chmod 600 runtime.env
COINSPHERE_DEPLOY_DIR=/path/to/coinsphere-go ./deploy.sh vX.Y.Z release-manifest.json
```

自动发布在服务器已配置 `COINSPHERE_STACK_ROOT` 时，将独立项目放在其 `compose/coinsphere-go` 子目录，并从既有 CoinSphere Secret 初始化 `runtime.env`。部署只停止和移除旧共享 Compose 中实际运行的 CoinSphere 容器；不会执行共享项目级 `down`、修改其他服务或删除旧数据卷。

应用 schema 来自 Backend 镜像内的单一初始化 migration。数据库与 Backend 文件都绑定到部署目录下的 `data/`，回滚不会自动执行 migration Down。
