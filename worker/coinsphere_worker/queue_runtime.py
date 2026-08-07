"""PostgreSQL Worker 任务队列的租约运行时。

本模块只实现 A1-2 的任务基础设施协议：单事务认领、租约 fencing、心跳、
过期回收和取消。它刻意不提供业务任务注册框架；当前两个 ``contract.*``
伪任务仅用于在没有数据集、回测或交易能力时验证并发与取消语义。
"""

from __future__ import annotations

import json
import logging
import os
import uuid
from dataclasses import dataclass
from decimal import Decimal
from pathlib import Path
from queue import Empty, Queue
from threading import Event, Thread
from typing import Any, Final, cast

import psycopg
from psycopg import Connection
from psycopg.rows import TupleRow

from .artifacts import freeze_records
from .backtest import (
    SIMULATOR_VERSION,
    BacktestConfig,
    BacktestError,
    BacktestProcessLimits,
    run_backtest_isolated,
)
from .lanes import WorkerLane
from .strategy import Candle, ParameterSpec, StrategyValidationError, load_strategy

LOGGER = logging.getLogger("coinsphere.worker")

DEFAULT_LEASE_SECONDS: Final = 15
DEFAULT_HEARTBEAT_SECONDS: Final = 1.0
DEFAULT_POLL_SECONDS: Final = 1.0
MAX_CONTRACT_SLEEP_SECONDS: Final = 300


class InvalidTaskError(ValueError):
    """表示伪任务类型或载荷不符合当前 A1 契约。"""


class LeaseLostError(RuntimeError):
    """Roll back a domain result when the task lease cannot commit with it."""


@dataclass(frozen=True, slots=True)
class TaskLease:
    """一次任务尝试的不可变租约快照。

    ``lease_id`` 是所有后续状态写入的 fencing token。即使同一任务被再次认领，
    旧 ``TaskLease`` 也不能心跳或提交终态。
    """

    task_id: str
    task_type: str
    payload_json: str
    attempt_count: int
    max_attempts: int
    lease_id: str
    worker_id: str
    lane: str = WorkerLane.REALTIME.value
    priority: int = 0


@dataclass(frozen=True, slots=True)
class Recovery:
    """描述一次过期租约恢复结果，供运行时输出脱敏状态日志。"""

    task_id: str
    lease_id: str | None
    status: str
    category: str


class PostgresTaskStore:
    """直接封装 ``worker_tasks`` 的 PostgreSQL 状态转换。

    每个公开方法都拥有一个短事务。认领使用 ``FOR UPDATE SKIP LOCKED``，
    其余活跃任务写入同时校验任务 ID、租约 ID、合法前态和数据库时间，确保
    租约一旦过期，旧 Worker 即使尚未被其他 Worker 接管也会 fail-closed。
    """

    def __init__(
        self,
        connection: Connection[TupleRow],
        worker_id: str,
        lease_seconds: int = DEFAULT_LEASE_SECONDS,
        lane: WorkerLane | str = WorkerLane.REALTIME,
    ) -> None:
        if not worker_id or len(worker_id) > 120:
            raise ValueError("worker_id 必须为 1 到 120 个字符")
        if lease_seconds < 2:
            raise ValueError("lease_seconds 必须至少为 2")
        try:
            self._lane = WorkerLane(lane).value
        except ValueError as exc:
            raise ValueError("lane 必须为 realtime 或 backtest") from exc
        self._connection = connection
        self._worker_id = worker_id
        self._lease_seconds = lease_seconds

    def claim(self) -> TaskLease | None:
        """原子认领队首任务并创建唯一租约；队列为空时返回 ``None``。"""

        lease_id = str(uuid.uuid4())
        # candidate 行锁与状态更新处于同一事务。SKIP LOCKED 让多个 Worker
        # 各自跳过已锁任务，而不是先读后写造成重复执行。
        with self._connection.transaction():
            row = self._connection.execute(
                """
                WITH candidate AS (
                    SELECT id
                    FROM worker_tasks
                    WHERE status = 'queued'
                      AND lane = %s
                      AND attempt_count < max_attempts
                    ORDER BY priority DESC, queued_at, id
                    FOR UPDATE SKIP LOCKED
                    LIMIT 1
                )
                UPDATE worker_tasks AS task
                SET status = 'claimed',
                    attempt_count = task.attempt_count + 1,
                    lease_id = %s,
                    worker_id = %s,
                    lease_expires_at = CURRENT_TIMESTAMP + (%s * INTERVAL '1 second'),
                    last_heartbeat_at = CURRENT_TIMESTAMP,
                    claimed_at = CURRENT_TIMESTAMP,
                    started_at = NULL,
                    finished_at = NULL,
                    result_json = NULL,
                    failure_category = NULL,
                    error_message = NULL,
                    updated_at = CURRENT_TIMESTAMP
                FROM candidate
                WHERE task.id = candidate.id
                RETURNING task.id, task.task_type, task.payload_json,
                          task.attempt_count, task.max_attempts, task.lease_id, task.worker_id,
                          task.lane, task.priority
                """,
                (self._lane, lease_id, self._worker_id, self._lease_seconds),
            ).fetchone()
        if row is None:
            return None
        return TaskLease(
            task_id=cast(str, row[0]),
            task_type=cast(str, row[1]),
            payload_json=cast(str, row[2]),
            attempt_count=cast(int, row[3]),
            max_attempts=cast(int, row[4]),
            lease_id=cast(str, row[5]),
            worker_id=cast(str, row[6]),
            lane=cast(str, row[7]),
            priority=cast(int, row[8]),
        )

    def start(self, task: TaskLease) -> bool:
        """把当前租约从 ``claimed`` 转为 ``running``。"""

        return self._fenced_update(
            """
            UPDATE worker_tasks
            SET status = 'running',
                started_at = CURRENT_TIMESTAMP,
                updated_at = CURRENT_TIMESTAMP
            WHERE id = %s
              AND lease_id = %s
              AND status = 'claimed'
              AND lease_expires_at > CURRENT_TIMESTAMP
            RETURNING id
            """,
            (task.task_id, task.lease_id),
        )

    def heartbeat(self, task: TaskLease) -> str | None:
        """续期运行中租约并返回数据库中的活跃状态。

        返回 ``None`` 表示租约已过期、被替换或任务已进入其他状态。调用方必须
        立即停止伪任务，且不得再尝试提交成功或失败终态。``cancelRequested``
        只返回给 Owner 确认，不再续期，否则 Owner 在确认取消前崩溃会突破取消时限。
        """

        with self._connection.transaction():
            row = self._connection.execute(
                """
                UPDATE worker_tasks
                SET last_heartbeat_at = CURRENT_TIMESTAMP,
                    lease_expires_at = CURRENT_TIMESTAMP + (%s * INTERVAL '1 second'),
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND lease_id = %s
                  AND status = 'running'
                  AND lease_expires_at > CURRENT_TIMESTAMP
                RETURNING status
                """,
                (self._lease_seconds, task.task_id, task.lease_id),
            ).fetchone()
            if row is None:
                # 正常心跳只需一次 UPDATE；仅在状态竞争、租约失效或收到取消时补查。
                # 该 SELECT 不修改租约时间，恢复器仍能按独立取消截止时间接管。
                row = self._connection.execute(
                    """
                    SELECT status
                    FROM worker_tasks
                    WHERE id = %s
                      AND lease_id = %s
                      AND status = 'cancelRequested'
                      AND lease_expires_at > CURRENT_TIMESTAMP
                    """,
                    (task.task_id, task.lease_id),
                ).fetchone()
        return None if row is None else cast(str, row[0])

    def succeed(self, task: TaskLease) -> bool:
        """仅允许仍有效的 ``running`` 租约提交成功终态。"""

        return self._fenced_update(
            """
            UPDATE worker_tasks
            SET status = 'succeeded',
                finished_at = CURRENT_TIMESTAMP,
                result_json = '{"status":"completed"}',
                failure_category = NULL,
                error_message = NULL,
                lease_id = NULL,
                worker_id = NULL,
                lease_expires_at = NULL,
                last_heartbeat_at = NULL,
                updated_at = CURRENT_TIMESTAMP
            WHERE id = %s
              AND lease_id = %s
              AND status = 'running'
              AND lease_expires_at > CURRENT_TIMESTAMP
            RETURNING id
            """,
            (task.task_id, task.lease_id),
        )

    def cancel(self, task: TaskLease) -> bool:
        """确认取消并清除活跃租约；保留 ``cancel_requested_at`` 审计时间。"""

        return self._fenced_update(
            """
            UPDATE worker_tasks
            SET status = 'canceled',
                finished_at = CURRENT_TIMESTAMP,
                result_json = NULL,
                failure_category = NULL,
                error_message = NULL,
                lease_id = NULL,
                worker_id = NULL,
                lease_expires_at = NULL,
                last_heartbeat_at = NULL,
                updated_at = CURRENT_TIMESTAMP
            WHERE id = %s
              AND lease_id = %s
              AND status = 'cancelRequested'
              AND lease_expires_at > CURRENT_TIMESTAMP
            RETURNING id
            """,
            (task.task_id, task.lease_id),
        )

    def fail(self, task: TaskLease, category: str, retryable: bool) -> str | None:
        """记录执行失败，并按剩余尝试次数决定重排或终止。

        重排时不保留异常正文，避免数据库变成潜在敏感载荷的旁路日志；达到终态时
        只写固定错误分类和固定中文说明。所有分支都会清除旧租约。
        """

        message = {
            "invalid_task": "任务契约无效或不受支持",
            "task_error": "任务执行发生内部错误",
        }.get(category, "任务执行失败")
        terminal = not retryable
        with self._connection.transaction():
            row = self._connection.execute(
                """
                UPDATE worker_tasks
                SET status = CASE
                        WHEN %s OR attempt_count >= max_attempts THEN 'failed'
                        ELSE 'queued'
                    END,
                    queued_at = CASE
                        WHEN NOT %s AND attempt_count < max_attempts
                            THEN CURRENT_TIMESTAMP
                        ELSE queued_at
                    END,
                    finished_at = CASE
                        WHEN %s OR attempt_count >= max_attempts
                            THEN CURRENT_TIMESTAMP
                        ELSE NULL
                    END,
                    result_json = NULL,
                    failure_category = CASE
                        WHEN %s OR attempt_count >= max_attempts THEN %s
                        ELSE NULL
                    END,
                    error_message = CASE
                        WHEN %s OR attempt_count >= max_attempts THEN %s
                        ELSE NULL
                    END,
                    lease_id = NULL,
                    worker_id = NULL,
                    lease_expires_at = NULL,
                    last_heartbeat_at = NULL,
                    claimed_at = CASE
                        WHEN NOT %s AND attempt_count < max_attempts THEN NULL
                        ELSE claimed_at
                    END,
                    started_at = CASE
                        WHEN NOT %s AND attempt_count < max_attempts THEN NULL
                        ELSE started_at
                    END,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s
                  AND lease_id = %s
                  AND status = 'running'
                  AND lease_expires_at > CURRENT_TIMESTAMP
                RETURNING status
                """,
                (
                    terminal,
                    terminal,
                    terminal,
                    terminal,
                    category,
                    terminal,
                    message,
                    terminal,
                    terminal,
                    task.task_id,
                    task.lease_id,
                ),
            ).fetchone()
            if row is not None and row[0] == "failed" and task.task_type == "strategy.publish":
                self._connection.execute(
                    """
                    UPDATE strategy_versions
                    SET status = 'failed'
                    WHERE worker_task_id = %s AND status = 'pending'
                    """,
                    (task.task_id,),
                )
        return None if row is None else cast(str, row[0])

    def recover_expired(self) -> list[Recovery]:
        """回收过期租约或超时取消，并保证取消任务永不重新入队。

        三类更新位于同一事务，且各自先用 ``FOR UPDATE SKIP LOCKED`` 锁定候选行。
        因此多个 Worker 可并发恢复不同任务；取消使用独立数据库截止时间，旧租约字段
        在状态切换时一次性清除。
        """

        recovered: list[Recovery] = []
        with self._connection.transaction():
            # 默认恢复轮询为 1 秒；4 秒数据库截止时间为调度抖动保留余量，确保即使
            # Owner 已观察取消但在提交 canceled 前崩溃，任务仍能在 5 秒契约内收敛。
            recovered.extend(
                self._recover(
                    """
                    WITH cancelable AS (
                        SELECT id, lease_id
                        FROM worker_tasks
                        WHERE status = 'cancelRequested'
                          AND lane = %s
                          AND (
                              lease_expires_at <= CURRENT_TIMESTAMP
                              OR cancel_requested_at <= CURRENT_TIMESTAMP - INTERVAL '4 seconds'
                          )
                        FOR UPDATE SKIP LOCKED
                    )
                    UPDATE worker_tasks AS task
                    SET status = 'canceled',
                        finished_at = CURRENT_TIMESTAMP,
                        result_json = NULL,
                        failure_category = NULL,
                        error_message = NULL,
                        lease_id = NULL,
                        worker_id = NULL,
                        lease_expires_at = NULL,
                        last_heartbeat_at = NULL,
                        updated_at = CURRENT_TIMESTAMP
                    FROM cancelable
                    WHERE task.id = cancelable.id
                    RETURNING task.id, cancelable.lease_id
                    """,
                    status="canceled",
                    category="cancel_recovered",
                )
            )
            recovered.extend(
                self._recover(
                    """
                    WITH expired AS (
                        SELECT id, lease_id
                        FROM worker_tasks
                        WHERE status IN ('claimed', 'running')
                          AND lane = %s
                          AND lease_expires_at <= CURRENT_TIMESTAMP
                          AND attempt_count < max_attempts
                        FOR UPDATE SKIP LOCKED
                    )
                    UPDATE worker_tasks AS task
                    SET status = 'queued',
                        queued_at = CURRENT_TIMESTAMP,
                        claimed_at = NULL,
                        started_at = NULL,
                        lease_id = NULL,
                        worker_id = NULL,
                        lease_expires_at = NULL,
                        last_heartbeat_at = NULL,
                        failure_category = NULL,
                        error_message = NULL,
                        updated_at = CURRENT_TIMESTAMP
                    FROM expired
                    WHERE task.id = expired.id
                    RETURNING task.id, expired.lease_id
                    """,
                    status="queued",
                    category="lease_expired_retry",
                )
            )
            recovered.extend(
                self._recover(
                    """
                    WITH exhausted AS (
                        SELECT id, lease_id
                        FROM worker_tasks
                        WHERE lane = %s
                          AND (
                                (
                                    status IN ('claimed', 'running')
                                    AND lease_expires_at <= CURRENT_TIMESTAMP
                                    AND attempt_count >= max_attempts
                                )
                                OR (status = 'queued' AND attempt_count >= max_attempts)
                            )
                        FOR UPDATE SKIP LOCKED
                    )
                    UPDATE worker_tasks AS task
                    SET status = 'failed',
                        finished_at = CURRENT_TIMESTAMP,
                        result_json = NULL,
                        lease_id = NULL,
                        worker_id = NULL,
                        lease_expires_at = NULL,
                        last_heartbeat_at = NULL,
                        failure_category = 'attempts_exhausted',
                        error_message = '任务已耗尽最大尝试次数',
                        updated_at = CURRENT_TIMESTAMP
                    FROM exhausted
                    WHERE task.id = exhausted.id
                    RETURNING task.id, exhausted.lease_id
                    """,
                    status="failed",
                    category="attempts_exhausted",
                )
            )
            self._connection.execute(
                """
                UPDATE strategy_versions AS version
                SET status = 'failed'
                FROM worker_tasks AS task
                WHERE version.worker_task_id = task.id
                  AND version.status = 'pending'
                  AND task.task_type = 'strategy.publish'
                  AND task.status = 'failed'
                """
            )
        return recovered

    def _fenced_update(self, statement: str, parameters: tuple[str, str]) -> bool:
        with self._connection.transaction():
            return self._connection.execute(statement, parameters).fetchone() is not None

    def _recover(self, statement: str, *, status: str, category: str) -> list[Recovery]:
        rows = self._connection.execute(statement, (self._lane,)).fetchall()
        return [
            Recovery(
                task_id=cast(str, row[0]),
                lease_id=cast(str | None, row[1]),
                status=status,
                category=category,
            )
            for row in rows
        ]


class WorkerRuntime:
    """串行消费 PostgreSQL 队列并维护当前任务租约。

    单进程只执行一个任务，足以验证 A1 并发协议，也避免提前引入任务池。多个容器
    仍可依赖数据库行锁横向扩展。正常停止时不篡改在途任务状态，而是停止心跳，
    让与崩溃相同的租约过期路径完成唯一、可 fencing 的恢复。
    """

    def __init__(
        self,
        dsn: str,
        worker_id: str,
        *,
        lease_seconds: int = DEFAULT_LEASE_SECONDS,
        heartbeat_seconds: float = DEFAULT_HEARTBEAT_SECONDS,
        poll_seconds: float = DEFAULT_POLL_SECONDS,
        lane: WorkerLane | str = WorkerLane.REALTIME,
    ) -> None:
        if not dsn.strip():
            raise ValueError("数据库 DSN 不能为空")
        if heartbeat_seconds <= 0 or heartbeat_seconds >= lease_seconds:
            raise ValueError("heartbeat_seconds 必须大于 0 且小于租约时长")
        if poll_seconds <= 0:
            raise ValueError("poll_seconds 必须大于 0")
        try:
            self._lane = WorkerLane(lane).value
        except ValueError as exc:
            raise ValueError("lane 必须为 realtime 或 backtest") from exc
        self._dsn = dsn
        self._worker_id = worker_id
        self._lease_seconds = lease_seconds
        self._heartbeat_seconds = heartbeat_seconds
        self._poll_seconds = poll_seconds
        self._artifact_dir: Path | None = None
        self._backtest_limits: BacktestProcessLimits | None = None
        if self._lane == WorkerLane.BACKTEST.value:
            try:
                artifact_dir = Path(os.environ["COINSPHERE_WORKER_ARTIFACT_DIR"])
                limits = BacktestProcessLimits(
                    wall_seconds=float(os.environ["COINSPHERE_WORKER_BACKTEST_WALL_SECONDS"]),
                    cpu_seconds=int(os.environ["COINSPHERE_WORKER_BACKTEST_CPU_SECONDS"]),
                    memory_bytes=int(os.environ["COINSPHERE_WORKER_BACKTEST_MEMORY_BYTES"]),
                    artifact_bytes=int(os.environ["COINSPHERE_WORKER_BACKTEST_ARTIFACT_BYTES"]),
                )
            except (KeyError, TypeError, ValueError) as exc:
                raise ValueError(
                    "backtest lane resource limits must be explicitly configured"
                ) from exc
            if not artifact_dir.is_absolute():
                raise ValueError("COINSPHERE_WORKER_ARTIFACT_DIR must be an absolute path")
            artifact_dir.mkdir(parents=True, exist_ok=True)
            self._artifact_dir = artifact_dir
            self._backtest_limits = limits

    def run(self, stop_event: Event) -> None:
        """运行消费循环，数据库边界异常时直接退出并等待租约恢复。"""

        LOGGER.info(
            "event=worker.start worker_id=%s mode=a1-postgres lane=%s", self._worker_id, self._lane
        )
        try:
            with psycopg.connect(self._dsn) as connection:
                store = PostgresTaskStore(
                    connection, self._worker_id, self._lease_seconds, self._lane
                )
                while not stop_event.is_set():
                    self._log_recoveries(store.recover_expired())
                    task = store.claim()
                    if task is None:
                        stop_event.wait(self._poll_seconds)
                        continue
                    LOGGER.info(
                        "event=task.claimed task_id=%s worker_id=%s lease_id=%s "
                        "attempt=%d max_attempts=%d",
                        task.task_id,
                        task.worker_id,
                        task.lease_id,
                        task.attempt_count,
                        task.max_attempts,
                    )
                    self._run_task(store, task, stop_event)
        except psycopg.Error as exc:
            # psycopg 的错误正文可能包含主机、用户或数据库名，日志只记录异常类型。
            LOGGER.error(
                "event=worker.database-error worker_id=%s error_type=%s",
                self._worker_id,
                type(exc).__name__,
            )
            raise
        except Exception as exc:
            LOGGER.error(
                "event=worker.runtime-error worker_id=%s error_type=%s",
                self._worker_id,
                type(exc).__name__,
            )
            raise
        finally:
            LOGGER.info("event=worker.stop worker_id=%s", self._worker_id)

    def _run_task(self, store: PostgresTaskStore, task: TaskLease, stop_event: Event) -> None:
        if not store.start(task):
            self._resolve_competing_transition(store, task)
            return
        LOGGER.info(
            "event=task.running task_id=%s worker_id=%s lease_id=%s status=running",
            task.task_id,
            task.worker_id,
            task.lease_id,
        )

        try:
            result_queue: Queue[tuple[str, object | None]] = Queue(maxsize=1)
            cancel_event = Event()
            Thread(
                target=self._execute_task,
                args=(task, cancel_event, result_queue),
                daemon=True,
            ).start()
            while True:
                if stop_event.is_set():
                    cancel_event.set()
                    return
                try:
                    category, result = result_queue.get(timeout=self._heartbeat_seconds)
                    break
                except Empty:
                    category, result = "", None
                state = store.heartbeat(task)
                if state is None:
                    cancel_event.set()
                    self._log_lease_lost(task)
                    return
                if state == "cancelRequested":
                    cancel_event.set()
                    self._finish_cancel(store, task)
                    return
                LOGGER.debug(
                    "event=task.heartbeat task_id=%s worker_id=%s lease_id=%s",
                    task.task_id,
                    task.worker_id,
                    task.lease_id,
                )
            if category == "invalid_task":
                raise InvalidTaskError
            if category == "task_error":
                raise RuntimeError
            completed = (
                store.succeed(task)
                if task.task_type.startswith("contract.")
                else self._complete_domain_task(task, cast(dict[str, object] | None, result))
            )
            if completed:
                LOGGER.info(
                    "event=task.succeeded task_id=%s worker_id=%s lease_id=%s status=succeeded",
                    task.task_id,
                    task.worker_id,
                    task.lease_id,
                )
            else:
                self._resolve_competing_transition(store, task)
        except InvalidTaskError:
            self._finish_failure(store, task, category="invalid_task", retryable=False)
        except psycopg.Error:
            raise
        except Exception:
            # 未知任务异常可能携带 payload 或第三方响应，不输出异常正文或 traceback。
            self._finish_failure(store, task, category="task_error", retryable=True)

    def _execute_task(
        self, task: TaskLease, cancel_event: Event, output: Queue[tuple[str, object | None]]
    ) -> None:
        try:
            if task.task_type == "strategy.publish":
                if task.lane != WorkerLane.BACKTEST.value:
                    raise InvalidTaskError
                output.put(("ok", self._publish(task)))
            elif task.task_type == "strategy.backtest":
                if task.lane != WorkerLane.BACKTEST.value:
                    raise InvalidTaskError
                output.put(("ok", self._backtest(task, cancel_event)))
            elif task.task_type in {"contract.noop", "contract.sleep"}:
                duration = self._contract_duration(task)
                cancel_event.wait(duration)
                output.put(("ok", {"duration": duration}))
            else:
                raise InvalidTaskError
        except (InvalidTaskError, BacktestError, StrategyValidationError):
            output.put(("invalid_task", None))
        except Exception:
            output.put(("task_error", None))

    @staticmethod
    def _ids(task: TaskLease, names: tuple[str, ...]) -> dict[str, str]:
        try:
            payload = json.loads(task.payload_json)
        except (TypeError, json.JSONDecodeError) as exc:
            raise InvalidTaskError from exc
        if not isinstance(payload, dict) or set(payload) != set(names):
            raise InvalidTaskError
        values: dict[str, str] = {}
        for name in names:
            value = payload.get(name)
            if not isinstance(value, str):
                raise InvalidTaskError
            try:
                parsed = uuid.UUID(value)
            except (ValueError, AttributeError) as exc:
                raise InvalidTaskError from exc
            if parsed.version != 7:
                raise InvalidTaskError
            values[name] = str(parsed)
        return values

    @staticmethod
    def _schema(value: object) -> dict[str, ParameterSpec] | None:
        if value in (None, {}):
            return None
        if not isinstance(value, dict):
            raise InvalidTaskError
        result: dict[str, ParameterSpec] = {}
        for name, spec in value.items():
            if not isinstance(name, str) or not isinstance(spec, dict):
                raise InvalidTaskError
            try:
                kind = cast(str, spec["type"])
                default = spec.get("default")
                minimum = spec.get("minimum")
                maximum = spec.get("maximum")
                enum = spec.get("enum")
                if kind in {"integer", "decimal"}:
                    convert = int if kind == "integer" else Decimal
                    default = None if default is None else convert(str(default))
                    minimum = None if minimum is None else convert(str(minimum))
                    maximum = None if maximum is None else convert(str(maximum))
                    enum = None if enum is None else tuple(convert(str(item)) for item in enum)
                elif enum is not None:
                    enum = tuple(enum)
                result[name] = ParameterSpec(
                    type=kind,
                    required=cast(bool, spec.get("required", True)),
                    default=cast(Any, default),
                    minimum=cast(Any, minimum),
                    maximum=cast(Any, maximum),
                    enum=cast(Any, enum),
                )
            except (KeyError, TypeError, ValueError) as exc:
                raise InvalidTaskError from exc
        return result

    def _publish(self, task: TaskLease) -> dict[str, object]:
        ids = self._ids(task, ("strategyId", "strategyVersionId"))
        with psycopg.connect(self._dsn) as connection:
            row = connection.execute(
                """
                SELECT source_code, market_type, symbol, interval_code, lookback_bars,
                       parameter_schema_json, runtime_version, code_sha256
                FROM strategy_versions
                WHERE id = %s AND strategy_id = %s
                  AND worker_task_id = %s AND status = 'pending'
                """,
                (ids["strategyVersionId"], ids["strategyId"], task.task_id),
            ).fetchone()
        if row is None:
            raise InvalidTaskError
        source, market, symbol, interval, lookback, schema, runtime, code_sha = row
        loaded = load_strategy(
            cast(str, source),
            market=cast(str, market),
            symbol=cast(str, symbol),
            interval=cast(str, interval),
            lookback_bars=cast(int, lookback),
            parameter_schema=self._schema(schema),
            runtime_version=cast(str, runtime),
        )
        if loaded.source_sha256 != str(code_sha):
            raise InvalidTaskError
        return {"strategyVersionId": ids["strategyVersionId"]}

    def _backtest(self, task: TaskLease, cancel_event: Event) -> dict[str, object]:
        ids = self._ids(task, ("backtestId",))
        with psycopg.connect(self._dsn) as connection:
            row = connection.execute(
                """
                SELECT b.strategy_version_id, b.parameters_json, b.start_time, b.end_time,
                       b.allocation_usdt, b.initial_equity, b.fee_rate, b.slippage_rate,
                       b.funding_rates_json, b.stop_loss_ratio, b.maintenance_margin_ratio,
                       b.simulator_version,
                       v.source_code, v.market_type, v.symbol, v.interval_code,
                       v.lookback_bars, v.parameter_schema_json, v.runtime_version,
                       v.code_sha256, i.id
                FROM backtests b
                JOIN strategy_versions v ON v.id = b.strategy_version_id
                JOIN market_instruments i ON i.id = v.instrument_id
                WHERE b.id = %s AND b.worker_task_id = %s AND v.status = 'published'
                """,
                (ids["backtestId"], task.task_id),
            ).fetchone()
            if row is None:
                raise InvalidTaskError
            (
                version_id,
                parameters,
                start,
                end,
                allocation,
                initial,
                fee,
                slippage,
                funding,
                stop_loss,
                margin,
                simulator_version,
                source,
                market,
                symbol,
                interval,
                lookback,
                schema,
                runtime,
                code_sha,
                instrument_id,
            ) = row
            candles_rows = connection.execute(
                """
                SELECT open_time, close_time, open_price, high_price, low_price, close_price,
                       base_volume, is_closed
                FROM market_candles
                WHERE venue = 'binance' AND instrument_id = %s AND interval_code = %s
                  AND open_time >= %s AND close_time <= %s
                ORDER BY open_time
                """,
                (instrument_id, interval, start, end),
            ).fetchall()
        if (
            not candles_rows
            or simulator_version != SIMULATOR_VERSION
            or self._artifact_dir is None
            or self._backtest_limits is None
        ):
            raise InvalidTaskError
        candles = tuple(
            Candle(
                instrument_id=str(symbol),
                interval=str(interval),
                open_time=r[0],
                close_time=r[1],
                open=r[2],
                high=r[3],
                low=r[4],
                close=r[5],
                base_volume=r[6],
                is_closed=r[7],
            )
            for r in candles_rows
        )
        step = candles[0].close_time - candles[0].open_time
        if (
            candles[0].open_time != start
            or candles[-1].close_time != end
            or any(
                not item.is_closed
                or item.instrument_id != str(symbol)
                or item.interval != str(interval)
                or item.close_time - item.open_time != step
                for item in candles
            )
            or any(
                left.close_time != right.open_time
                for left, right in zip(candles, candles[1:], strict=False)
            )
        ):
            raise InvalidTaskError
        loaded = load_strategy(
            cast(str, source),
            market=cast(str, market),
            symbol=cast(str, symbol),
            interval=cast(str, interval),
            lookback_bars=cast(int, lookback),
            parameter_schema=self._schema(schema),
            runtime_version=cast(str, runtime),
        )
        if loaded.source_sha256 != str(code_sha):
            raise InvalidTaskError
        params = cast(dict[str, Any], parameters)
        if not isinstance(params, dict):
            raise InvalidTaskError
        schema_values = self._schema(schema) or {}
        params = {
            key: (
                Decimal(str(value))
                if schema_values.get(key, None) and schema_values[key].type == "decimal"
                else value
            )
            for key, value in params.items()
        }
        funding_values = (
            None
            if str(market) == "spot"
            else tuple(Decimal(str(item)) for item in cast(list[Any], funding))
        )
        if str(market) == "spot" and funding not in (None, []):
            raise InvalidTaskError
        config = BacktestConfig(
            market=cast(str, market),
            allocation_usdt=Decimal(str(allocation)),
            initial_equity=Decimal(str(initial)),
            fee_rate=Decimal(str(fee)),
            slippage_rate=Decimal(str(slippage)),
            funding_rates=funding_values,
            stop_loss_ratio=None if stop_loss is None else Decimal(str(stop_loss)),
            maintenance_margin_ratio=None if margin is None else Decimal(str(margin)),
        )
        result = run_backtest_isolated(
            loaded, candles, params, config, self._backtest_limits, cancel_event=cancel_event
        )
        result_records = result.to_records()
        input_records = [
            {
                "allocationUsdt": config.allocation_usdt,
                "codeSha256": str(code_sha),
                "endTime": end,
                "feeRate": config.fee_rate,
                "fundingRates": funding_values or (),
                "initialEquity": config.initial_equity,
                "instrumentId": str(instrument_id),
                "interval": str(interval),
                "lookbackBars": int(lookback),
                "maintenanceMarginRatio": config.maintenance_margin_ratio,
                "market": str(market),
                "parameterSchema": schema,
                "parameters": params,
                "simulatorVersion": SIMULATOR_VERSION,
                "slippageRate": config.slippage_rate,
                "sourceCode": str(source),
                "startTime": start,
                "stopLossRatio": config.stop_loss_ratio,
                "strategyRuntimeVersion": str(runtime),
                "strategyVersionId": str(version_id),
                "symbol": str(symbol),
                "type": "configuration",
            },
            *(
                {
                    "baseVolume": item.base_volume,
                    "close": item.close,
                    "closeTime": item.close_time,
                    "high": item.high,
                    "instrumentId": str(instrument_id),
                    "interval": item.interval,
                    "isClosed": item.is_closed,
                    "low": item.low,
                    "open": item.open,
                    "openTime": item.open_time,
                    "symbol": item.instrument_id,
                    "type": "candle",
                }
                for item in candles
            ),
        ]
        manifest = freeze_records(
            self._artifact_dir / ids["backtestId"],
            input_records=input_records,
            result_records=result_records,
        )
        return {
            "backtestId": ids["backtestId"],
            "strategyVersionId": str(version_id),
            "manifest": {**manifest.to_record(), "sha256": manifest.sha256},
            "summary": result_records[-1],
        }

    def _complete_domain_task(self, task: TaskLease, result: dict[str, object] | None) -> bool:
        if result is None:
            return False
        try:
            with psycopg.connect(self._dsn) as connection, connection.transaction():
                if task.task_type == "strategy.publish":
                    updated = connection.execute(
                        "UPDATE strategy_versions "
                        "SET status = 'published', published_at = CURRENT_TIMESTAMP "
                        "WHERE id = %s AND status = 'pending' AND worker_task_id = %s "
                        "AND EXISTS (SELECT 1 FROM worker_tasks WHERE id = %s "
                        "AND lease_id = %s AND status = 'running' "
                        "AND lease_expires_at > CURRENT_TIMESTAMP) RETURNING id",
                        (result["strategyVersionId"], task.task_id, task.task_id, task.lease_id),
                    ).fetchone()
                elif task.task_type == "strategy.backtest":
                    manifest = cast(dict[str, Any], result["manifest"])
                    references = cast(dict[str, str], manifest["references"])
                    files = cast(list[dict[str, Any]], manifest["files"])
                    by_path = {
                        cast(str, item["path"]): cast(str, item["sha256"]) for item in files
                    }
                    updated = connection.execute(
                        "UPDATE backtests SET summary_json = %s::jsonb, input_sha256 = %s, "
                        "result_sha256 = %s, manifest_sha256 = %s "
                        "WHERE id = %s AND worker_task_id = %s "
                        "AND EXISTS (SELECT 1 FROM worker_tasks WHERE id = %s "
                        "AND lease_id = %s AND status = 'running' "
                        "AND lease_expires_at > CURRENT_TIMESTAMP) RETURNING id",
                        (
                            json.dumps(result["summary"], separators=(",", ":")),
                            by_path[references["input"]],
                            by_path[references["result"]],
                            str(manifest.get("sha256", "")),
                            result.get("backtestId"),
                            task.task_id,
                            task.task_id,
                            task.lease_id,
                        ),
                    ).fetchone()
                else:
                    return False
                if updated is None:
                    return False
                completed = connection.execute(
                    "UPDATE worker_tasks SET status = 'succeeded', "
                    "finished_at = CURRENT_TIMESTAMP, "
                    "result_json = '{\"status\":\"completed\"}', failure_category = NULL, "
                    "error_message = NULL, lease_id = NULL, worker_id = NULL, "
                    "lease_expires_at = NULL, last_heartbeat_at = NULL, "
                    "updated_at = CURRENT_TIMESTAMP "
                    "WHERE id = %s AND lease_id = %s AND status = 'running' "
                    "AND lease_expires_at > CURRENT_TIMESTAMP RETURNING id",
                    (task.task_id, task.lease_id),
                ).fetchone()
                if completed is None:
                    raise LeaseLostError
                return True
        except LeaseLostError:
            return False

    def _resolve_competing_transition(self, store: PostgresTaskStore, task: TaskLease) -> None:
        """处理成功/启动与外部取消并发的窄窗口。"""

        state = store.heartbeat(task)
        if state == "cancelRequested":
            self._finish_cancel(store, task)
        else:
            self._log_lease_lost(task)

    def _finish_cancel(self, store: PostgresTaskStore, task: TaskLease) -> None:
        if store.cancel(task):
            LOGGER.info(
                "event=task.canceled task_id=%s worker_id=%s lease_id=%s status=canceled",
                task.task_id,
                task.worker_id,
                task.lease_id,
            )
        else:
            self._log_lease_lost(task)

    def _finish_failure(
        self,
        store: PostgresTaskStore,
        task: TaskLease,
        *,
        category: str,
        retryable: bool,
    ) -> None:
        status = store.fail(task, category, retryable)
        if status is None:
            self._resolve_competing_transition(store, task)
            return
        LOGGER.warning(
            "event=task.failed task_id=%s worker_id=%s lease_id=%s status=%s error_category=%s",
            task.task_id,
            task.worker_id,
            task.lease_id,
            status,
            category,
        )

    @staticmethod
    def _contract_duration(task: TaskLease) -> float:
        """校验当前阶段唯一允许的伪任务，并返回其有限执行时长。"""

        try:
            payload = json.loads(task.payload_json)
        except (json.JSONDecodeError, TypeError) as exc:
            raise InvalidTaskError from exc
        if not isinstance(payload, dict):
            raise InvalidTaskError
        if task.task_type == "contract.noop" and not payload:
            return 0.0
        if task.task_type == "contract.sleep" and set(payload) == {"durationSeconds"}:
            duration = payload["durationSeconds"]
            if (
                isinstance(duration, int)
                and not isinstance(duration, bool)
                and 1 <= duration <= MAX_CONTRACT_SLEEP_SECONDS
            ):
                return float(duration)
        raise InvalidTaskError

    @staticmethod
    def _log_recoveries(recoveries: list[Recovery]) -> None:
        for recovery in recoveries:
            LOGGER.warning(
                "event=task.recovered task_id=%s lease_id=%s status=%s error_category=%s",
                recovery.task_id,
                recovery.lease_id or "none",
                recovery.status,
                recovery.category,
            )

    @staticmethod
    def _log_lease_lost(task: TaskLease) -> None:
        LOGGER.warning(
            "event=task.lease-lost task_id=%s worker_id=%s lease_id=%s",
            task.task_id,
            task.worker_id,
            task.lease_id,
        )
