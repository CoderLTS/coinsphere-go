from __future__ import annotations

import hashlib
import json
import threading
from collections.abc import Mapping, Sequence
from datetime import UTC, datetime, timedelta
from decimal import Decimal
from gzip import decompress
from pathlib import Path

import pytest

from coinsphere_worker.artifacts import (
    ArtifactError,
    ArtifactFile,
    Manifest,
    canonical_json,
    freeze_records,
    utc_text,
)
from coinsphere_worker.backtest import (
    BacktestCanceledError,
    BacktestConfig,
    BacktestError,
    BacktestProcessError,
    BacktestProcessLimits,
    BacktestTimeoutError,
    MissingFundingError,
    run_backtest,
    run_backtest_isolated,
)
from coinsphere_worker.lanes import LaneBusyError, LaneRuntime, WorkerLane
from coinsphere_worker.strategy import (
    Candle,
    JSONScalar,
    LoadedStrategy,
    ParameterSpec,
    StrategyExecutionError,
    StrategyTimeoutError,
    StrategyValidationError,
    call_on_bar,
    load_strategy,
)


def candle(
    index: int,
    *,
    open: str,
    high: str,
    low: str,
    close: str,
) -> Candle:
    start = datetime(2026, 1, 1, tzinfo=UTC) + timedelta(minutes=index)
    return Candle(
        instrument_id="BTCUSDT",
        interval="1m",
        open_time=start,
        close_time=start + timedelta(minutes=1),
        open=Decimal(open),
        high=Decimal(high),
        low=Decimal(low),
        close=Decimal(close),
        base_volume=Decimal("1"),
    )


def strategy(
    source: str, *, market: str = "spot", lookback_bars: int = 1
) -> LoadedStrategy:
    return load_strategy(
        source,
        market=market,
        symbol="BTCUSDT",
        interval="1m",
        lookback_bars=lookback_bars,
    )


def test_spot_golden_case_is_next_open_decimal_and_deterministic() -> None:
    bars = [
        candle(0, open="100", high="101", low="99", close="100"),
        candle(1, open="101", high="103", low="100", close="102"),
        candle(2, open="110", high="111", low="109", close="110"),
    ]
    loaded = strategy(
        "def on_bar(candles, params):\n"
        "    return Decimal('1') if len(candles) == 2 else Decimal('0')\n",
        lookback_bars=2,
    )
    config = BacktestConfig(
        market="spot",
        allocation_usdt=Decimal("100"),
        initial_equity=Decimal("1000"),
        fee_rate=Decimal("0.001"),
        slippage_rate=Decimal("0.01"),
    )

    first = run_backtest(loaded, bars, {}, config)
    second = run_backtest(loaded, bars, {}, config)

    assert first.to_records() == second.to_records()
    assert [item.target for item in first.targets] == [
        Decimal("0"),
        Decimal("1"),
        Decimal("1"),
    ]
    assert len(first.orders) == 1
    assert first.orders[0].candle_open_time == bars[2].open_time
    assert first.fills[0].price == Decimal("111.10")
    assert first.fills[0].notional == Decimal("101.0")
    assert first.fees[0].amount == Decimal("0.1010")


def test_strategy_boundary_rejects_invalid_result_exception_and_timeout() -> None:
    bar = candle(0, open="100", high="101", low="99", close="100")
    sources = (
        "def on_bar(candles, params):\n    return 1\n",
        "def on_bar(candles, params):\n    return Decimal('NaN')\n",
        "def on_bar(candles, params):\n    return Decimal('2')\n",
        "def on_bar(candles, params):\n    return Decimal('1') / Decimal('0')\n",
    )
    for source in sources:
        with pytest.raises(StrategyExecutionError):
            strategy(source).signal([bar], {})

    release = threading.Event()

    def slow_on_bar(
        _candles: Sequence[Candle], _params: Mapping[str, JSONScalar]
    ) -> Decimal:
        release.wait(1)
        return Decimal("0")

    try:
        with pytest.raises(StrategyTimeoutError):
            call_on_bar(
                slow_on_bar,
                [bar],
                {},
                market="spot",
                timeout_seconds=0.01,
            )
    finally:
        release.set()


def test_candle_rejects_non_boolean_closed_flag() -> None:
    with pytest.raises(StrategyValidationError):
        Candle(
            instrument_id="BTCUSDT",
            interval="1m",
            open_time=datetime(2026, 1, 1, tzinfo=UTC),
            close_time=datetime(2026, 1, 1, 0, 1, tzinfo=UTC),
            open=Decimal("100"),
            high=Decimal("101"),
            low=Decimal("99"),
            close=Decimal("100"),
            base_volume=Decimal("1"),
            is_closed=1,  # type: ignore[arg-type]
        )


def test_strategy_metadata_params_and_candle_identity_are_fixed() -> None:
    schema = {
        "threshold": ParameterSpec(
            "decimal",
            minimum=Decimal("0"),
            maximum=Decimal("1"),
        )
    }
    loaded = load_strategy(
        "from __future__ import annotations\n"
        "LABEL = 'threshold'\n"
        "def value(params):\n"
        "    return params[LABEL]\n"
        "def on_bar(candles: Sequence[Candle], params: Mapping[str, JSONScalar]) -> Decimal:\n"
        "    return value(params)\n",
        market="spot",
        symbol="BTCUSDT",
        interval="1m",
        lookback_bars=1,
        parameter_schema=schema,
    )
    schema.clear()
    assert loaded.signal(
        [candle(0, open="100", high="101", low="99", close="100")],
        {"threshold": Decimal("0.5")},
    ) == Decimal("0.5")
    with pytest.raises(StrategyValidationError):
        loaded.signal(
            [candle(0, open="100", high="101", low="99", close="100")],
            {"threshold": 0.5},  # type: ignore[dict-item]
        )
    with pytest.raises(StrategyValidationError):
        load_strategy(
            "def on_bar(candles, params):\n    return Decimal('0')\n",
            market="spot",
            symbol="",
        )


def test_strategy_params_are_passed_in_canonical_name_order() -> None:
    loaded = strategy(
        "def on_bar(candles, params):\n"
        "    for name in params:\n"
        "        return Decimal('1') if name == 'a' else Decimal('0')\n"
    )
    bar = candle(0, open="100", high="101", low="99", close="100")
    assert loaded.signal([bar], {"b": Decimal("0"), "a": Decimal("0")}) == Decimal("1")


def test_usdm_funding_stop_gap_and_same_bar_liquidation_use_adverse_path() -> None:
    loaded = strategy(
        "def on_bar(candles, params):\n    return Decimal('1')\n",
        market="usd_m",
    )
    gap_bars = [
        candle(0, open="100", high="101", low="99", close="100"),
        candle(1, open="100", high="105", low="95", close="100"),
        candle(2, open="80", high="82", low="75", close="80"),
    ]
    gap_result = run_backtest(
        loaded,
        gap_bars,
        {},
        BacktestConfig(
            market="usd_m",
            allocation_usdt=Decimal("100"),
            initial_equity=Decimal("1000"),
            fee_rate=Decimal("0"),
            slippage_rate=Decimal("0"),
            funding_rates=[Decimal("0"), Decimal("0.01"), Decimal("0")],
            stop_loss_ratio=Decimal("0.1"),
        ),
    )
    assert gap_result.funding[0].amount == Decimal("-1.00")
    assert gap_result.orders[-1].reason == "stop_loss"
    assert gap_result.fills[-1].price == Decimal("80")

    adverse_bars = [
        candle(0, open="100", high="101", low="99", close="100"),
        candle(1, open="100", high="101", low="10", close="100"),
    ]
    adverse_result = run_backtest(
        loaded,
        adverse_bars,
        {},
        BacktestConfig(
            market="usd_m",
            allocation_usdt=Decimal("1000"),
            initial_equity=Decimal("1000"),
            fee_rate=Decimal("0"),
            slippage_rate=Decimal("0"),
            funding_rates=[Decimal("0"), Decimal("0")],
            stop_loss_ratio=Decimal("0.1"),
            maintenance_margin_ratio=Decimal("0.2"),
        ),
    )
    assert adverse_result.orders[-1].reason == "liquidation"
    assert adverse_result.fills[-1].price == Decimal("10")
    assert adverse_result.liquidations[-1].reason == "liquidation"

    with pytest.raises(MissingFundingError):
        BacktestConfig(
            market="usd_m",
            allocation_usdt=Decimal("100"),
            initial_equity=Decimal("1000"),
            fee_rate=Decimal("0"),
            slippage_rate=Decimal("0"),
        )


def test_usdm_allocation_cannot_exceed_equity_without_leverage() -> None:
    with pytest.raises(BacktestError):
        BacktestConfig(
            market="usd_m",
            allocation_usdt=Decimal("1000.01"),
            initial_equity=Decimal("1000"),
            fee_rate=Decimal("0"),
            slippage_rate=Decimal("0"),
            funding_rates=[],
        )


def test_long_backtest_lane_does_not_occupy_realtime_slot() -> None:
    runtime = LaneRuntime()
    started = threading.Event()
    release = threading.Event()

    def long_backtest() -> None:
        started.set()
        release.wait(1)

    thread = threading.Thread(target=lambda: runtime.run_backtest(long_backtest))
    thread.start()
    assert started.wait(1)
    try:
        assert runtime.run_realtime(lambda: "realtime") == "realtime"
        with pytest.raises(LaneBusyError):
            runtime.run(WorkerLane.BACKTEST, lambda: None)
    finally:
        release.set()
        thread.join(1)
    assert not thread.is_alive()


def test_isolated_backtest_returns_result_and_terminates_timeout() -> None:
    bar = candle(0, open="100", high="101", low="99", close="100")
    config = BacktestConfig(
        market="spot",
        allocation_usdt=Decimal("100"),
        initial_equity=Decimal("1000"),
        fee_rate=Decimal("0"),
        slippage_rate=Decimal("0"),
    )
    limits = BacktestProcessLimits(
        wall_seconds=5,
        cpu_seconds=5,
        memory_bytes=512 * 1024 * 1024,
        artifact_bytes=1024 * 1024,
    )
    result = run_backtest_isolated(
        strategy("def on_bar(candles, params):\n    return Decimal('0')\n"),
        [bar],
        {},
        config,
        limits,
    )
    assert result.final_equity == Decimal("1000")

    tiny_limits = BacktestProcessLimits(
        wall_seconds=5,
        cpu_seconds=5,
        memory_bytes=512 * 1024 * 1024,
        artifact_bytes=1,
    )
    with pytest.raises(BacktestProcessError) as error:
        run_backtest_isolated(
            strategy("def on_bar(candles, params):\n    return Decimal('0')\n"),
            [bar],
            {},
            config,
            tiny_limits,
        )
    assert error.value.category == "artifact_limit"

    timeout_limits = BacktestProcessLimits(
        wall_seconds=0.75,
        cpu_seconds=5,
        memory_bytes=512 * 1024 * 1024,
        artifact_bytes=1024 * 1024,
    )
    with pytest.raises(BacktestTimeoutError):
        run_backtest_isolated(
            strategy(
                "def on_bar(candles, params):\n"
                "    while True:\n"
                "        pass\n"
            ),
            [bar],
            {},
            config,
            timeout_limits,
        )

    cancel_event = threading.Event()
    timer = threading.Timer(0.2, cancel_event.set)
    timer.start()
    try:
        with pytest.raises(BacktestCanceledError):
            run_backtest_isolated(
                strategy(
                    "def on_bar(candles, params):\n"
                    "    while True:\n"
                    "        pass\n"
                ),
                [bar],
                {},
                config,
                limits,
                cancel_event=cancel_event,
            )
    finally:
        timer.cancel()


def test_freeze_records_is_reproducible_and_content_addressed(tmp_path: Path) -> None:
    inputs = [
        {"openTime": datetime(2026, 1, 1, tzinfo=UTC), "open": Decimal("1.00")},
        {"openTime": datetime(2026, 1, 1, 0, 1, tzinfo=UTC), "open": Decimal("2")},
    ]
    results = [{"returnUsdt": Decimal("1.50"), "type": "summary"}]
    first = freeze_records(tmp_path / "first", input_records=inputs, result_records=results)
    second = freeze_records(tmp_path / "second", input_records=inputs, result_records=results)

    assert first.sha256 == second.sha256
    assert first.to_record() == second.to_record()
    for entry in first.files:
        path = tmp_path / "first" / entry.path
        assert hashlib.sha256(path.read_bytes()).hexdigest() == entry.sha256
        assert entry.size == path.stat().st_size
    input_path = tmp_path / "first" / (first.references or {})["input"]
    assert decompress(input_path.read_bytes()).decode() == "".join(
        canonical_json(record) + "\n" for record in inputs
    )
    manifest_record = json.loads((tmp_path / "first" / "manifest.json").read_text())
    assert manifest_record == first.to_record()

    reversed_manifest = freeze_records(
        tmp_path / "reversed",
        input_records=reversed(inputs),
        result_records=results,
    )
    assert (first.references or {})["input"] != (reversed_manifest.references or {})["input"]

    with pytest.raises(ArtifactError):
        freeze_records(
            tmp_path / "invalid",
            input_records=[{1: "not-json"}],
            result_records=results,
        )


def test_manifest_rejects_path_aliases_invalid_files_and_dangling_references(
    tmp_path: Path,
) -> None:
    file = ArtifactFile("objects/data.jsonl.gz", "0" * 64, 0)
    with pytest.raises(ArtifactError):
        ArtifactFile("objects/./data.jsonl.gz", "0" * 64, 0)
    with pytest.raises(ArtifactError):
        Manifest((file, "objects/data.jsonl.gz"))  # type: ignore[arg-type]
    with pytest.raises(ArtifactError):
        Manifest((file,), references={"input": "objects/missing.jsonl.gz"})
    with pytest.raises(ArtifactError):
        freeze_records(
            tmp_path / "invalid-references",
            input_records=[],
            result_records=[],
            references=[],  # type: ignore[arg-type]
        )
    with pytest.raises(ArtifactError):
        ArtifactFile("C:/objects/data.jsonl.gz", "0" * 64, 0)
    with pytest.raises(ArtifactError):
        ArtifactFile("C:objects/data.jsonl.gz", "0" * 64, 0)
    with pytest.raises(ArtifactError):
        ArtifactFile("objects/\x00data.jsonl.gz", "0" * 64, 0)
    with pytest.raises(ArtifactError):
        ArtifactFile("objects/\x7fdata.jsonl.gz", "0" * 64, 0)
    with pytest.raises(ArtifactError):
        utc_text("not-a-datetime")
    assert utc_text(datetime(1, 1, 1, tzinfo=UTC)) == "0001-01-01T00:00:00.000000000Z"
