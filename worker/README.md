# CoinSphere Quant Worker

Python 3.12 Worker 将承载不可变数据集生成、vectorbt 向量回测和 NautilusTrader 事件回测。

A0 仅建立运行时契约、健康检查与质量工具，不包含任务领取、数据集生成或策略执行能力。容器以 `a0-idle` 模式保持前台运行，健康命令明确返回 `taskConsumer=false`。Worker 当前没有网络和交易权限；后续任务也不得直接持有交易所凭据。

```bash
uv sync --locked --all-groups
uv run ruff check .
uv run mypy coinsphere_worker tests
uv run pytest
```

## 容器契约

镜像固定 Python 3.12 与 uv 基础镜像摘要，并使用 `uv sync --locked --no-dev` 安装 `uv.lock` 中的运行时依赖：

```bash
docker build -t coinsphere-go-worker ./worker
docker run --rm coinsphere-go-worker python -m coinsphere_worker health
```

健康输出是稳定 JSON：

```json
{"mode":"a0-idle","protocolVersion":1,"role":"quant-worker","status":"healthy","taskConsumer":false}
```

根目录开发 Compose 进一步对 Worker 设置无网络、只读文件系统、移除全部 Linux capabilities 和 `no-new-privileges`，不暴露端口、不挂载卷，也不注入数据库或交易凭据。

## 回滚

本交付没有数据库迁移和持久化数据。回滚时执行 `docker compose rm --stop --force worker`，恢复此前的 Compose/CI 配置并删除本地 `coinsphere-go-worker` 镜像即可；Backend、Frontend 和生产发布 Compose 不受影响。
