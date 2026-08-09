"""A1 Worker 租约协议的真实 PostgreSQL 集成测试。"""

from __future__ import annotations

import hashlib
import json
import logging
import math
import os
import threading
import time
import uuid
from collections.abc import Iterator
from concurrent.futures import ThreadPoolExecutor
from datetime import UTC, datetime, timedelta
from decimal import Decimal
from gzip import decompress
from pathlib import Path
from queue import Queue
from typing import Any, cast

import psycopg
import pytest
from psycopg import sql
from psycopg.conninfo import make_conninfo

from coinsphere_worker.queue_runtime import PostgresTaskStore, TaskLease, WorkerLane, WorkerRuntime

POSTGRES_DSN_ENV = "COINSPHERE_TEST_POSTGRES_DSN"


def test_realtime_payload_requires_exact_uuidv7_and_utc_keys() -> None:
    task = TaskLease(
        task_id="019d2000-0000-7000-8000-000000000001",
        task_type="strategy.realtime",
        payload_json=(
            '{"instanceId":"019d2000-0000-7000-8000-000000000002",'
            '"candleOpenTime":"2026-08-08T00:00:00Z"}'
        ),
        attempt_count=1,
        max_attempts=3,
        lease_id="lease",
        worker_id="worker",
    )
    instance_id, candle_time = WorkerRuntime._realtime_payload(task)
    assert instance_id == "019d2000-0000-7000-8000-000000000002"
    assert candle_time.isoformat() == "2026-08-08T00:00:00+00:00"

    for payload in (
        '{"instanceId":"019d2000-0000-4000-8000-000000000002","candleOpenTime":"2026-08-08T00:00:00Z"}',
        (
            '{"instanceId":"019d2000-0000-7000-8000-000000000002",'
            '"candleOpenTime":"2026-08-08 00:00:00Z"}'
        ),
        (
            '{"instanceId":"019d2000-0000-7000-8000-000000000002",'
            '"candleOpenTime":"2026-08-08T00:00:00+00:00"}'
        ),
        (
            '{"instanceId":"019d2000-0000-7000-8000-000000000002",'
            '"candleOpenTime":"2026-08-08T00:00:00Z","extra":1}'
        ),
    ):
        invalid = TaskLease(
            task_id=task.task_id,
            task_type=task.task_type,
            payload_json=payload,
            attempt_count=task.attempt_count,
            max_attempts=task.max_attempts,
            lease_id=task.lease_id,
            worker_id=task.worker_id,
        )
        with pytest.raises(ValueError):
            WorkerRuntime._realtime_payload(invalid)


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
    migration_directory = (
        Path(__file__).resolve().parents[2]
        / "backend"
        / "internal"
        / "migration"
        / "sql"
    )
    up_parts: list[str] = []
    for migration_path in sorted(migration_directory.glob("*.sql")):
        up_sql, separator, _down_sql = migration_path.read_text(encoding="utf-8").partition(
            "-- +goose Down"
        )
        assert separator, f"Worker migration {migration_path.name} 缺少 Down 分隔符"
        up_parts.append(up_sql.replace("-- +goose Up", "", 1))
    up_sql = "\n".join(up_parts)

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
def empty_queue(request: pytest.FixtureRequest) -> Iterator[None]:
    """每个用例只清理随机测试 schema 中的量化任务与资源。"""

    if "postgres_dsn" not in request.fixturenames:
        yield
        return
    postgres_dsn = cast(str, request.getfixturevalue("postgres_dsn"))
    with psycopg.connect(postgres_dsn, autocommit=True) as connection:
        connection.execute(
            "TRUNCATE notification_deliveries, domain_event_outbox, strategy_signals, "
            "strategy_instances, backtests, strategy_versions, strategies, worker_tasks CASCADE"
        )
    yield


def insert_task(
    dsn: str,
    task_id: str,
    *,
    task_type: str = "contract.noop",
    payload_json: str = "{}",
    attempt_count: int = 0,
    max_attempts: int = 3,
    lane: str = "realtime",
    priority: int = 0,
) -> None:
    with psycopg.connect(dsn, autocommit=True) as connection:
        connection.execute(
            """
            INSERT INTO worker_tasks (
                id, task_type, payload_json, attempt_count, max_attempts, lane, priority
            ) VALUES (%s, %s, %s, %s, %s, %s, %s)
            """,
            (task_id, task_type, payload_json, attempt_count, max_attempts, lane, priority),
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


def claim_with_worker(
    dsn: str,
    worker_id: str,
    barrier: threading.Barrier,
    lane: WorkerLane = WorkerLane.REALTIME,
) -> TaskLease | None:
    with psycopg.connect(dsn) as connection:
        store = PostgresTaskStore(connection, worker_id, lane=lane)
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


def test_worker_lanes_claim_only_their_own_priority_queue(postgres_dsn: str) -> None:
    insert_task(postgres_dsn, "018f0000-0000-7000-8000-000000000208", priority=1)
    insert_task(
        postgres_dsn,
        "018f0000-0000-7000-8000-000000000209",
        lane=WorkerLane.BACKTEST.value,
        priority=1,
    )
    insert_task(
        postgres_dsn,
        "018f0000-0000-7000-8000-000000000210",
        lane=WorkerLane.BACKTEST.value,
        priority=10,
    )

    with psycopg.connect(postgres_dsn) as connection:
        realtime = PostgresTaskStore(connection, "worker-realtime", lane=WorkerLane.REALTIME)
        realtime_task = realtime.claim()
        assert realtime_task is not None
        assert realtime_task.lane == WorkerLane.REALTIME.value
        assert realtime_task.task_id == "018f0000-0000-7000-8000-000000000208"

    with psycopg.connect(postgres_dsn) as connection:
        backtest = PostgresTaskStore(connection, "worker-backtest", lane=WorkerLane.BACKTEST)
        backtest_task = backtest.claim()
        assert backtest_task is not None
        assert backtest_task.lane == WorkerLane.BACKTEST.value
        assert backtest_task.task_id == "018f0000-0000-7000-8000-000000000210"


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


def test_strategy_failure_and_backtest_result_commit(
    postgres_dsn: str, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    instrument_id = "019d0000-0000-7000-8000-000000000001"
    strategy_id = "019d0000-0000-7000-8000-000000000010"
    failed_version_id = "019d0000-0000-7000-8000-000000000011"
    failed_task_id = "019d0000-0000-7000-8000-000000000012"
    published_version_id = "019d0000-0000-7000-8000-000000000021"
    published_task_id = "019d0000-0000-7000-8000-000000000022"
    backtest_id = "019d0000-0000-7000-8000-000000000023"
    backtest_task_id = "019d0000-0000-7000-8000-000000000024"
    source = "def on_bar(candles, params):\n    return Decimal('0')\n"
    code_sha = hashlib.sha256(source.encode()).hexdigest()
    artifact_root = tmp_path / "artifacts"
    for name, value in {
        "COINSPHERE_WORKER_ARTIFACT_DIR": str(artifact_root),
        "COINSPHERE_WORKER_BACKTEST_WALL_SECONDS": "10",
        "COINSPHERE_WORKER_BACKTEST_CPU_SECONDS": "10",
        "COINSPHERE_WORKER_BACKTEST_MEMORY_BYTES": "536870912",
        "COINSPHERE_WORKER_BACKTEST_ARTIFACT_BYTES": "1048576",
    }.items():
        monkeypatch.setenv(name, value)
    runtime = WorkerRuntime(postgres_dsn, "worker-backtest", lane=WorkerLane.BACKTEST)

    with psycopg.connect(postgres_dsn, autocommit=True) as connection:
        connection.execute(
            """
            INSERT INTO market_instruments (
                id, venue, market_type, native_symbol, base_asset, quote_asset, status,
                price_tick, quantity_step, min_quantity, min_notional, updated_at
            ) VALUES (%s, 'binance', 'spot', 'BTCUSDT', 'BTC', 'USDT', 'trading',
                      0.1, 0.001, 0.001, 5, CURRENT_TIMESTAMP)
            """,
            (instrument_id,),
        )
        owner = connection.execute(
            "INSERT INTO users (username) VALUES ('worker-strategy-owner') RETURNING id"
        ).fetchone()
        assert owner is not None
        record_ids: list[int] = []
        for scope, key in (
            ("strategy:publish:failed", "a"),
            ("strategy:publish:published", "b"),
            ("backtest:create", "c"),
        ):
            record = connection.execute(
                """
                INSERT INTO idempotency_records (
                    user_id, scope, key_hash, request_hash, expires_at, created_at
                ) VALUES (%s, %s, repeat(%s, 64), repeat('d', 64),
                          CURRENT_TIMESTAMP + INTERVAL '1 day', CURRENT_TIMESTAMP)
                RETURNING id
                """,
                (owner[0], scope, key),
            ).fetchone()
            assert record is not None
            record_ids.append(cast(int, record[0]))
        connection.execute(
            """
            INSERT INTO strategies (
                id, name, source_code, market_type, instrument_id, interval_code,
                lookback_bars, parameter_schema_json, created_by_user_id, updated_by_user_id
            ) VALUES (%s, 'hold', %s, 'spot', %s, '1m', 1, '{}', %s, %s)
            """,
            (strategy_id, source, instrument_id, owner[0], owner[0]),
        )
        connection.execute(
            """
            INSERT INTO worker_tasks (id, task_type, payload_json, lane)
            VALUES (%s, 'strategy.publish', %s, 'backtest')
            """,
            (
                failed_task_id,
                json.dumps(
                    {"strategyId": strategy_id, "strategyVersionId": failed_version_id}
                ),
            ),
        )
        connection.execute(
            """
            INSERT INTO strategy_versions (
                id, strategy_id, version_number, worker_task_id, idempotency_record_id,
                name, source_code, code_sha256, runtime_version, market_type, instrument_id, symbol,
                interval_code, lookback_bars, parameter_schema_json, published_by_user_id
            ) VALUES (%s, %s, 1, %s, %s, 'invalid', 'invalid source', repeat('e', 64),
                      'python3.12', 'spot', %s, 'BTCUSDT', '1m', 1, '{}', %s)
            """,
            (
                failed_version_id,
                strategy_id,
                failed_task_id,
                record_ids[0],
                instrument_id,
                owner[0],
            ),
        )

    with psycopg.connect(postgres_dsn) as connection:
        store = PostgresTaskStore(connection, "worker-publish", lane=WorkerLane.BACKTEST)
        failed_lease = cast(TaskLease, store.claim())
        assert failed_lease.task_id == failed_task_id
        assert store.start(failed_lease)
        output: Queue[tuple[str, object | None]] = Queue(maxsize=1)
        runtime._execute_task(failed_lease, threading.Event(), output)
        assert output.get_nowait()[0] == "invalid_task"
        assert store.fail(failed_lease, "invalid_task", retryable=False) == "failed"
    with psycopg.connect(postgres_dsn) as connection:
        failed_status = connection.execute(
            "SELECT status FROM strategy_versions WHERE id = %s", (failed_version_id,)
        ).fetchone()
    assert failed_status == ("failed",)

    with psycopg.connect(postgres_dsn, autocommit=True) as connection:
        connection.execute(
            """
            INSERT INTO worker_tasks (
                id, task_type, payload_json, status, attempt_count, lane,
                finished_at, result_json
            ) VALUES (%s, 'strategy.publish', %s, 'succeeded', 1, 'backtest',
                      CURRENT_TIMESTAMP, '{"status":"completed"}')
            """,
            (
                published_task_id,
                json.dumps(
                    {"strategyId": strategy_id, "strategyVersionId": published_version_id}
                ),
            ),
        )
        connection.execute(
            """
            INSERT INTO strategy_versions (
                id, strategy_id, version_number, worker_task_id, idempotency_record_id,
                name, source_code, code_sha256, runtime_version, market_type, instrument_id, symbol,
                interval_code, lookback_bars, parameter_schema_json, published_by_user_id
            ) VALUES (%s, %s, 2, %s, %s, 'hold', %s, %s, 'python3.12', 'spot', %s, 'BTCUSDT',
                      '1m', 1, '{"count":{"type":"integer","default":1,
                      "minimum":"1","maximum":"2"}}', %s)
            """,
            (
                published_version_id,
                strategy_id,
                published_task_id,
                record_ids[1],
                source,
                code_sha,
                instrument_id,
                owner[0],
            ),
        )
        connection.execute(
            """
            UPDATE strategy_versions
            SET status = 'published', published_at = CURRENT_TIMESTAMP
            WHERE id = %s
            """,
            (published_version_id,),
        )
        connection.execute(
            """
            INSERT INTO worker_tasks (id, task_type, payload_json, lane)
            VALUES (%s, 'strategy.backtest', %s, 'backtest')
            """,
            (backtest_task_id, json.dumps({"backtestId": backtest_id})),
        )
        connection.execute(
            """
            INSERT INTO backtests (
                id, owner_user_id, strategy_version_id, worker_task_id,
                idempotency_record_id, simulator_version, parameters_json,
                start_time, end_time, allocation_usdt, initial_equity, fee_rate,
                slippage_rate, funding_rates_json
            ) VALUES (%s, %s, %s, %s, %s, 'decimal-bar-v1', '{}',
                      TIMESTAMPTZ '2026-08-01 00:00:00+00',
                      TIMESTAMPTZ '2026-08-01 00:01:00+00', 100, 1000, 0, 0, '[]')
            """,
            (
                backtest_id,
                owner[0],
                published_version_id,
                backtest_task_id,
                record_ids[2],
            ),
        )
        connection.execute(
            """
            INSERT INTO market_candles (
                venue, instrument_id, interval_code, open_time, close_time,
                open_price, high_price, low_price, close_price, base_volume, is_closed
            ) VALUES ('binance', %s, '1m', TIMESTAMPTZ '2026-08-01 00:00:00+00',
                      TIMESTAMPTZ '2026-08-01 00:01:00+00', 100, 101, 99, 100, 10, TRUE)
            """,
            (instrument_id,),
        )

    with psycopg.connect(postgres_dsn) as connection:
        store = PostgresTaskStore(
            connection, "worker-backtest", lease_seconds=30, lane=WorkerLane.BACKTEST
        )
        lease = cast(TaskLease, store.claim())
        assert lease.task_id == backtest_task_id
        assert store.start(lease)
    result = runtime._backtest(lease, threading.Event())
    assert runtime._complete_domain_task(lease, result)

    manifest = cast(dict[str, Any], result["manifest"])
    references = cast(dict[str, str], manifest["references"])
    input_path = artifact_root / backtest_id / references["input"]
    input_lines = decompress(input_path.read_bytes()).decode().splitlines()
    configuration = json.loads(input_lines[0])
    assert len(input_lines) == 2
    assert configuration["type"] == "configuration"
    assert configuration["sourceCode"] == source
    assert configuration["simulatorVersion"] == "decimal-bar-v1"
    assert configuration["allocationUsdt"] == "100"

    with psycopg.connect(postgres_dsn) as connection:
        row = connection.execute(
            """
            SELECT task.status, backtest.summary_json, backtest.input_sha256,
                   backtest.result_sha256, backtest.manifest_sha256
            FROM backtests AS backtest
            JOIN worker_tasks AS task ON task.id = backtest.worker_task_id
            WHERE backtest.id = %s
            """,
            (backtest_id,),
        ).fetchone()
    assert row is not None
    assert row[0] == "succeeded"
    assert row[1]["type"] == "summary"
    assert all(isinstance(value, str) and len(value) == 64 for value in row[2:])
    assert row[4] == manifest["sha256"]


def test_realtime_signal_is_idempotent_and_expires_manual(postgres_dsn: str) -> None:
    instrument_id = "019d3000-0000-7000-8000-000000000001"
    strategy_id = "019d3000-0000-7000-8000-000000000010"
    version_id = "019d3000-0000-7000-8000-000000000011"
    publish_task_id = "019d3000-0000-7000-8000-000000000012"
    instance_id = "019d3000-0000-7000-8000-000000000013"
    first_task_id = "019d3000-0000-7000-8000-000000000014"
    second_task_id = "019d3000-0000-7000-8000-000000000015"
    duplicate_task_id = "019d3000-0000-7000-8000-000000000016"
    out_of_order_instance_id = "019d3000-0000-7000-8000-000000000017"
    later_task_id = "019d3000-0000-7000-8000-000000000018"
    delayed_task_id = "019d3000-0000-7000-8000-000000000019"
    source = "def on_bar(candles, params):\n    return Decimal('0.5')\n"
    code_sha = hashlib.sha256(source.encode()).hexdigest()

    with psycopg.connect(postgres_dsn, autocommit=True) as connection:
        connection.execute(
            """
            INSERT INTO market_instruments (
                id, venue, market_type, native_symbol, base_asset, quote_asset, status,
                price_tick, quantity_step, min_quantity, min_notional, updated_at
            ) VALUES (%s, 'binance', 'spot', 'ETHUSDT', 'ETH', 'USDT', 'trading',
                      0.1, 0.001, 0.001, 5, CURRENT_TIMESTAMP)
            """,
            (instrument_id,),
        )
        owner = connection.execute(
            "INSERT INTO users (username) VALUES ('worker-realtime-owner') RETURNING id"
        ).fetchone()
        assert owner is not None
        record = connection.execute(
            """
            INSERT INTO idempotency_records (
                user_id, scope, key_hash, request_hash, expires_at, created_at
            ) VALUES (%s, 'strategy:publish:realtime', repeat('r', 64), repeat('s', 64),
                      CURRENT_TIMESTAMP + INTERVAL '1 day', CURRENT_TIMESTAMP)
            RETURNING id
            """,
            (owner[0],),
        ).fetchone()
        assert record is not None
        connection.execute(
            """
            INSERT INTO strategies (
                id, name, source_code, market_type, instrument_id, interval_code,
                lookback_bars, parameter_schema_json, created_by_user_id, updated_by_user_id
            ) VALUES (%s, 'realtime hold', %s, 'spot', %s, '1m', 2, '{}', %s, %s)
            """,
            (strategy_id, source, instrument_id, owner[0], owner[0]),
        )
        connection.execute(
            """
            INSERT INTO worker_tasks (
                id, task_type, payload_json, status, attempt_count, lane, finished_at
            ) VALUES (%s, 'strategy.publish', %s, 'succeeded', 1, 'backtest', CURRENT_TIMESTAMP)
            """,
            (
                publish_task_id,
                json.dumps({"strategyId": strategy_id, "strategyVersionId": version_id}),
            ),
        )
        connection.execute(
            """
            INSERT INTO strategy_versions (
                id, strategy_id, version_number, status, worker_task_id, idempotency_record_id,
                name, source_code, code_sha256, runtime_version, market_type, instrument_id,
                symbol, interval_code, lookback_bars, parameter_schema_json,
                published_by_user_id, published_at
            ) VALUES (%s, %s, 1, 'published', %s, %s, 'realtime hold', %s, %s,
                      'python3.12', 'spot', %s, 'ETHUSDT', '1m', 2, '{}', %s,
                      CURRENT_TIMESTAMP)
            """,
            (
                version_id,
                strategy_id,
                publish_task_id,
                record[0],
                source,
                code_sha,
                instrument_id,
                owner[0],
            ),
        )
        connection.execute(
            """
            INSERT INTO strategy_instances (
                id, owner_user_id, strategy_version_id, name, mode, environment, is_enabled
            ) VALUES (%s, %s, %s, 'realtime manual', 'manual', 'paper', TRUE)
            """,
            (instance_id, owner[0], version_id),
        )
        connection.execute(
            """
            INSERT INTO strategy_instances (
                id, owner_user_id, strategy_version_id, name, mode, environment, is_enabled
            ) VALUES (%s, %s, %s, 'out of order manual', 'manual', 'paper', TRUE)
            """,
            (out_of_order_instance_id, owner[0], version_id),
        )
        base_candle_open = datetime(2099, 8, 8, tzinfo=UTC)
        for index in range(102):
            open_time = base_candle_open + timedelta(minutes=index)
            close_time = open_time + timedelta(minutes=1)
            connection.execute(
                """
                INSERT INTO market_candles (
                    venue, instrument_id, interval_code, open_time, close_time,
                    open_price, high_price, low_price, close_price, base_volume, is_closed
                ) VALUES ('binance', %s, '1m', %s, %s, 100, 101, 99, 100, 10, TRUE)
                """,
                (
                    instrument_id,
                    open_time,
                    close_time,
                ),
            )

    def insert_realtime_task(
        task_id: str, candle_open_time: str, strategy_instance_id: str = instance_id
    ) -> None:
        payload = json.dumps(
            {"instanceId": strategy_instance_id, "candleOpenTime": candle_open_time},
            separators=(",", ":"),
        )
        with psycopg.connect(postgres_dsn, autocommit=True) as connection:
            connection.execute(
                """
                INSERT INTO worker_tasks (id, task_type, payload_json, lane, dedupe_key)
                VALUES (%s, 'strategy.realtime', %s, 'realtime', %s)
                """,
                (task_id, payload, f"test:{task_id}"),
            )

    runtime = WorkerRuntime(postgres_dsn, "worker-realtime", lease_seconds=30)

    def execute_realtime(
        task_id: str, candle_open_time: str, strategy_instance_id: str = instance_id
    ) -> dict[str, object]:
        insert_realtime_task(task_id, candle_open_time, strategy_instance_id)
        with psycopg.connect(postgres_dsn) as connection:
            store = PostgresTaskStore(
                connection, "worker-realtime", lease_seconds=30, lane=WorkerLane.REALTIME
            )
            lease = cast(TaskLease, store.claim())
            assert lease.task_id == task_id
            assert store.start(lease)
        result = runtime._realtime(lease)
        assert runtime._complete_domain_task(lease, result)
        return result

    first = execute_realtime(first_task_id, "2099-08-08T00:00:00Z")
    assert first["target"] == Decimal("0.5")
    second = execute_realtime(second_task_id, "2099-08-08T00:01:00Z")
    assert second["expiresAt"] is not None
    duplicate = execute_realtime(duplicate_task_id, "2099-08-08T00:01:00Z")
    assert duplicate["target"] == second["target"]

    with psycopg.connect(postgres_dsn) as connection:
        signals = connection.execute(
            """
            SELECT candle_open_time, status, target
            FROM strategy_signals
            WHERE strategy_instance_id = %s
            ORDER BY candle_open_time
            """,
            (instance_id,),
        ).fetchall()
        tasks = connection.execute(
            "SELECT id, status FROM worker_tasks WHERE id IN (%s, %s, %s) ORDER BY id",
            (first_task_id, second_task_id, duplicate_task_id),
        ).fetchall()
        events = connection.execute(
            """
            SELECT aggregate_id, payload_json::jsonb
            FROM domain_event_outbox
            WHERE event_type = 'strategy.signal.created'
            ORDER BY aggregate_id
            """
        ).fetchall()
    assert [(row[0].isoformat(), row[1], row[2]) for row in signals] == [
        ("2099-08-08T00:00:00+00:00", "expired", Decimal("0.5")),
        ("2099-08-08T00:01:00+00:00", "active", Decimal("0.5")),
    ]
    assert tasks == [
        (first_task_id, "succeeded"),
        (second_task_id, "succeeded"),
        (duplicate_task_id, "succeeded"),
    ]
    assert [row[0] for row in events] == [first_task_id, second_task_id]
    assert [row[1]["signalId"] for row in events] == [first_task_id, second_task_id]
    assert all(row[1]["target"] == "0.5" for row in events)

    execute_realtime(later_task_id, "2099-08-08T00:01:00Z", out_of_order_instance_id)
    execute_realtime(delayed_task_id, "2099-08-08T00:00:00Z", out_of_order_instance_id)
    with psycopg.connect(postgres_dsn) as connection:
        out_of_order_signals = connection.execute(
            """
            SELECT candle_open_time, status
            FROM strategy_signals
            WHERE strategy_instance_id = %s
            ORDER BY candle_open_time
            """,
            (out_of_order_instance_id,),
        ).fetchall()
    assert [(row[0].isoformat(), row[1]) for row in out_of_order_signals] == [
        ("2099-08-08T00:00:00+00:00", "expired"),
        ("2099-08-08T00:01:00+00:00", "active"),
    ]

    # Keep a steady 20-event/second input so the sample represents normal queue
    # load while measuring the DB-backed path end to end.
    latency_tasks = [
        (
            f"019d3000-0000-7000-8000-{100 + index:012x}",
            (base_candle_open + timedelta(minutes=2 + index)).isoformat().replace("+00:00", "Z"),
        )
        for index in range(100)
    ]
    latency_runtime = WorkerRuntime(
        postgres_dsn,
        "worker-realtime-p99",
        lease_seconds=30,
        heartbeat_seconds=0.1,
        poll_seconds=0.01,
    )
    stop_event = threading.Event()
    worker_errors: list[BaseException] = []

    def run_latency_worker() -> None:
        try:
            latency_runtime.run(stop_event)
        except BaseException as exc:
            worker_errors.append(exc)

    worker_thread = threading.Thread(target=run_latency_worker, name="realtime-p99-worker")
    worker_thread.start()
    try:
        for task_id, candle_open_time in latency_tasks:
            insert_realtime_task(task_id, candle_open_time)
            time.sleep(0.05)

        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            with psycopg.connect(postgres_dsn) as connection:
                row = connection.execute(
                    "SELECT count(*) FROM strategy_signals "
                    "WHERE strategy_instance_id = %s AND candle_open_time >= %s",
                    (instance_id, base_candle_open + timedelta(minutes=2)),
                ).fetchone()
            if row is not None and row[0] == len(latency_tasks):
                break
            time.sleep(0.02)
        else:
            pytest.fail("实时信号 p99 样本未在 30 秒内全部持久化")
    finally:
        stop_event.set()
        worker_thread.join(timeout=5)

    if worker_thread.is_alive():
        pytest.fail("实时信号 p99 Worker 未能在停止请求后退出")
    if worker_errors:
        pytest.fail(f"实时信号 p99 Worker failed: {type(worker_errors[0]).__name__}")

    with psycopg.connect(postgres_dsn) as connection:
        latency_rows = connection.execute(
            "SELECT EXTRACT(EPOCH FROM (signal.created_at - task.queued_at)) "
            "FROM strategy_signals AS signal "
            "JOIN worker_tasks AS task ON task.id = signal.id::text "
            "WHERE signal.strategy_instance_id = %s "
            "AND signal.candle_open_time >= %s "
            "ORDER BY task.queued_at, task.id",
            (instance_id, base_candle_open + timedelta(minutes=2)),
        ).fetchall()
    latencies = [float(row[0]) for row in latency_rows if row[0] is not None]
    assert len(latencies) == len(latency_tasks)
    ordered_latencies = sorted(latencies)
    p99_index = max(0, math.ceil(len(ordered_latencies) * 0.99) - 1)
    p99_seconds = ordered_latencies[p99_index]
    assert p99_seconds <= 2.0, (
        f"实时信号持久化 p99={p99_seconds:.3f}s, "
        f"max={max(ordered_latencies):.3f}s, samples={len(ordered_latencies)}"
    )
