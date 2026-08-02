"""A1 Worker 租约协议的真实 PostgreSQL 集成测试。"""

from __future__ import annotations

import logging
import os
import threading
import time
import uuid
from collections.abc import Iterator
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from typing import Any, cast

import psycopg
import pytest
from psycopg import sql
from psycopg.conninfo import make_conninfo

from coinsphere_worker.queue_runtime import PostgresTaskStore, TaskLease, WorkerRuntime

POSTGRES_DSN_ENV = "COINSPHERE_TEST_POSTGRES_DSN"


@pytest.fixture(scope="session")
def postgres_dsn() -> Iterator[str]:
    """在调用方提供的测试数据库内创建并最终删除随机隔离 schema。

    测试不会使用固定 schema 或清空外部表，避免误配置 DSN 时碰触现有任务。
    表结构直接取自已合并 migration 的 Up 段，防止测试夹具与生产约束漂移。
    """

    base_dsn = os.getenv(POSTGRES_DSN_ENV, "").strip()
    if not base_dsn:
        if os.getenv("CI"):
            pytest.fail(f"CI 必须设置 {POSTGRES_DSN_ENV} 并执行 PostgreSQL 集成门禁")
        pytest.skip(f"未设置 {POSTGRES_DSN_ENV}，PostgreSQL 集成测试由 CI 强制执行")

    schema = f"worker_runtime_test_{uuid.uuid4().hex}"
    isolated_dsn = make_conninfo(base_dsn, options=f"-c search_path={schema}")
    migration_path = (
        Path(__file__).resolve().parents[2]
        / "backend"
        / "internal"
        / "migration"
        / "sql"
        / "00002_a1_worker_tasks.sql"
    )
    up_sql, separator, _down_sql = migration_path.read_text(encoding="utf-8").partition(
        "-- +goose Down"
    )
    assert separator, "Worker migration 缺少 Down 分隔符"
    up_sql = up_sql.replace("-- +goose Up", "", 1)

    with psycopg.connect(base_dsn, autocommit=True) as admin:
        admin.execute(sql.SQL("CREATE SCHEMA {}").format(sql.Identifier(schema)))
    try:
        with psycopg.connect(isolated_dsn, autocommit=True) as connection:
            connection.execute(up_sql, prepare=False)
        yield isolated_dsn
    finally:
        with psycopg.connect(base_dsn, autocommit=True) as admin:
            admin.execute(sql.SQL("DROP SCHEMA {} CASCADE").format(sql.Identifier(schema)))


@pytest.fixture(autouse=True)
def empty_queue(postgres_dsn: str) -> Iterator[None]:
    """每个用例只清理随机测试 schema 中的队列。"""

    with psycopg.connect(postgres_dsn, autocommit=True) as connection:
        connection.execute("TRUNCATE worker_tasks")
    yield


def insert_task(
    dsn: str,
    task_id: str,
    *,
    task_type: str = "contract.noop",
    payload_json: str = "{}",
    attempt_count: int = 0,
    max_attempts: int = 3,
) -> None:
    with psycopg.connect(dsn, autocommit=True) as connection:
        connection.execute(
            """
            INSERT INTO worker_tasks (
                id, task_type, payload_json, attempt_count, max_attempts
            ) VALUES (%s, %s, %s, %s, %s)
            """,
            (task_id, task_type, payload_json, attempt_count, max_attempts),
        )


def task_row(dsn: str, task_id: str) -> dict[str, Any]:
    with psycopg.connect(dsn) as connection:
        row = connection.execute(
            """
            SELECT status, attempt_count, lease_id, worker_id, lease_expires_at,
                   last_heartbeat_at, finished_at, failure_category, error_message
            FROM worker_tasks
            WHERE id = %s
            """,
            (task_id,),
        ).fetchone()
    assert row is not None
    names = (
        "status",
        "attempt_count",
        "lease_id",
        "worker_id",
        "lease_expires_at",
        "last_heartbeat_at",
        "finished_at",
        "failure_category",
        "error_message",
    )
    return dict(zip(names, row, strict=True))


def wait_for_status(dsn: str, task_id: str, status: str, timeout: float = 5.0) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if task_row(dsn, task_id)["status"] == status:
            return
        time.sleep(0.02)
    pytest.fail(f"任务 {task_id} 未在 {timeout} 秒内进入 {status}")


def claim_with_worker(dsn: str, worker_id: str, barrier: threading.Barrier) -> TaskLease | None:
    with psycopg.connect(dsn) as connection:
        store = PostgresTaskStore(connection, worker_id)
        barrier.wait()
        return store.claim()


def test_concurrent_workers_do_not_claim_the_same_task(postgres_dsn: str) -> None:
    task_id = "018f0000-0000-7000-8000-000000000201"
    insert_task(postgres_dsn, task_id)
    barrier = threading.Barrier(2)

    with ThreadPoolExecutor(max_workers=2) as pool:
        leases = list(
            pool.map(
                lambda worker_id: claim_with_worker(postgres_dsn, worker_id, barrier),
                ("worker-a", "worker-b"),
            )
        )

    claimed = [lease for lease in leases if lease is not None]
    assert len(claimed) == 1
    assert claimed[0].task_id == task_id
    assert task_row(postgres_dsn, task_id)["attempt_count"] == 1


def test_heartbeat_renews_lease_and_expired_lease_is_fenced(postgres_dsn: str) -> None:
    task_id = "018f0000-0000-7000-8000-000000000202"
    insert_task(postgres_dsn, task_id)

    with psycopg.connect(postgres_dsn) as old_connection:
        old_store = PostgresTaskStore(old_connection, "worker-old", lease_seconds=2)
        old_lease = cast(TaskLease, old_store.claim())
        assert old_store.start(old_lease)
        before = task_row(postgres_dsn, task_id)
        time.sleep(0.02)
        assert old_store.heartbeat(old_lease) == "running"
        after = task_row(postgres_dsn, task_id)
        assert after["last_heartbeat_at"] > before["last_heartbeat_at"]
        assert after["lease_expires_at"] > before["lease_expires_at"]

        with psycopg.connect(postgres_dsn, autocommit=True) as admin:
            admin.execute(
                """
                UPDATE worker_tasks
                SET lease_expires_at = CURRENT_TIMESTAMP - INTERVAL '1 second'
                WHERE id = %s
                """,
                (task_id,),
            )
        assert old_store.heartbeat(old_lease) is None
        assert not old_store.succeed(old_lease)

        with psycopg.connect(postgres_dsn) as new_connection:
            new_store = PostgresTaskStore(new_connection, "worker-new", lease_seconds=2)
            recoveries = new_store.recover_expired()
            assert [(item.task_id, item.status) for item in recoveries] == [(task_id, "queued")]
            new_lease = cast(TaskLease, new_store.claim())
            assert new_lease.lease_id != old_lease.lease_id
            assert new_lease.attempt_count == 2

        assert old_store.heartbeat(old_lease) is None
        assert not old_store.succeed(old_lease)


def test_expired_final_attempt_fails_instead_of_requeueing(postgres_dsn: str) -> None:
    task_id = "018f0000-0000-7000-8000-000000000203"
    insert_task(postgres_dsn, task_id, attempt_count=1, max_attempts=2)

    with psycopg.connect(postgres_dsn) as connection:
        store = PostgresTaskStore(connection, "worker-final", lease_seconds=2)
        lease = cast(TaskLease, store.claim())
        assert lease.attempt_count == 2
        assert store.start(lease)
        with psycopg.connect(postgres_dsn, autocommit=True) as admin:
            admin.execute(
                """
                UPDATE worker_tasks
                SET lease_expires_at = CURRENT_TIMESTAMP - INTERVAL '1 second'
                WHERE id = %s
                """,
                (task_id,),
            )
        store.recover_expired()

    row = task_row(postgres_dsn, task_id)
    assert row["status"] == "failed"
    assert row["failure_category"] == "attempts_exhausted"
    assert row["finished_at"] is not None
    assert row["lease_id"] is None
    assert row["worker_id"] is None


def test_expired_cancel_request_is_canceled_without_retry(postgres_dsn: str) -> None:
    task_id = "018f0000-0000-7000-8000-000000000206"
    insert_task(postgres_dsn, task_id)

    with psycopg.connect(postgres_dsn) as connection:
        store = PostgresTaskStore(connection, "worker-cancel-recovery", lease_seconds=2)
        lease = cast(TaskLease, store.claim())
        assert store.start(lease)
        with psycopg.connect(postgres_dsn, autocommit=True) as admin:
            admin.execute(
                """
                UPDATE worker_tasks
                SET status = 'cancelRequested',
                    cancel_requested_at = CURRENT_TIMESTAMP,
                    lease_expires_at = CURRENT_TIMESTAMP - INTERVAL '1 second'
                WHERE id = %s
                """,
                (task_id,),
            )
        recoveries = store.recover_expired()

    assert [(item.task_id, item.status) for item in recoveries] == [(task_id, "canceled")]
    row = task_row(postgres_dsn, task_id)
    assert row["status"] == "canceled"
    assert row["attempt_count"] == 1
    assert row["finished_at"] is not None
    assert row["lease_id"] is None


def test_cancel_deadline_survives_owner_crash_within_five_seconds(postgres_dsn: str) -> None:
    task_id = "018f0000-0000-7000-8000-000000000207"
    insert_task(postgres_dsn, task_id)

    # 使用完整 15 秒租约，并让 Owner 先观察到取消再断开连接，精确覆盖复审发现的
    # 崩溃窗口：恢复不能等待租约自然到期，旧 Owner 也不能再写入任何终态。
    owner_connection = psycopg.connect(postgres_dsn)
    try:
        owner_store = PostgresTaskStore(owner_connection, "worker-owner", lease_seconds=15)
        old_lease = cast(TaskLease, owner_store.claim())
        assert owner_store.start(old_lease)
        with psycopg.connect(postgres_dsn, autocommit=True) as admin:
            admin.execute(
                """
                UPDATE worker_tasks
                SET status = 'cancelRequested',
                    cancel_requested_at = CURRENT_TIMESTAMP,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = %s AND status = 'running'
                """,
                (task_id,),
            )
        requested_at = time.monotonic()
        before_heartbeat = task_row(postgres_dsn, task_id)
        assert owner_store.heartbeat(old_lease) == "cancelRequested"
        after_heartbeat = task_row(postgres_dsn, task_id)
        assert after_heartbeat["lease_expires_at"] == before_heartbeat["lease_expires_at"]
        assert after_heartbeat["last_heartbeat_at"] == before_heartbeat["last_heartbeat_at"]
    finally:
        owner_connection.close()

    stop_event = threading.Event()
    errors: list[BaseException] = []

    def run_recovery_worker() -> None:
        try:
            WorkerRuntime(postgres_dsn, "worker-recovery").run(stop_event)
        except BaseException as exc:  # pragma: no cover - 失败会在主测试线程断言
            errors.append(exc)

    thread = threading.Thread(target=run_recovery_worker, daemon=True)
    thread.start()
    try:
        wait_for_status(postgres_dsn, task_id, "canceled")
        elapsed = time.monotonic() - requested_at
    finally:
        stop_event.set()
        thread.join(timeout=2)

    assert elapsed < 5
    assert not thread.is_alive()
    assert not errors
    row = task_row(postgres_dsn, task_id)
    assert row["lease_id"] is None
    assert row["worker_id"] is None

    with psycopg.connect(postgres_dsn) as stale_connection:
        stale_store = PostgresTaskStore(stale_connection, "worker-owner", lease_seconds=15)
        assert stale_store.heartbeat(old_lease) is None
        assert not stale_store.succeed(old_lease)
        assert not stale_store.cancel(old_lease)


def test_cancel_request_stops_contract_task_within_five_seconds(postgres_dsn: str) -> None:
    task_id = "018f0000-0000-7000-8000-000000000204"
    insert_task(
        postgres_dsn,
        task_id,
        task_type="contract.sleep",
        payload_json='{"durationSeconds":30}',
    )
    stop_event = threading.Event()
    errors: list[BaseException] = []

    def run_worker() -> None:
        try:
            WorkerRuntime(
                postgres_dsn,
                "worker-cancel",
                lease_seconds=2,
                heartbeat_seconds=0.05,
                poll_seconds=0.02,
            ).run(stop_event)
        except BaseException as exc:  # pragma: no cover - 失败会在主测试线程断言
            errors.append(exc)

    thread = threading.Thread(target=run_worker, daemon=True)
    thread.start()
    wait_for_status(postgres_dsn, task_id, "running")
    with psycopg.connect(postgres_dsn, autocommit=True) as connection:
        connection.execute(
            """
            UPDATE worker_tasks
            SET status = 'cancelRequested',
                cancel_requested_at = CURRENT_TIMESTAMP,
                updated_at = CURRENT_TIMESTAMP
            WHERE id = %s AND status = 'running'
            """,
            (task_id,),
        )
    requested_at = time.monotonic()
    wait_for_status(postgres_dsn, task_id, "canceled")
    elapsed = time.monotonic() - requested_at
    stop_event.set()
    thread.join(timeout=2)

    assert elapsed < 5
    assert not thread.is_alive()
    assert not errors
    row = task_row(postgres_dsn, task_id)
    assert row["finished_at"] is not None
    assert row["lease_id"] is None
    assert row["worker_id"] is None


def test_invalid_task_fails_without_logging_payload(
    postgres_dsn: str, caplog: pytest.LogCaptureFixture
) -> None:
    task_id = "018f0000-0000-7000-8000-000000000205"
    private_payload = "payload-must-never-appear-in-logs"
    insert_task(
        postgres_dsn,
        task_id,
        task_type="unsupported.task",
        payload_json=f'{{"value":"{private_payload}"}}',
    )
    stop_event = threading.Event()
    errors: list[BaseException] = []

    def run_worker() -> None:
        try:
            WorkerRuntime(
                postgres_dsn,
                "worker-invalid",
                lease_seconds=2,
                heartbeat_seconds=0.05,
                poll_seconds=0.02,
            ).run(stop_event)
        except BaseException as exc:  # pragma: no cover - 失败会在主测试线程断言
            errors.append(exc)

    caplog.set_level(logging.INFO, logger="coinsphere.worker")
    thread = threading.Thread(target=run_worker, daemon=True)
    thread.start()
    wait_for_status(postgres_dsn, task_id, "failed")
    stop_event.set()
    thread.join(timeout=2)

    assert not errors
    assert private_payload not in caplog.text
    row = task_row(postgres_dsn, task_id)
    assert row["failure_category"] == "invalid_task"
    assert row["error_message"] == "任务契约无效或不受支持"
