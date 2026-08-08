"""Worker 容器的 A1 PostgreSQL 消费与健康入口。"""

from __future__ import annotations

import argparse
import json
import logging
import os
import signal
import socket
from collections.abc import Sequence
from threading import Event
from types import FrameType

import psycopg

from .queue_runtime import WorkerLane, WorkerRuntime

DATABASE_DSN_ENV = "COINSPHERE_WORKER_DATABASE_DSN"


def health_document(status: str = "healthy", error_category: str | None = None) -> str:
    """返回稳定、可供容器门禁解析且不包含数据库配置的健康文档。"""

    payload: dict[str, bool | int | str] = {
        "mode": "a1-postgres",
        "protocolVersion": 1,
        "role": "quant-worker",
        "status": status,
        "taskConsumer": True,
    }
    if error_category is not None:
        payload["errorCategory"] = error_category
    return json.dumps(payload, separators=(",", ":"), sort_keys=True)


def database_healthcheck(dsn: str) -> None:
    """在三秒连接预算内确认队列表可访问，不读取任务载荷。"""

    with psycopg.connect(dsn, connect_timeout=3) as connection:
        connection.execute("SELECT 1 FROM worker_tasks LIMIT 0")


def main(argv: Sequence[str] | None = None) -> int:
    """执行 Worker 消费循环或一次性数据库健康检查。"""

    parser = argparse.ArgumentParser(description="CoinSphere Python Worker A1 runtime")
    parser.add_argument("command", choices=("run", "health"))
    parser.add_argument(
        "--lane",
        choices=tuple(item.value for item in WorkerLane),
        default=WorkerLane.REALTIME.value,
        help="task lane consumed by the worker",
    )
    args = parser.parse_args(argv)
    command = args.command
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )

    dsn = os.getenv(DATABASE_DSN_ENV, "").strip()
    if not dsn:
        logging.getLogger("coinsphere.worker").error(
            "event=worker.configuration-error error_category=database_dsn_missing"
        )
        if command == "health":
            print(health_document("unhealthy", "database_dsn_missing"), flush=True)
        return 2

    if command == "health":
        try:
            database_healthcheck(dsn)
        except (psycopg.Error, ValueError):
            # 连接异常可能带出 DSN 片段，健康日志和 JSON 只暴露固定分类。
            logging.getLogger("coinsphere.worker").error(
                "event=worker.health-failed error_category=database_unavailable"
            )
            print(health_document("unhealthy", "database_unavailable"), flush=True)
            return 1
        print(health_document(), flush=True)
        return 0

    worker_id = f"{socket.gethostname()}:{os.getpid()}"
    stop_event = Event()

    # SIGINT 与 SIGTERM 只请求停止；运行时会停止心跳并让在途任务沿统一的租约
    # 过期路径恢复，避免关机与其他 Worker 同时改写任务状态。
    def request_stop(signum: int, _frame: FrameType | None) -> None:
        logging.getLogger("coinsphere.worker").info(
            "event=worker.stop-requested worker_id=%s signal=%s",
            worker_id,
            signal.Signals(signum).name,
        )
        stop_event.set()

    previous_sigint = signal.signal(signal.SIGINT, request_stop)
    previous_sigterm = signal.signal(signal.SIGTERM, request_stop)
    try:
        WorkerRuntime(dsn, worker_id, lane=args.lane).run(stop_event)
    except Exception:
        # 运行时已按异常类型输出固定分类；入口不打印可能包含连接信息的异常正文。
        return 1
    finally:
        signal.signal(signal.SIGINT, previous_sigint)
        signal.signal(signal.SIGTERM, previous_sigterm)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
