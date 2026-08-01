# CoinSphere Quant Worker

Python 3.12 Worker 将承载不可变数据集生成、vectorbt 向量回测和 NautilusTrader 事件回测。

A0 仅建立运行时契约与质量工具，不包含策略执行能力。Worker 当前没有网络和交易权限；后续任务也不得直接持有交易所凭据。

```bash
uv sync --locked --all-groups
uv run ruff check .
uv run mypy coinsphere_worker tests
uv run pytest
```
