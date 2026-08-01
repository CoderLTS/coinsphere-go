from coinsphere_worker import runtime_info


def test_runtime_info_is_stable() -> None:
    info = runtime_info()

    assert info.role == "quant-worker"
    assert info.protocol_version == 1
