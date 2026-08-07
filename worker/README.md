# CoinSphere Quant Worker

Python 3.12 Worker 实现 PostgreSQL 任务租约以及 M1.4 单文件策略与确定性回测核心：`strategy.py` 固定可信 Python 文件和统一 `on_bar` 契约，`backtest.py` 提供 Decimal Bar 模拟器与显式资源限制的子进程入口，`artifacts.py` 生成内容寻址 `JSONL.gz` 和 SHA-256 Manifest。未引入动态插件、运行时依赖安装、vectorbt、NautilusTrader 或逐任务 Docker。

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

`python -m coinsphere_worker run --lane realtime` 和 `python -m coinsphere_worker run --lane backtest` 必须通过 `COINSPHERE_WORKER_DATABASE_DSN` 连接已经应用 CoinSphere migration 的 PostgreSQL/TimescaleDB；配置缺失、lane 非法或数据库异常时进程 fail-closed 非零退出。每个消费者只认领本 lane，并按优先级、创建时间和任务 ID 稳定排序；两个消费者各自串行执行一个任务，不共享槽位，也不使用 Redis、Kafka 或 NATS。

回测业务处理器调用 `run_backtest_isolated` 时必须显式传入墙钟、CPU、内存和产物大小上限。子进程使用 `spawn`、清空环境并在 Linux 应用原生资源限制；超时会有界终止整个子进程。纯 `run_backtest` 只承载确定性金融计算，不读取时钟、环境、数据库或网络。

认领会递增 `attempt_count` 并创建新的 `lease_id`。启动、心跳、成功、失败和取消均同时校验任务 ID、租约 ID、合法前态和数据库时间；租约过期后旧 Worker 不能再续租或写终态。过期的 `claimed/running` 在仍有尝试次数时重新排队，否则进入 `failed`。心跳对 `cancelRequested` 只观察、不续租；恢复器在租约过期或取消请求满 4 秒时将其转为 `canceled`，以默认 1 秒轮询保证 Owner 在确认取消前崩溃时仍满足 5 秒时限。SIGINT/SIGTERM 会停止认领和心跳，在途任务由同一过期租约路径恢复。

日志覆盖启动停止、认领、状态转换、心跳异常、恢复、取消和终态，只包含任务、Worker、租约、状态与固定错误分类。禁止记录 DSN、环境值、原始 `payload_json`、凭据、令牌或个人数据。

## 容器契约

根目录开发 Compose 使用内部数据库网络启动共享 TimescaleDB、一次性 migration、Backend 和默认 realtime Worker。Worker 保持只读文件系统、移除全部 Linux capabilities、启用 `no-new-privileges`，不暴露端口，也不持有交易所凭据。晋级候选产物只通过调用方提供的独立目录写入，Manifest 中路径必须为相对 POSIX 路径。

```bash
docker compose up --detach --build --wait worker
docker compose exec -T worker python -m coinsphere_worker health
```

健康检查会在三秒连接预算内确认 `worker_tasks` 可访问，成功输出固定 JSON：

```json
{"mode":"a1-postgres","protocolVersion":1,"role":"quant-worker","status":"healthy","taskConsumer":true}
```

## 回滚

先停止 realtime/backtest 消费者，确认不再产生心跳或认领，再回退本纵向 PR。若不存在非默认 lane/priority 数据，可回退 `00005`；否则保留 schema 和任务，等待兼容版本恢复消费，不得删除任务或手工修改 migration 版本。未被晋级证据引用的临时产物按 Runbook 清理，已引用内容不得自动删除。生产 Release 当前不部署 Worker，不修改生产、真实交易或凭据配置。
