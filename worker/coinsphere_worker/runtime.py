"""Worker 运行时契约。"""

from dataclasses import dataclass


@dataclass(frozen=True, slots=True)
class RuntimeInfo:
    """描述 Worker 与控制面的最小兼容信息。"""

    role: str
    protocol_version: int


def runtime_info() -> RuntimeInfo:
    """返回当前 Worker 的稳定协议标识。"""

    return RuntimeInfo(role="quant-worker", protocol_version=1)
