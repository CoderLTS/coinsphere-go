"""Trusted single-file strategy loading and the shared ``on_bar`` contract."""

from __future__ import annotations
import __future__

import ast
import hashlib
import math
import threading
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from datetime import UTC, datetime
from decimal import Decimal
from enum import StrEnum
from pathlib import Path
from types import MappingProxyType
from typing import Any, Final, cast


class Market(StrEnum):
    SPOT = "spot"
    USD_M = "usd_m"


type JSONScalar = str | int | bool | Decimal
type StrategyFunction = Callable[[Sequence["Candle"], Mapping[str, JSONScalar]], Decimal]


class StrategyValidationError(ValueError):
    """The source, input candle sequence, parameters, or target is invalid."""


class StrategyExecutionError(RuntimeError):
    """The strategy raised an exception or returned an invalid target."""


class StrategyTimeoutError(StrategyExecutionError):
    """The strategy did not return before the caller's deadline."""


class ForbiddenStrategyError(StrategyValidationError):
    """The single-file source attempts to use a forbidden capability."""


@dataclass(frozen=True, slots=True, init=False)
class Candle:
    """An immutable, closed UTC OHLCV candle with Decimal values."""

    instrument_id: str
    interval: str
    open_time: datetime
    close_time: datetime
    open: Decimal
    high: Decimal
    low: Decimal
    close: Decimal
    base_volume: Decimal
    is_closed: bool

    def __init__(
        self,
        instrument_id: str = "",
        interval: str = "1m",
        open_time: datetime | None = None,
        close_time: datetime | None = None,
        open: Decimal | None = None,
        high: Decimal | None = None,
        low: Decimal | None = None,
        close: Decimal | None = None,
        base_volume: Decimal | None = None,
        is_closed: bool = True,
        *,
        volume: Decimal | None = None,
    ) -> None:
        if base_volume is None:
            base_volume = volume
        values = (open_time, close_time, open, high, low, close, base_volume)
        if any(value is None for value in values):
            raise TypeError("Candle requires UTC times and Decimal OHLCV values")
        object.__setattr__(self, "instrument_id", instrument_id)
        object.__setattr__(self, "interval", interval)
        object.__setattr__(self, "open_time", cast(datetime, open_time))
        object.__setattr__(self, "close_time", cast(datetime, close_time))
        object.__setattr__(self, "open", cast(Decimal, open))
        object.__setattr__(self, "high", cast(Decimal, high))
        object.__setattr__(self, "low", cast(Decimal, low))
        object.__setattr__(self, "close", cast(Decimal, close))
        object.__setattr__(self, "base_volume", cast(Decimal, base_volume))
        object.__setattr__(self, "is_closed", is_closed)
        validate_candle(self)

    @property
    def volume(self) -> Decimal:
        return self.base_volume


def _finite_decimal(value: object, field: str) -> Decimal:
    if not isinstance(value, Decimal) or not value.is_finite():
        raise StrategyValidationError(f"{field} must be a finite Decimal")
    return value


def _utc(value: object, field: str) -> datetime:
    if (
        not isinstance(value, datetime)
        or value.tzinfo is None
        or value.utcoffset() != UTC.utcoffset(value)
    ):
        raise StrategyValidationError(f"{field} must be timezone-aware UTC")
    return value.astimezone(UTC)


def _json_scalar(value: object, field: str) -> JSONScalar:
    if isinstance(value, bool):
        return value
    if isinstance(value, int) and not isinstance(value, bool):
        return value
    if isinstance(value, str):
        return value
    if isinstance(value, Decimal) and value.is_finite():
        return value
    raise StrategyValidationError(f"{field} must be a finite Decimal, integer, boolean, or string")


def validate_candle(candle: Candle) -> None:
    """Validate the trust-boundary invariants shared by realtime and backtest."""

    if (
        not isinstance(candle.instrument_id, str)
        or not candle.instrument_id
        or any(char.isspace() for char in candle.instrument_id)
        or not isinstance(candle.interval, str)
        or not candle.interval
        or any(char.isspace() for char in candle.interval)
    ):
        raise StrategyValidationError("candle identity must be non-whitespace strings")
    open_time = _utc(candle.open_time, "open_time")
    close_time = _utc(candle.close_time, "close_time")
    if close_time <= open_time:
        raise StrategyValidationError("close_time must be after open_time")
    if not isinstance(candle.is_closed, bool) or not candle.is_closed:
        raise StrategyValidationError("on_bar accepts closed candles only")
    values = {
        "open": candle.open,
        "high": candle.high,
        "low": candle.low,
        "close": candle.close,
        "base_volume": candle.base_volume,
    }
    for field, value in values.items():
        number = _finite_decimal(value, field)
        if field != "base_volume" and number <= 0:
            raise StrategyValidationError(f"{field} must be positive")
        if field == "base_volume" and number < 0:
            raise StrategyValidationError("base_volume must not be negative")
    if candle.low > min(candle.open, candle.close) or candle.high < max(candle.open, candle.close):
        raise StrategyValidationError("candle OHLC range is inconsistent")


def validate_candles(
    candles: Sequence[Candle], lookback_bars: int | None = None
) -> tuple[Candle, ...]:
    result = tuple(candles)
    if lookback_bars is not None and (
        isinstance(lookback_bars, bool)
        or not isinstance(lookback_bars, int)
        or lookback_bars < 1
        or len(result) > lookback_bars
    ):
        raise StrategyValidationError("candles exceed lookback_bars")
    previous: datetime | None = None
    for candle in result:
        if not isinstance(candle, Candle):
            raise StrategyValidationError("candles must contain Candle values")
        current = candle.open_time.astimezone(UTC)
        if previous is not None and current <= previous:
            raise StrategyValidationError("candles must be strictly time ordered")
        previous = current
    return result


def validate_target(value: object, market: Market | str) -> Decimal:
    target = _finite_decimal(value, "on_bar result")
    market_value = str(market)
    if market_value == Market.SPOT.value:
        lower, upper = Decimal("0"), Decimal("1")
    elif market_value == Market.USD_M.value:
        lower, upper = Decimal("-1"), Decimal("1")
    else:
        raise StrategyValidationError(f"unsupported market: {market_value}")
    if not lower <= target <= upper:
        raise StrategyValidationError(f"target must be within {lower}..{upper} for {market_value}")
    return target


_FORBIDDEN_NAMES: Final[frozenset[str]] = frozenset(
    {
        "__import__",
        "__builtins__",
        "eval",
        "exec",
        "compile",
        "open",
        "input",
        "globals",
        "locals",
        "vars",
        "getattr",
        "setattr",
        "delattr",
        "breakpoint",
        "help",
        "dir",
        "os",
        "sys",
        "socket",
        "subprocess",
        "requests",
        "urllib",
        "http",
        "psycopg",
        "random",
        "secrets",
        "time",
        "datetime",
        "credential",
        "credentials",
        "password",
        "token",
        "api_key",
        "apikey",
        "dsn",
    }
)
def _future_import(name: str, *_args: object, **_kwargs: object) -> object:
    if name != "__future__":
        raise ImportError(name)
    return __future__


_SAFE_BUILTINS: Final[dict[str, Any]] = {
    "__import__": _future_import,
    "abs": abs,
    "all": all,
    "any": any,
    "enumerate": enumerate,
    "len": len,
    "max": max,
    "min": min,
    "range": range,
    "round": round,
    "sorted": sorted,
    "sum": sum,
    "zip": zip,
}


def _validate_source(source: str) -> ast.Module:
    try:
        tree = ast.parse(source, mode="exec")
    except SyntaxError as exc:
        raise StrategyValidationError("strategy source is not valid Python") from exc
    for node in ast.walk(tree):
        if isinstance(node, (ast.Set, ast.SetComp)):
            raise ForbiddenStrategyError("strategy sets are forbidden")
        if isinstance(node, (ast.Global, ast.Nonlocal)):
            raise ForbiddenStrategyError("strategy global state is forbidden")
        if isinstance(node, ast.Attribute) and isinstance(node.ctx, ast.Store):
            raise ForbiddenStrategyError("strategy attribute mutation is forbidden")
        if isinstance(node, (ast.Import, ast.ImportFrom)) and not _future_annotations(node):
            raise ForbiddenStrategyError("strategy imports are forbidden")
        if isinstance(node, ast.Name) and (
            node.id.lower() in _FORBIDDEN_NAMES or node.id.startswith("__")
        ):
            raise ForbiddenStrategyError(f"forbidden strategy name: {node.id}")
        if isinstance(node, ast.Attribute) and (
            node.attr.lower() in _FORBIDDEN_NAMES or node.attr.startswith("__")
        ):
            raise ForbiddenStrategyError(f"forbidden strategy attribute: {node.attr}")
    for node in tree.body:
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            if isinstance(node, ast.AsyncFunctionDef):
                raise StrategyValidationError("strategy functions must be synchronous")
            if node.decorator_list or node.args.defaults or any(
                default is not None for default in node.args.kw_defaults
            ):
                raise ForbiddenStrategyError(
                    "strategy decorators and function defaults are forbidden"
                )
        elif (
            isinstance(node, ast.Expr)
            and isinstance(node.value, ast.Constant)
            and isinstance(node.value.value, str)
        ) or _future_annotations(node):
            continue
        elif isinstance(node, ast.Assign):
            if not all(isinstance(target, ast.Name) for target in node.targets):
                raise ForbiddenStrategyError("strategy constants must use simple names")
            try:
                value = ast.literal_eval(node.value)
            except (ValueError, TypeError) as exc:
                raise ForbiddenStrategyError(
                    "strategy constants must be immutable literals"
                ) from exc
            if not _immutable_constant(value):
                raise ForbiddenStrategyError("strategy constants must be immutable literals")
        else:
            raise StrategyValidationError(
                "source may only contain functions and immutable constants"
            )
    functions = [
        node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == "on_bar"
    ]
    if len(functions) != 1:
        raise StrategyValidationError("source must define exactly one on_bar")
    args = functions[0].args
    if (
        args.vararg
        or args.kwarg
        or args.kwonlyargs
        or args.defaults
        or any(default is not None for default in args.kw_defaults)
        or len(args.posonlyargs) + len(args.args) != 2
    ):
        raise StrategyValidationError("on_bar must accept exactly candles and params")
    return tree


def _immutable_constant(value: object) -> bool:
    return isinstance(value, (str, int, bool)) or (
        isinstance(value, tuple) and all(_immutable_constant(item) for item in value)
    )


def _future_annotations(node: ast.AST) -> bool:
    return (
        isinstance(node, ast.ImportFrom)
        and node.module == "__future__"
        and len(node.names) == 1
        and node.names[0].name == "annotations"
        and node.names[0].asname is None
    )


@dataclass(frozen=True, slots=True)
class ParameterSpec:
    type: str
    required: bool = True
    default: JSONScalar | None = None
    minimum: Decimal | int | None = None
    maximum: Decimal | int | None = None
    enum: tuple[JSONScalar, ...] | None = None

    def __post_init__(self) -> None:
        if self.type not in {"integer", "decimal", "boolean", "string"}:
            raise StrategyValidationError(f"unsupported parameter type: {self.type}")
        if not isinstance(self.required, bool):
            raise StrategyValidationError("parameter required must be boolean")
        if self.default is not None:
            _validate_parameter(self.type, self.default, self)
        for name in ("minimum", "maximum"):
            bound = getattr(self, name)
            if bound is not None and (
                isinstance(bound, bool)
                or not isinstance(bound, (int, Decimal))
                or (isinstance(bound, Decimal) and not bound.is_finite())
            ):
                raise StrategyValidationError(f"parameter {name} must be a finite number")
        if self.minimum is not None and self.maximum is not None and self.minimum > self.maximum:
            raise StrategyValidationError("parameter minimum must not exceed maximum")
        if self.type not in {"integer", "decimal"} and (
            self.minimum is not None or self.maximum is not None
        ):
            raise StrategyValidationError("only numeric parameters support minimum and maximum")
        if self.enum is not None:
            if not self.enum:
                raise StrategyValidationError("parameter enum must not be empty")
            for value in self.enum:
                _validate_parameter(self.type, value, self)


def _validate_parameter(name: str, value: object, spec: ParameterSpec) -> JSONScalar:
    expected: tuple[type[object], ...]
    if spec.type == "integer":
        expected = (int,)
    elif spec.type == "decimal":
        expected = (Decimal,)
    elif spec.type == "boolean":
        expected = (bool,)
    elif spec.type == "string":
        expected = (str,)
    else:
        raise StrategyValidationError(f"unsupported parameter type: {spec.type}")
    if isinstance(value, bool) and spec.type == "integer":
        raise StrategyValidationError(f"parameter {name} has invalid type")
    if not isinstance(value, expected):
        raise StrategyValidationError(f"parameter {name} has invalid type")
    scalar = value
    if isinstance(value, Decimal) and not value.is_finite():
        raise StrategyValidationError(f"parameter {name} must be finite")
    if isinstance(value, (int, Decimal)):
        if spec.minimum is not None and value < spec.minimum:
            raise StrategyValidationError(f"parameter {name} is below minimum")
        if spec.maximum is not None and value > spec.maximum:
            raise StrategyValidationError(f"parameter {name} is above maximum")
    if spec.enum is not None and scalar not in spec.enum:
        raise StrategyValidationError(f"parameter {name} is not allowed")
    return scalar


def validate_params(
    params: Mapping[str, JSONScalar], schema: Mapping[str, ParameterSpec] | None = None
) -> dict[str, JSONScalar]:
    if not isinstance(params, Mapping):
        raise StrategyValidationError("params must be a mapping")
    result: dict[str, JSONScalar] = {}
    for name, value in params.items():
        if not isinstance(name, str) or not name:
            raise StrategyValidationError("parameter names must be non-empty strings")
        result[name] = _json_scalar(value, f"parameter {name}")
    if schema is None:
        return dict(sorted(result.items()))
    unknown = set(result) - set(schema)
    if unknown:
        raise StrategyValidationError(f"unknown parameters: {sorted(unknown)!r}")
    for name, spec in schema.items():
        if name not in result:
            if spec.required and spec.default is None:
                raise StrategyValidationError(f"missing parameter: {name}")
            if spec.default is not None:
                result[name] = spec.default
        if name in result:
            result[name] = _validate_parameter(name, result[name], spec)
    return dict(sorted(result.items()))


@dataclass(frozen=True, slots=True)
class StrategySpec:
    market: Market | str
    symbol: str = ""
    interval: str = "1m"
    lookback_bars: int = 1
    parameter_schema: Mapping[str, ParameterSpec] | None = None
    runtime_version: str = "python3.12"

    def __post_init__(self) -> None:
        if str(self.market) not in {Market.SPOT.value, Market.USD_M.value}:
            raise StrategyValidationError("unsupported strategy market")
        if (
            isinstance(self.lookback_bars, bool)
            or not isinstance(self.lookback_bars, int)
            or self.lookback_bars < 1
        ):
            raise StrategyValidationError("lookback_bars must be positive")
        if (
            not isinstance(self.symbol, str)
            or not self.symbol
            or any(char.isspace() for char in self.symbol)
        ):
            raise StrategyValidationError("symbol must be a non-whitespace string")
        if not isinstance(self.interval, str) or not self.interval or any(
            char.isspace() for char in self.interval
        ):
            raise StrategyValidationError("interval must be a non-whitespace string")
        if self.runtime_version != "python3.12":
            raise StrategyValidationError("runtime_version must be python3.12")
        if self.parameter_schema is not None:
            schema = dict(self.parameter_schema)
            if any(
                not isinstance(name, str)
                or not name
                or not isinstance(spec, ParameterSpec)
                for name, spec in schema.items()
            ):
                raise StrategyValidationError("parameter_schema is invalid")
            object.__setattr__(self, "parameter_schema", MappingProxyType(schema))


@dataclass(frozen=True, slots=True)
class LoadedStrategy:
    source_sha256: str
    on_bar: StrategyFunction
    spec: StrategySpec
    source: str = ""

    def signal(
        self,
        candles: Sequence[Candle],
        params: Mapping[str, JSONScalar],
        *,
        timeout_seconds: float | None = None,
    ) -> Decimal:
        return call_on_bar(self, candles, params, timeout_seconds=timeout_seconds)


def load_strategy(
    source: str | Path,
    *,
    market: Market | str,
    symbol: str = "",
    interval: str = "1m",
    lookback_bars: int = 1,
    parameter_schema: Mapping[str, ParameterSpec] | None = None,
    runtime_version: str = "python3.12",
) -> LoadedStrategy:
    """Compile one source file in a closed builtins environment."""

    source_text = Path(source).read_text(encoding="utf-8") if isinstance(source, Path) else source
    if not isinstance(source_text, str) or not source_text.strip():
        raise StrategyValidationError("strategy source must not be empty")
    tree = _validate_source(source_text)
    namespace: dict[str, Any] = {
        "__builtins__": _SAFE_BUILTINS,
        "Decimal": Decimal,
        "Candle": Candle,
    }
    try:
        exec(compile(tree, "<strategy>", "exec"), namespace, namespace)
    except Exception as exc:
        raise StrategyValidationError("strategy compilation failed") from exc
    function = namespace.get("on_bar")
    if not callable(function):
        raise StrategyValidationError("source must define callable on_bar")
    spec = StrategySpec(
        market=market,
        symbol=symbol,
        interval=interval,
        lookback_bars=lookback_bars,
        parameter_schema=parameter_schema,
        runtime_version=runtime_version,
    )
    return LoadedStrategy(
        source_sha256=hashlib.sha256(source_text.encode("utf-8")).hexdigest(),
        on_bar=cast(StrategyFunction, function),
        spec=spec,
        source=source_text,
    )


def load_strategy_file(path: str | Path, **kwargs: Any) -> LoadedStrategy:
    return load_strategy(Path(path), **kwargs)


def _call(
    function: StrategyFunction, candles: tuple[Candle, ...], params: Mapping[str, JSONScalar]
) -> object:
    return function(candles, params)


def call_on_bar(
    strategy: LoadedStrategy | StrategyFunction,
    candles: Sequence[Candle],
    params: Mapping[str, JSONScalar],
    *,
    market: Market | str | None = None,
    lookback_bars: int | None = None,
    timeout_seconds: float | None = None,
) -> Decimal:
    target_lookback: int | None
    schema: Mapping[str, ParameterSpec] | None
    if isinstance(strategy, LoadedStrategy):
        target_market = strategy.spec.market if market is None else market
        target_lookback = strategy.spec.lookback_bars if lookback_bars is None else lookback_bars
        schema = strategy.spec.parameter_schema
        function = strategy.on_bar
    else:
        if market is None:
            raise StrategyValidationError("market is required for a bare strategy function")
        target_market = market
        target_lookback = lookback_bars
        schema = None
        function = strategy
    history = validate_candles(candles, target_lookback)
    if isinstance(strategy, LoadedStrategy):
        if strategy.spec.symbol and any(
            item.instrument_id != strategy.spec.symbol for item in history
        ):
            raise StrategyValidationError("candle instrument does not match strategy symbol")
        if any(item.interval != strategy.spec.interval for item in history):
            raise StrategyValidationError("candle interval does not match strategy interval")
    checked_params = validate_params(params, schema)
    readonly_params: Mapping[str, JSONScalar] = MappingProxyType(checked_params)
    if timeout_seconds is not None and (
        isinstance(timeout_seconds, bool)
        or not isinstance(timeout_seconds, (int, float))
        or not math.isfinite(timeout_seconds)
        or timeout_seconds <= 0
    ):
        raise StrategyValidationError("timeout_seconds must be a positive finite number")
    result: list[object] = []
    error: list[BaseException] = []

    def invoke() -> None:
        try:
            result.append(_call(function, history, readonly_params))
        except BaseException as exc:  # re-raise as a stable domain error below
            error.append(exc)

    if timeout_seconds is None:
        invoke()
    else:
        thread = threading.Thread(target=invoke, daemon=True)
        thread.start()
        thread.join(timeout_seconds)
        if thread.is_alive():
            raise StrategyTimeoutError("on_bar timed out")
    if error:
        raise StrategyExecutionError("on_bar failed") from error[0]
    if not result:
        raise StrategyExecutionError("on_bar returned no result")
    try:
        return validate_target(result[0], target_market)
    except StrategyValidationError as exc:
        raise StrategyExecutionError(str(exc)) from exc


__all__ = [
    "Candle",
    "ForbiddenStrategyError",
    "JSONScalar",
    "LoadedStrategy",
    "Market",
    "ParameterSpec",
    "StrategyExecutionError",
    "StrategyFunction",
    "StrategySpec",
    "StrategyTimeoutError",
    "StrategyValidationError",
    "call_on_bar",
    "load_strategy",
    "load_strategy_file",
    "validate_candle",
    "validate_candles",
    "validate_params",
    "validate_target",
]
