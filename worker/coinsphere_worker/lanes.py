"""Small in-process lane boundary for realtime and backtest work."""

from __future__ import annotations

from collections.abc import Callable, Iterator
from contextlib import contextmanager
from enum import StrEnum
from threading import Semaphore
from typing import TypeVar


class WorkerLane(StrEnum):
    REALTIME = "realtime"
    BACKTEST = "backtest"


class LaneBusyError(RuntimeError):
    """The lane's single execution slot is already occupied."""


T = TypeVar("T")


class LaneRuntime:
    """Independent semaphores keep a long backtest from occupying realtime."""

    def __init__(self) -> None:
        self._slots = {
            WorkerLane.REALTIME: Semaphore(1),
            WorkerLane.BACKTEST: Semaphore(1),
        }

    @contextmanager
    def slot(self, lane: WorkerLane | str) -> Iterator[None]:
        try:
            selected = WorkerLane(lane)
        except ValueError as exc:
            raise ValueError(f"unsupported worker lane: {lane}") from exc
        semaphore = self._slots[selected]
        if not semaphore.acquire(blocking=False):
            raise LaneBusyError(f"{selected.value} lane is busy")
        try:
            yield
        finally:
            semaphore.release()

    def run(self, lane: WorkerLane | str, function: Callable[[], T]) -> T:
        with self.slot(lane):
            return function()

    def run_realtime(self, function: Callable[[], T]) -> T:
        return self.run(WorkerLane.REALTIME, function)

    def run_backtest(self, function: Callable[[], T]) -> T:
        return self.run(WorkerLane.BACKTEST, function)

__all__ = ["LaneBusyError", "LaneRuntime", "WorkerLane"]
