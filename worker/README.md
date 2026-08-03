# CoinSphere Quant Worker

Python 3.12 Worker 当前实现 A1 PostgreSQL 任务基础设施：原子认领、唯一租约、周期心跳、崩溃回收、尝试次数和 5 秒内取消。数据集、vectorbt、NautilusTrader、行情、回测和交易能力尚未加入；当前只接受 `contract.noop` 与有限时长的 `contract.sleep` 伪任务来验证协议。

```bash
uv sync --locked --all-groups
uv run ruff check .
uv run mypy coinsphere_worker tests
uv run pytest
```

设置仅供测试的 PostgreSQL DSN 后，Pytest 会在随机隔离 schema 中运行并发与取消用例，并在结束时只删除该随机 schema：

```bash
export COINSPHERE_TEST_POSTGRES_DSN='postgresql://coinsphere:test-only@127.0.0.1:5432/coinsphere_worker_test?sslmode=disable'
uv run pytest tests/test_queue_runtime.py
```

## 运行契约

`python -m coinsphere_worker run` 必须通过 `COINSPHERE_WORKER_DATABASE_DSN` 连接已经应用 CoinSphere 单一基线的 PostgreSQL/TimescaleDB；配置缺失或数据库异常时进程 fail-closed 非零退出。单进程串行执行一个任务，多个 Worker 依赖 PostgreSQL `FOR UPDATE SKIP LOCKED` 并发认领，不使用 Redis、Kafka 或 NATS。

认领会递增 `attempt_count` 并创建新的 `lease_id`。启动、心跳、成功、失败和取消均同时校验任务 ID、租约 ID、合法前态和数据库时间；租约过期后旧 Worker 不能再续租或写终态。过期的 `claimed/running` 在仍有尝试次数时重新排队，否则进入 `failed`。心跳对 `cancelRequested` 只观察、不续租；恢复器在租约过期或取消请求满 4 秒时将其转为 `canceled`，以默认 1 秒轮询保证 Owner 在确认取消前崩溃时仍满足 5 秒时限。SIGINT/SIGTERM 会停止认领和心跳，在途任务由同一过期租约路径恢复。

日志覆盖启动停止、认领、状态转换、心跳异常、恢复、取消和终态，只包含任务、Worker、租约、状态与固定错误分类。禁止记录 DSN、环境值、原始 `payload_json`、凭据、令牌或个人数据。

## 容器契约

根目录开发 Compose 使用内部数据库网络启动共享 TimescaleDB、一次性 migration、Backend 和 Worker。Worker 保持只读文件系统、移除全部 Linux capabilities、启用 `no-new-privileges`，不暴露端口、不挂载业务数据卷，也不持有交易所凭据。

```bash
docker compose up --detach --build --wait worker
docker compose exec -T worker python -m coinsphere_worker health
```

健康检查会在三秒连接预算内确认 `worker_tasks` 可访问，成功输出固定 JSON：

```json
{"mode":"a1-postgres","protocolVersion":1,"role":"quant-worker","status":"healthy","taskConsumer":true}
```

## 回滚

先停止 Worker，确认不再产生心跳或认领，再回退 Worker 代码、开发 Compose 和 CI 配置。已存在的任务及 migration 版本必须保留，等待兼容版本恢复消费；不得为了代码回滚删除任务、修改状态或强制执行 `00001` Down。生产 Release 当前不构建或部署 Worker，因此不需要修改生产 Compose、镜像 digest 或发布产物。
