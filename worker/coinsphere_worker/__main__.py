"""Worker 容器的 A0 运行与健康入口。"""

import argparse
import json
from collections.abc import Sequence
from contextlib import suppress
from threading import Event

from .runtime import runtime_info


def health_document() -> str:
    """返回稳定、可供容器门禁解析的 A0 健康文档。"""

    info = runtime_info()
    payload: dict[str, bool | int | str] = {
        "mode": "a0-idle",
        "protocolVersion": info.protocol_version,
        "role": info.role,
        "status": "healthy",
        "taskConsumer": False,
    }
    return json.dumps(payload, separators=(",", ":"), sort_keys=True)


def main(argv: Sequence[str] | None = None) -> int:
    """执行前台空闲进程或一次性健康检查。"""

    parser = argparse.ArgumentParser(description="CoinSphere Python Worker A0 runtime")
    parser.add_argument("command", choices=("run", "health"))
    command = parser.parse_args(argv).command

    print(health_document(), flush=True)
    if command == "run":
        with suppress(KeyboardInterrupt):
            Event().wait()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
