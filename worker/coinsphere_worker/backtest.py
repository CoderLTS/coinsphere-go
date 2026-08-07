"""A narrow deterministic Decimal bar backtester."""

from __future__ import annotations

import importlib
import math
import os
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from datetime import UTC, datetime
from decimal import Context, Decimal, localcontext
from multiprocessing import get_context
from multiprocessing.connection import Connection
from threading import Event
from time import monotonic
from typing import Any, Final, cast

from .artifacts import decimal_text, jsonl_gzip_bytes, utc_text
from .strategy import (
    Candle,
    JSONScalar,
    LoadedStrategy,
    Market,
    ParameterSpec,
    StrategyFunction,
    call_on_bar,
    load_strategy,
    validate_candles,
)


class BacktestError(ValueError):
    """The input or execution model is invalid."""


class MissingFundingError(BacktestError):
    """USD-M input is missing a funding rate for a required candle."""


class BacktestTimeoutError(BacktestError):
    """The isolated backtest exceeded its wall-clock limit."""


class BacktestCanceledError(BacktestError):
    """The isolated backtest was canceled by its owner."""


class BacktestProcessError(BacktestError):
    """The isolated backtest failed or exited without a result."""

    def __init__(self, category: str) -> None:
        self.category = category
        super().__init__(f"isolated backtest failed: {category}")


_BACKTEST_CONTEXT = Context(prec=50)


@dataclass(frozen=True, slots=True)
class BacktestProcessLimits:
    wall_seconds: float
    cpu_seconds: int
    memory_bytes: int
    artifact_bytes: int

    def __post_init__(self) -> None:
        if (
            isinstance(self.wall_seconds, bool)
            or not isinstance(self.wall_seconds, (int, float))
            or not math.isfinite(self.wall_seconds)
            or self.wall_seconds <= 0
        ):
            raise BacktestError("wall_seconds must be a positive finite number")
        for name in ("cpu_seconds", "memory_bytes", "artifact_bytes"):
            value = getattr(self, name)
            if isinstance(value, bool) or not isinstance(value, int) or value <= 0:
                raise BacktestError(f"{name} must be a positive integer")


@dataclass(frozen=True, slots=True)
class BacktestConfig:
    market: Market | str
    allocation_usdt: Decimal
    initial_equity: Decimal
    fee_rate: Decimal
    slippage_rate: Decimal
    funding_rates: Mapping[datetime, Decimal] | Sequence[Decimal] | None = None
    stop_loss_ratio: Decimal | None = None
    maintenance_margin_ratio: Decimal | None = None

    def __post_init__(self) -> None:
        if str(self.market) not in {Market.SPOT.value, Market.USD_M.value}:
            raise BacktestError("unsupported market")
        for name in ("allocation_usdt", "initial_equity", "fee_rate", "slippage_rate"):
            value = cast(Decimal, getattr(self, name))
            if not isinstance(value, Decimal) or not value.is_finite():
                raise BacktestError(f"{name} must be a finite Decimal")
        if self.allocation_usdt <= 0 or self.initial_equity <= 0:
            raise BacktestError("allocation_usdt and initial_equity must be positive")
        if self.fee_rate < 0 or self.fee_rate >= 1:
            raise BacktestError("fee_rate must be between zero and one")
        if self.slippage_rate < 0 or self.slippage_rate >= 1:
            raise BacktestError("slippage_rate must be between zero and one")
        if self.allocation_usdt > self.initial_equity:
            raise BacktestError(
                f"{str(self.market)} allocation_usdt must not exceed initial_equity"
            )
        for name in ("stop_loss_ratio", "maintenance_margin_ratio"):
            optional_value = cast(Decimal | None, getattr(self, name))
            if optional_value is not None and (
                not isinstance(optional_value, Decimal)
                or not optional_value.is_finite()
                or not Decimal("0") < optional_value < Decimal("1")
            ):
                raise BacktestError(f"{name} must be between zero and one")
        if str(self.market) == Market.USD_M.value and self.funding_rates is None:
            raise MissingFundingError("USD-M backtests require explicit funding rates")


@dataclass(frozen=True, slots=True)
class TargetRecord:
    candle_close_time: datetime
    target: Decimal

    def to_record(self) -> dict[str, str]:
        return {
            "candleCloseTime": utc_text(self.candle_close_time),
            "target": decimal_text(self.target),
            "type": "target",
        }


@dataclass(frozen=True, slots=True)
class OrderRecord:
    order_id: str
    candle_open_time: datetime
    side: str
    quantity: Decimal
    target: Decimal
    reason: str

    def to_record(self) -> dict[str, str]:
        return {
            "candleOpenTime": utc_text(self.candle_open_time),
            "orderId": self.order_id,
            "quantity": decimal_text(self.quantity),
            "reason": self.reason,
            "side": self.side,
            "target": decimal_text(self.target),
            "type": "order",
        }


@dataclass(frozen=True, slots=True)
class FillRecord:
    order_id: str
    candle_open_time: datetime
    side: str
    quantity: Decimal
    price: Decimal
    notional: Decimal

    def to_record(self) -> dict[str, str]:
        return {
            "candleOpenTime": utc_text(self.candle_open_time),
            "notional": decimal_text(self.notional),
            "orderId": self.order_id,
            "price": decimal_text(self.price),
            "quantity": decimal_text(self.quantity),
            "side": self.side,
            "type": "fill",
        }


@dataclass(frozen=True, slots=True)
class FeeRecord:
    candle_open_time: datetime
    amount: Decimal
    reason: str

    def to_record(self) -> dict[str, str]:
        return {
            "amount": decimal_text(self.amount),
            "candleOpenTime": utc_text(self.candle_open_time),
            "reason": self.reason,
            "type": "fee",
        }


@dataclass(frozen=True, slots=True)
class FundingRecord:
    candle_close_time: datetime
    rate: Decimal
    amount: Decimal

    def to_record(self) -> dict[str, str]:
        return {
            "amount": decimal_text(self.amount),
            "candleCloseTime": utc_text(self.candle_close_time),
            "rate": decimal_text(self.rate),
            "type": "funding",
        }


@dataclass(frozen=True, slots=True)
class EquityRecord:
    candle_close_time: datetime
    equity: Decimal
    drawdown: Decimal

    def to_record(self) -> dict[str, str]:
        return {
            "candleCloseTime": utc_text(self.candle_close_time),
            "drawdown": decimal_text(self.drawdown),
            "equity": decimal_text(self.equity),
            "type": "equity",
        }


@dataclass(frozen=True, slots=True)
class LiquidationRecord:
    candle_close_time: datetime
    price: Decimal
    reason: str

    def to_record(self) -> dict[str, str]:
        return {
            "candleCloseTime": utc_text(self.candle_close_time),
            "price": decimal_text(self.price),
            "reason": self.reason,
            "type": "liquidation",
        }


@dataclass(frozen=True, slots=True)
class BacktestResult:
    targets: tuple[TargetRecord, ...]
    orders: tuple[OrderRecord, ...]
    fills: tuple[FillRecord, ...]
    fees: tuple[FeeRecord, ...]
    funding: tuple[FundingRecord, ...]
    equity_curve: tuple[EquityRecord, ...]
    liquidations: tuple[LiquidationRecord, ...]
    initial_equity: Decimal
    final_equity: Decimal
    return_usdt: Decimal
    return_ratio: Decimal
    max_drawdown: Decimal

    @property
    def target_sequence(self) -> tuple[TargetRecord, ...]:
        return self.targets

    @property
    def funding_events(self) -> tuple[FundingRecord, ...]:
        return self.funding

    @property
    def liquidation_events(self) -> tuple[LiquidationRecord, ...]:
        return self.liquidations

    @property
    def drawdown(self) -> tuple[EquityRecord, ...]:
        return self.equity_curve

    def to_records(self) -> list[dict[str, object]]:
        records: list[dict[str, object]] = []
        for group in (
            self.targets,
            self.orders,
            self.fills,
            self.fees,
            self.funding,
            self.equity_curve,
            self.liquidations,
        ):
            records.extend(
                {key: value for key, value in item.to_record().items()} for item in group
            )
        records.append(
            {
                "finalEquity": decimal_text(self.final_equity),
                "initialEquity": decimal_text(self.initial_equity),
                "maxDrawdown": decimal_text(self.max_drawdown),
                "returnRatio": decimal_text(self.return_ratio),
                "returnUsdt": decimal_text(self.return_usdt),
                "type": "summary",
            }
        )
        return records

    def to_dict(self) -> dict[str, object]:
        return {
            "targets": [item.to_record() for item in self.targets],
            "orders": [item.to_record() for item in self.orders],
            "fills": [item.to_record() for item in self.fills],
            "fees": [item.to_record() for item in self.fees],
            "funding": [item.to_record() for item in self.funding],
            "equity": [item.to_record() for item in self.equity_curve],
            "liquidations": [item.to_record() for item in self.liquidations],
            "summary": self.to_records()[-1],
        }


def _utc_key(value: datetime) -> datetime:
    if (
        not isinstance(value, datetime)
        or value.tzinfo is None
        or value.utcoffset() != UTC.utcoffset(value)
    ):
        raise MissingFundingError("funding timestamps must be timezone-aware UTC")
    return value.astimezone(UTC)


def _funding_map(config: BacktestConfig, candles: Sequence[Candle]) -> dict[datetime, Decimal]:
    if str(config.market) != Market.USD_M.value:
        return {}
    source = config.funding_rates
    if isinstance(source, Mapping):
        try:
            values = {_utc_key(key): value for key, value in source.items()}
        except (AttributeError, TypeError) as exc:
            raise MissingFundingError("funding timestamps must be timezone-aware UTC") from exc
    elif source is not None:
        if len(source) != len(candles):
            raise MissingFundingError("USD-M funding rates must cover every candle")
        values = {
            _utc_key(candle.close_time): rate for candle, rate in zip(candles, source, strict=True)
        }
    else:
        raise MissingFundingError("USD-M backtests require explicit funding rates")
    for candle in candles:
        rate = values.get(_utc_key(candle.close_time))
        if not isinstance(rate, Decimal) or not rate.is_finite():
            raise MissingFundingError(f"missing funding rate at {utc_text(candle.close_time)}")
    return values


def _mark_equity(
    market: str, cash: Decimal, qty: Decimal, avg_entry: Decimal, price: Decimal
) -> Decimal:
    if market == Market.SPOT.value:
        return cash + qty * price
    return cash + (price - avg_entry) * qty if qty else cash


def _divide(left: Decimal, right: Decimal) -> Decimal:
    with localcontext(_BACKTEST_CONTEXT):
        return left / right


def run_backtest(
    strategy: LoadedStrategy | StrategyFunction,
    candles: Sequence[Candle],
    params: Mapping[str, JSONScalar],
    config: BacktestConfig,
    *,
    timeout_seconds: float | None = None,
) -> BacktestResult:
    """在固定 Decimal 精度下执行确定性回测。"""

    with localcontext(_BACKTEST_CONTEXT):
        return _run_backtest(
            strategy,
            candles,
            params,
            config,
            timeout_seconds=timeout_seconds,
        )


def _apply_process_limits(limits: BacktestProcessLimits) -> None:
    if os.name != "posix":
        return
    resource = cast(Any, importlib.import_module("resource"))

    resource.setrlimit(resource.RLIMIT_CPU, (limits.cpu_seconds, limits.cpu_seconds))
    resource.setrlimit(resource.RLIMIT_AS, (limits.memory_bytes, limits.memory_bytes))
    resource.setrlimit(resource.RLIMIT_FSIZE, (limits.artifact_bytes, limits.artifact_bytes))


def _run_backtest_process(
    connection: Connection,
    source: str,
    market: str,
    symbol: str,
    interval: str,
    lookback_bars: int,
    parameter_schema: Mapping[str, ParameterSpec] | None,
    runtime_version: str,
    candles: tuple[Candle, ...],
    params: dict[str, JSONScalar],
    config: BacktestConfig,
    limits: BacktestProcessLimits,
) -> None:
    os.environ.clear()
    try:
        _apply_process_limits(limits)
        loaded = load_strategy(
            source,
            market=market,
            symbol=symbol,
            interval=interval,
            lookback_bars=lookback_bars,
            parameter_schema=parameter_schema,
            runtime_version=runtime_version,
        )
        result = run_backtest(loaded, candles, params, config)
        if len(jsonl_gzip_bytes(result.to_records())) > limits.artifact_bytes:
            connection.send((None, "artifact_limit"))
        else:
            connection.send((result, None))
    except BaseException as exc:
        connection.send((None, type(exc).__name__))
    finally:
        connection.close()


def run_backtest_isolated(
    strategy: LoadedStrategy,
    candles: Sequence[Candle],
    params: Mapping[str, JSONScalar],
    config: BacktestConfig,
    limits: BacktestProcessLimits,
    *,
    cancel_event: Event | None = None,
) -> BacktestResult:
    """Run one trusted backtest in a bounded child process with a clean environment."""

    context = get_context("spawn")
    receive, send = context.Pipe(duplex=False)
    process = context.Process(
        target=_run_backtest_process,
        args=(
            send,
            strategy.source,
            str(strategy.spec.market),
            strategy.spec.symbol,
            strategy.spec.interval,
            strategy.spec.lookback_bars,
            dict(strategy.spec.parameter_schema or {}),
            strategy.spec.runtime_version,
            tuple(candles),
            dict(params),
            config,
            limits,
        ),
        daemon=True,
    )
    process_started = False
    try:
        process.start()
        process_started = True
        send.close()
        deadline = monotonic() + limits.wall_seconds
        while True:
            if cancel_event is not None and cancel_event.is_set():
                raise BacktestCanceledError("isolated backtest canceled")
            remaining = deadline - monotonic()
            if remaining <= 0:
                raise BacktestTimeoutError("isolated backtest timed out")
            if receive.poll(min(remaining, 0.05)):
                try:
                    result, category = receive.recv()
                except EOFError as exc:
                    raise BacktestProcessError(f"exit_{process.exitcode}") from exc
                break
    finally:
        send.close()
        receive.close()
        if process_started:
            process.join(0.1)
            if process.is_alive():
                process.terminate()
                process.join(1)
            if process.is_alive():
                process.kill()
                process.join(1)
    if category is not None:
        raise BacktestProcessError(cast(str, category))
    return cast(BacktestResult, result)


def _run_backtest(
    strategy: LoadedStrategy | StrategyFunction,
    candles: Sequence[Candle],
    params: Mapping[str, JSONScalar],
    config: BacktestConfig,
    *,
    timeout_seconds: float | None = None,
) -> BacktestResult:
    """Run closed bars with next-open target execution and no wall-clock reads."""

    bars = validate_candles(candles)
    if not bars:
        raise BacktestError("backtest requires at least one candle")
    if isinstance(strategy, LoadedStrategy) and str(strategy.spec.market) != str(config.market):
        raise BacktestError("strategy market does not match backtest market")
    funding = _funding_map(config, bars)
    market = str(config.market)
    targets: list[TargetRecord] = []
    orders: list[OrderRecord] = []
    fills: list[FillRecord] = []
    fees: list[FeeRecord] = []
    funding_events: list[FundingRecord] = []
    equity_curve: list[EquityRecord] = []
    liquidations: list[LiquidationRecord] = []
    cash = config.initial_equity
    qty = Decimal("0")
    avg_entry = Decimal("0")
    current_target = Decimal("0")
    pending: Decimal | None = None
    peak = config.initial_equity
    order_number = 0

    def execute(
        delta: Decimal,
        candle: Candle,
        target: Decimal,
        reason: str,
        execution_price: Decimal | None = None,
    ) -> None:
        nonlocal cash, qty, avg_entry, order_number
        if delta == 0:
            return
        side = "buy" if delta > 0 else "sell"
        raw_price = candle.open if execution_price is None else execution_price
        price = (
            raw_price * (Decimal("1") + config.slippage_rate)
            if delta > 0
            else raw_price * (Decimal("1") - config.slippage_rate)
        )
        quantity = abs(delta)
        notional = quantity * price
        order_number += 1
        order_id = f"order-{order_number:08d}"
        orders.append(OrderRecord(order_id, candle.open_time, side, quantity, target, reason))
        fills.append(FillRecord(order_id, candle.open_time, side, quantity, price, notional))
        fee = notional * config.fee_rate
        fees.append(FeeRecord(candle.open_time, fee, "trading"))
        old_qty = qty
        new_qty = old_qty + delta
        if market == Market.SPOT.value:
            cash += -notional - fee if delta > 0 else notional - fee
        else:
            closed = (
                min(abs(old_qty), abs(delta)) if old_qty and old_qty * delta < 0 else Decimal("0")
            )
            if closed:
                cash += (
                    (price - avg_entry) * closed * (Decimal("1") if old_qty > 0 else Decimal("-1"))
                )
            cash -= fee
        if not new_qty:
            avg_entry = Decimal("0")
        elif not old_qty or old_qty * new_qty < 0:
            avg_entry = price
        elif old_qty * delta > 0:
            avg_entry = _divide(abs(old_qty) * avg_entry + abs(delta) * price, abs(new_qty))
        qty = new_qty

    for index, candle in enumerate(bars):
        if pending is not None:
            if pending != current_target:
                desired_notional = config.allocation_usdt * pending
                desired_qty = _divide(desired_notional, candle.open)
                execute(desired_qty - qty, candle, pending, "target")
                current_target = pending
            pending = None

        if qty:
            if market == Market.USD_M.value:
                rate = funding[_utc_key(candle.close_time)]
                amount = -qty * candle.close * rate
                cash += amount
                funding_events.append(FundingRecord(candle.close_time, rate, amount))

            risk_reason: str | None = None
            risk_price: Decimal | None = None
            if config.stop_loss_ratio is not None:
                if qty > 0:
                    stop_price = avg_entry * (Decimal("1") - config.stop_loss_ratio)
                    breached = candle.low <= stop_price
                    risk_price = min(candle.open, stop_price) if breached else None
                else:
                    stop_price = avg_entry * (Decimal("1") + config.stop_loss_ratio)
                    breached = candle.high >= stop_price
                    risk_price = max(candle.open, stop_price) if breached else None
                if risk_price is not None:
                    risk_reason = "stop_loss"

            if config.maintenance_margin_ratio is not None:
                adverse_price = candle.low if qty > 0 else candle.high
                adverse_equity = _mark_equity(market, cash, qty, avg_entry, adverse_price)
                margin_limit = config.initial_equity * config.maintenance_margin_ratio
                if adverse_equity <= margin_limit:
                    # When stop and liquidation happen in one candle, use the worse fill.
                    risk_reason = "liquidation"
                    risk_price = adverse_price

            if risk_reason is not None and risk_price is not None:
                execute(-qty, candle, Decimal("0"), risk_reason, risk_price)
                current_target = Decimal("0")
                liquidations.append(LiquidationRecord(candle.close_time, risk_price, risk_reason))

        equity = _mark_equity(market, cash, qty, avg_entry, candle.close)
        peak = max(peak, equity)
        drawdown = _divide(equity - peak, peak) if peak else Decimal("0")
        equity_curve.append(EquityRecord(candle.close_time, equity, drawdown))
        target = call_on_bar(
            strategy,
            bars[
                max(
                    0,
                    index
                    + 1
                    - getattr(getattr(strategy, "spec", None), "lookback_bars", len(bars)),
                ) : index + 1
            ],
            params,
            market=market,
            timeout_seconds=timeout_seconds,
        )
        targets.append(TargetRecord(candle.close_time, target))
        pending = target

    final_equity = equity_curve[-1].equity
    return BacktestResult(
        tuple(targets),
        tuple(orders),
        tuple(fills),
        tuple(fees),
        tuple(funding_events),
        tuple(equity_curve),
        tuple(liquidations),
        config.initial_equity,
        final_equity,
        final_equity - config.initial_equity,
        _divide(final_equity - config.initial_equity, config.initial_equity),
        min((item.drawdown for item in equity_curve), default=Decimal("0")),
    )


backtest = run_backtest


__all__: Final = [
    "BacktestConfig",
    "BacktestCanceledError",
    "BacktestError",
    "BacktestProcessError",
    "BacktestProcessLimits",
    "BacktestResult",
    "BacktestTimeoutError",
    "EquityRecord",
    "FeeRecord",
    "FillRecord",
    "FundingRecord",
    "LiquidationRecord",
    "MissingFundingError",
    "OrderRecord",
    "TargetRecord",
    "backtest",
    "run_backtest",
    "run_backtest_isolated",
]
