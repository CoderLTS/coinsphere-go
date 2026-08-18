import json
from threading import Barrier, Event

import pytest

from coinsphere_worker import __main__ as worker_main
from coinsphere_worker.__main__ import health_document, main
from coinsphere_worker.queue_runtime import WorkerLane


def test_health_document_declares_a1_consumer_boundary() -> None:
    assert json.loads(health_document()) == {
        "mode": "a1-postgres",
        "protocolVersion": 1,
        "role": "quant-worker",
        "status": "healthy",
        "taskConsumer": True,
    }


def test_health_command_exits_successfully(
    capsys: pytest.CaptureFixture[str], monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv(worker_main.DATABASE_DSN_ENV, "postgresql://test-only")
    monkeypatch.setattr(worker_main, "database_healthcheck", lambda _dsn: None)

    assert main(["health"]) == 0
    captured = capsys.readouterr()
    assert captured.out == health_document() + "\n"


def test_health_command_fails_closed_without_database_dsn(
    capsys: pytest.CaptureFixture[str], monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.delenv(worker_main.DATABASE_DSN_ENV, raising=False)

    assert main(["health"]) == 2
    captured = capsys.readouterr()
    assert json.loads(captured.out) == {
        "errorCategory": "database_dsn_missing",
        "mode": "a1-postgres",
        "protocolVersion": 1,
        "role": "quant-worker",
        "status": "unhealthy",
        "taskConsumer": True,
    }


def test_dual_lane_failure_stops_both_consumers(monkeypatch: pytest.MonkeyPatch) -> None:
    started = Barrier(2)
    calls: list[tuple[str, str]] = []

    class Runtime:
        def __init__(self, _dsn: str, worker_id: str, lane: WorkerLane) -> None:
            self.worker_id = worker_id
            self.lane = lane

        def run(self, stop_event: Event) -> None:
            calls.append((self.worker_id, self.lane.value))
            started.wait()
            if self.lane is WorkerLane.BACKTEST:
                raise RuntimeError("backtest stopped")
            stop_event.wait(1)

    monkeypatch.setattr(worker_main, "WorkerRuntime", Runtime)
    stop_event = Event()

    assert (
        worker_main.run_worker_lanes(
            "postgresql://test-only", "worker", list(WorkerLane), stop_event
        )
        == 1
    )
    assert stop_event.is_set()
    assert {lane for _, lane in calls} == {"realtime", "backtest"}
