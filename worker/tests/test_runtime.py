import json

import pytest

from coinsphere_worker import __main__ as worker_main
from coinsphere_worker import runtime_info
from coinsphere_worker.__main__ import health_document, main


def test_runtime_info_is_stable() -> None:
    info = runtime_info()

    assert info.role == "quant-worker"
    assert info.protocol_version == 1


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
