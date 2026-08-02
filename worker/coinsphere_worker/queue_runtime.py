"""PostgreSQL Worker 任务队列的租约运行时。

本模块只实现 A1-2 的任务基础设施协议：单事务认领、租约 fencing、心跳、
过期回收和取消。它刻意不提供业务任务注册框架；当前两个 ``contract.*``
伪任务仅用于在没有数据集、回测或交易能力时验证并发与取消语义。
"""

from __future__ import annotations

import json
import logging
import time
import uuid
from dataclasses import dataclass
from threading import Event
from typing import Final, cast

import psycopg
from psycopg import Connection
from psycopg.rows import TupleRow

LOGGER = logging.getLogger("coinsphere.worker")

DEFAULT_LEASE_SECONDS: Final = 15
DEFAULT_HEARTBEAT_SECONDS: Final = 1.0
DEFAULT_POLL_SECONDS: Final = 1.0
MAX_CONTRACT_SLEEP_SECONDS: Final = 300


class InvalidTaskError(ValueError):
    """表示伪任务类型或载荷不符合当前 A1 契约。"""


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
    ) -> None:
        if not worker_id or len(worker_id) > 120:
            raise ValueError("worker_id 必须为 1 到 120 个字符")
        if lease_seconds < 2:
            raise ValueError("lease_seconds 必须至少为 2")
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
                      AND attempt_count < max_attempts
                    ORDER BY queued_at, id
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
                          task.attempt_count, task.max_attempts, task.lease_id, task.worker_id
                """,
                (lease_id, self._worker_id, self._lease_seconds),
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
        """续期当前租约并返回数据库中的活跃状态。

        返回 ``None`` 表示租约已过期、被替换或任务已进入其他状态。调用方必须
        立即停止伪任务，且不得再尝试提交成功或失败终态。
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
                  AND status IN ('running', 'cancelRequested')
                  AND lease_expires_at > CURRENT_TIMESTAMP
                RETURNING status
                """,
                (self._lease_seconds, task.task_id, task.lease_id),
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
        return None if row is None else cast(str, row[0])

    def recover_expired(self) -> list[Recovery]:
        """回收过期租约，并保证取消任务永不重新入队。

        三类更新位于同一事务，且各自先用 ``FOR UPDATE SKIP LOCKED`` 锁定候选行。
        因此多个 Worker 可并发恢复不同任务；旧租约字段在状态切换时一次性清除。
        """

        recovered: list[Recovery] = []
        with self._connection.transaction():
            recovered.extend(
                self._recover(
                    """
                    WITH expired AS (
                        SELECT id, lease_id
                        FROM worker_tasks
                        WHERE status = 'cancelRequested'
                          AND lease_expires_at <= CURRENT_TIMESTAMP
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
                    FROM expired
                    WHERE task.id = expired.id
                    RETURNING task.id, expired.lease_id
                    """,
                    status="canceled",
                    category="cancel_after_lease_expiry",
                )
            )
            recovered.extend(
                self._recover(
                    """
                    WITH expired AS (
                        SELECT id, lease_id
                        FROM worker_tasks
                        WHERE status IN ('claimed', 'running')
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
                        WHERE (
                                status IN ('claimed', 'running')
                                AND lease_expires_at <= CURRENT_TIMESTAMP
                                AND attempt_count >= max_attempts
                            )
                            OR (status = 'queued' AND attempt_count >= max_attempts)
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
        return recovered

    def _fenced_update(self, statement: str, parameters: tuple[str, str]) -> bool:
        with self._connection.transaction():
            return self._connection.execute(statement, parameters).fetchone() is not None

    def _recover(self, statement: str, *, status: str, category: str) -> list[Recovery]:
        rows = self._connection.execute(statement).fetchall()
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
    ) -> None:
        if not dsn.strip():
            raise ValueError("数据库 DSN 不能为空")
        if heartbeat_seconds <= 0 or heartbeat_seconds >= lease_seconds:
            raise ValueError("heartbeat_seconds 必须大于 0 且小于租约时长")
        if poll_seconds <= 0:
            raise ValueError("poll_seconds 必须大于 0")
        self._dsn = dsn
        self._worker_id = worker_id
        self._lease_seconds = lease_seconds
        self._heartbeat_seconds = heartbeat_seconds
        self._poll_seconds = poll_seconds

    def run(self, stop_event: Event) -> None:
        """运行消费循环，数据库边界异常时直接退出并等待租约恢复。"""

        LOGGER.info("event=worker.start worker_id=%s mode=a1-postgres", self._worker_id)
        try:
            with psycopg.connect(self._dsn) as connection:
                store = PostgresTaskStore(connection, self._worker_id, self._lease_seconds)
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
            duration = self._contract_duration(task)
            deadline = time.monotonic() + duration
            while True:
                if stop_event.is_set():
                    LOGGER.info(
                        "event=task.interrupted task_id=%s worker_id=%s lease_id=%s "
                        "recovery=lease-expiry",
                        task.task_id,
                        task.worker_id,
                        task.lease_id,
                    )
                    return
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    break
                stop_event.wait(min(self._heartbeat_seconds, remaining))
                if stop_event.is_set():
                    continue
                state = store.heartbeat(task)
                if state is None:
                    self._log_lease_lost(task)
                    return
                if state == "cancelRequested":
                    self._finish_cancel(store, task)
                    return
                LOGGER.debug(
                    "event=task.heartbeat task_id=%s worker_id=%s lease_id=%s",
                    task.task_id,
                    task.worker_id,
                    task.lease_id,
                )

            if store.succeed(task):
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
