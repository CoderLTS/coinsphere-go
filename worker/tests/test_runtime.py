import json

import pytest

from coinsphere_worker import runtime_info
from coinsphere_worker.__main__ import health_document, main


def test_runtime_info_is_stable() -> None:
    info = runtime_info()

    assert info.role == "quant-worker"
    assert info.protocol_version == 1


def test_health_document_declares_a0_idle_boundary() -> None:
    assert json.loads(health_document()) == {
        "mode": "a0-idle",
        "protocolVersion": 1,
        "role": "quant-worker",
        "status": "healthy",
        "taskConsumer": False,
    }


def test_health_command_exits_successfully(capsys: pytest.CaptureFixture[str]) -> None:
    assert main(["health"]) == 0
    captured = capsys.readouterr()
    assert captured.out == health_document() + "\n"
