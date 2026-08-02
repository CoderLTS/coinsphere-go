# 发布与回滚

## 当前边界

当前生产 Compose 仍使用 SQLite，版本化 migration 已包含机制基线和未启用消费的 `worker_tasks` 表，应用启动仍保留 GORM `AutoMigrate`。A1 完成业务 schema 切换前，本流水线只用于现有管理平台，不得据此启用模拟盘或实盘交易能力。

发布允许 Codex 和 GitHub Actions 连接生产主机，但不得接触真实交易所密钥或发起真实订单。生产发布必须由用户从 `main` 手工触发；PR、push 和定时任务不会使用生产 Runner。

后端容器接收 Compose 发送的 `SIGTERM` 后，通过 `signal.NotifyContext` 取消 HTTP 与 Runtime 根 Context，停止接收新请求和认领新执行，并有界关闭数据库与 WebSocket。应用总关机预算为 30 秒，Compose `stop_grace_period` 为 40 秒；被取消的既有工作流执行按当前重试策略进入 `retry_waiting` 或 `failed`。`worker_tasks` schema 不改变运行拓扑，Python Worker 任务消费仍未进入发布范围。

## 生产基础设施

- Runner：`coinsphere-production`，标签为 `self-hosted`、`Linux`、`X64`、`coinsphere-release`、`production`。
- Registry：`127.0.0.1:5000`，Runner 使用服务器本地 `coinsphere-ci` 登录信息，GitHub 不保存 Registry 密码。
- DPanel Compose：`/home/infrastructure/dpanel/compose/coinsphere-go`。
- Web 健康检查：生产主机 `http://127.0.0.1:8080/health`。
- 生产运行配置：部署目录的 `runtime.env`，权限固定为 `0600`，不会进入仓库、日志或 Release。
- Runner 必须提供 Python 3、GNU tar 和 gzip；最终产物扫描只使用 Python 标准库读取 ZIP、tar.gz 和 JSON，TAR 仅解压到系统临时文件且不会写入工作区。
- 出站代理只配置在 Runner 服务环境中；Action 下载和其他出站工具仍会继承该环境。`build.sh` 统一大小写变量，并仅通过 BuildKit 预定义构建参数把代理传入构建步骤，镜像和运行容器不保存代理配置。
- `${DOCKER_CONFIG:-$HOME/.docker}/config.json` 禁止包含顶层 `proxies`。发布前置检查发现该配置时会在调用 Docker 前终止，避免代理自动注入生产容器。
- Runner 的 `NO_PROXY`/`no_proxy` 必须至少包含 `127.0.0.1`、`localhost` 和本机 Registry 地址，确保推送、部署及健康检查不经过出站代理。

仓库当前为 GitHub 私有仓库，现有套餐不支持 Branch Protection 或 Environment required reviewers。`production` Environment 已用自定义部署分支策略限制为 `main`，工作流还会校验最新 `origin/main`；当前人工门禁由 `workflow_dispatch` 和用户不直接推送 `main` 的流程约束保证。如需 GitHub 强制 PR 审查或“触发后再审批”，必须升级支持私有仓库保护规则的套餐。

## 手工发布

1. 确认目标 PR 已由用户合并到 `main`，且 CI、安全检查和迁移说明通过。
2. 在 GitHub Actions 打开 `Release and deploy`，选择 `main`，输入符合 `vX.Y.Z` 的新版本号并手工运行。
3. 流水线确认执行 Commit 是最新 `origin/main`，且版本标签不存在。
4. 专用 Runner 构建三个发布包：Windows x86、Linux amd64 和 Docker Compose；后端和前端镜像分别推送版本标签与 `sha-<commit>` 标签到主机本地私有 Registry，以取得 RepoDigest，禁止使用 `latest`。
5. 流水线从 Manifest 中的不可变 RepoDigest 生成双镜像 SPDX JSON SBOM，完成镜像漏洞扫描，并在 `dist` 内一次性生成覆盖三个归档、Manifest 和两份 SBOM 的 `SHA256SUMS`。
6. 最终产物安全与完整性扫描通过后，才把候选产物上传为 Actions Artifact；扫描失败时禁止上传 Artifact、部署和创建 GitHub Release。
7. 自动部署从已扫描 Manifest 读取不可变 RepoDigest，停止旧服务、备份 SQLite 数据卷、执行 migration、启动固定 digest 镜像并检查健康状态。
8. 部署成功后才创建 GitHub Release；只有扫描通过但部署失败的候选产物会在 Actions Artifact 保留 14 天，且不会创建版本标签。
9. Release 创建后，Registry 对 backend/web 分别保留最近 10 个版本和当前部署版本，并删除对应的本地旧镜像标签。
10. 无论发布成功或失败，最终步骤都会删除 `dist`、清理超过 24 小时的 Runner 临时文件、清理 CoinSphere 专用 Builder 中超过 7 天的缓存并停止 Builder 容器。

Windows/Linux 包不是桌面应用：包内后端二进制可直接运行，`web` 目录需要 Nginx 或等价 Web Server 托管并反向代理到后端。

部署脚本的 `stop`、`down` 和回滚均使用相同关机契约：先发送 `SIGTERM`，由应用在 30 秒内完成 HTTP、Runtime、数据库和 WebSocket 收尾，Compose 最多等待 40 秒后才强制停止。发布工作流本身不因 A1-1 增加新的生产触发方式。

## 最终产物门禁

`dist` 必须精确包含以下七个普通文件，不允许目录、符号链接、缺项或额外文件：

- `coinsphere-<version>-windows-x86.zip`
- `coinsphere-<version>-linux-amd64.tar.gz`
- `coinsphere-<version>-docker.tar.gz`
- `release-manifest.json`
- `coinsphere-<version>-backend.spdx.json`
- `coinsphere-<version>-web.spdx.json`
- `SHA256SUMS`

`SHA256SUMS` 必须以规范相对路径各覆盖前三个归档、Manifest 和两份 SBOM 一次。扫描器逐项重算 SHA-256，严格校验 Manifest 的版本、Commit、镜像标签与 digest；本地 Docker 元数据必须证明版本标签和 `sha-<commit>` 标签指向同一镜像、RepoDigest 与 OCI version/revision 标签正确，Registry 远端两个标签也必须解析到相同 digest。SBOM 必须是非空的 SPDX 2.3 JSON 文档，其唯一 `DOCUMENT -> DESCRIBES` 根组件必须绑定对应镜像 repository 和 Manifest digest。

归档只允许预期根目录、文件清单和关键执行位。构建会移除 Nginx 未使用的前端 `.gz` 副本，并以零时间戳、数字属主和无名称字段的单一 GZIP 流生成规范 TAR。绝对路径、盘符、反斜杠、`.`/`..`、控制字符、重复或大小写冲突路径、链接、设备类型、ZIP 前缀/本地头额外字段/注释/尾随数据、GZIP 可选元数据/串接流，以及 TAR PAX/属主名称/逻辑结尾后的数据会直接失败。内容扫描会按路径与文件签名阻止非占位凭据、私钥、实际 `.env`/`runtime.env`、Docker 登录配置和嵌套归档；仅 Docker 包固定位置的 `runtime.env.example` 及仓库已知占位值可以通过。错误日志只报告固定规则与文件名，不输出命中内容或不可信 JSON 字段。

本地只运行伪产物正反测试，不连接 Registry 或生产主机：

```bash
bash scripts/release/tests/artifact-scan-test.sh
```

检查真实候选目录时，本地 Docker 必须已存在与 Manifest 匹配的版本和 Commit 标签，且扫描器可以读取本机 Registry 的远端标签：

```bash
COINSPHERE_REGISTRY=127.0.0.1:5000 \
  python3 scripts/release/scan-artifacts.py vX.Y.Z <40位commit> dist
```

## 持久型 Runner 清理

- CoinSphere 使用独立的 `coinsphere-release` Buildx Builder，并通过宿主机网络和项目内 BuildKit 镜像源配置复用服务器现有出站链路；缓存清理不会操作其他项目的 Builder。
- Go 模块、Go 编译结果和 pnpm store 使用 CoinSphere 专用 BuildKit cache mount；发布版本号变化只会重建前端产物，不会使依赖安装层失效。缓存仍在保留期内时，依赖锁文件变化只下载缺失内容。
- Runner 的 `HTTP_PROXY`、`HTTPS_PROXY` 和 `NO_PROXY`（或对应小写变量）供发布作业的出站工具使用；只有构建脚本会把这些变量显式转交给 BuildKit。不得通过 Docker 客户端全局 `proxies` 为 Builder 配置代理，因为该配置也会注入 Compose 运行容器。
- `scripts/release/cleanup-runner.sh` 只删除当前仓库的 `dist`、过期的 `RUNNER_TEMP` 内容和 CoinSphere Builder 缓存，不执行 `docker system prune`，不删除容器、数据卷或其他仓库镜像。
- `scripts/release/prune-registry.sh` 只允许访问 `127.0.0.1:5000`，默认仅预演；发布工作流显式传入 `--apply`。认证复用 Runner 本地 Docker 登录配置，凭据不会进入命令参数或日志。
- Registry Manifest 删除后，未引用 Blob 仍需在独立维护窗口执行 Registry garbage collection。共享 Registry 运行期间禁止自动执行 GC，避免与其他项目推送并发造成数据损坏。
- Action 下载目录、工具缓存和 Runner 诊断日志保留给 Runner 自身管理；GitHub Artifact 按 14 天策略由 GitHub 清理，GitHub Release 长期保留。

## 自动回滚

最终产物扫描发生在部署前。扫描失败时没有服务或数据变更，不执行部署回滚，也不会上传 Actions Artifact 或创建 GitHub Release；构建阶段为取得 RepoDigest 已写入主机本地 Registry 的候选标签可能保留，按失败记录核对后由 Registry 保留策略或维护窗口清理，禁止为此自动删除仍可能被其他标签引用的 Manifest。

migration、Compose 启动或 `/health` 任一步失败时，`deploy.sh` 会：

1. 停止失败版本。
2. 删除失败版本使用的 SQLite 数据卷，并从部署前 tar 备份恢复；首次部署前没有数据卷时直接清理新卷。
3. 恢复上一份 Compose 和镜像版本文件。
4. 拉取并重新启动上一固定版本。

脚本保留最近 10 份 SQLite 备份。它不会执行 migration Down，也不会修改 `schema_migrations` 伪造回滚。

关机期间已认领的工作流执行会在可收尾时进入既有 `retry_waiting` 或 `failed`；若进程在 40 秒宽限后仍被强制停止，遗留的 `running` 记录继续由既有 stale recovery 在下一次启动后处理，不得手工改写状态。

## 手工恢复

自动回滚失败时，先保持服务停止，再检查：

```bash
cd /home/infrastructure/dpanel/compose/coinsphere-go
docker compose --env-file .env -f compose.yaml ps
ls -lt backups/
```

恢复到 Registry 中仍存在的上一版本，可在部署目录执行：

```bash
./deploy.sh vX.Y.Z
```

Release workflow 自动部署时调用 `./deploy.sh vX.Y.Z release-manifest.json`，脚本严格校验 Manifest 后把两个镜像 digest 写入部署 `.env`。不传 Manifest 的单参数模式只用于管理员明确选择旧版本标签的手工恢复。

管理员初始密码由服务器首次准备时随机生成。需要首次登录时，只在 SSH 终端读取 `runtime.env`，登录并改密后从该文件移除 `COINSPHERE_AUTH__BOOTSTRAP_ADMIN_PASSWORD`；不要把值发送到聊天、Issue、PR 或 Actions 日志。

任何回滚都要记录失败版本、时间线、备份文件、健康检查和恢复结果。交易能力落地后，发布前还必须先停止新增敞口并按交易应急手册处理活动订单。

## 本门禁回滚

本次门禁不修改数据库或业务运行时数据。代码回滚时整体回退本 PR，并恢复 Release workflow 原有校验和与部署调用；已生成的候选 Artifact 和 Registry 标签不得视为已扫描产物，必须停止后续上传与部署。生产 `.env` 中已写入的 digest 引用可继续运行，无需改回版本标签。

## A1-1 生命周期回滚

A1-1 不修改 schema、业务数据、API 契约或 Release 工作流。需要回滚时整体回退本 PR，恢复上一固定镜像与 Compose 文件；无需执行 migration Down。已进入 `retry_waiting` 或 `failed` 的执行继续按原有运行时语义处理，禁止为回滚手工改写执行状态。Python Worker、Outbox、WebSocket 正常态协议、Auth 和迁移切换不属于本次回滚范围。

## A1-2 Worker 任务 schema 回滚

`00002` 不启用 Worker 或产生任务。手工回滚前必须使用当前迁移二进制确认 `worker_tasks` 为空，再执行一次 Down 并确认版本回到 `1`；非空队列会拒绝 Down，禁止删除任务或篡改 migration 版本。自动发布回滚继续通过部署前 SQLite 备份恢复 schema 和数据，不额外执行 Down。
