import os
import socket
import subprocess
import time
from pathlib import Path

import pytest
import requests


REPO_ROOT = Path(__file__).resolve().parents[2]


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _wait_ready(host: str, timeout: float = 5.0) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            r = requests.get(f"http://{host}/healthz", timeout=0.5)
            if r.status_code == 200:
                return
        except requests.RequestException:
            pass
        time.sleep(0.05)
    raise RuntimeError(f"gcp-local not ready at {host} within {timeout}s")


@pytest.fixture(scope="session")
def gcp_local_binary(tmp_path_factory) -> str:
    """Build the gcp-local binary once per test session."""
    bin_dir = tmp_path_factory.mktemp("gcp-local-bin")
    bin_path = bin_dir / "gcp-local"
    subprocess.run(
        ["go", "build", "-o", str(bin_path), "./cmd/gcp-local"],
        cwd=str(REPO_ROOT),
        check=True,
    )
    return str(bin_path)


@pytest.fixture(scope="session")
def emulator(gcp_local_binary):
    """Start gcp-local on a random port for the whole session."""
    port = _free_port()
    host = f"localhost:{port}"
    proc = subprocess.Popen(
        [gcp_local_binary, "start", f"--port={port}", "--no-daemon"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    try:
        _wait_ready(host)
        yield {"host": host, "port": port}
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()


@pytest.fixture
def emulator_url(emulator) -> str:
    return f"http://{emulator['host']}"


@pytest.fixture
def storage_client(emulator):
    from google.auth.credentials import AnonymousCredentials
    from google.cloud import storage

    os.environ["STORAGE_EMULATOR_HOST"] = f"http://{emulator['host']}"
    return storage.Client(
        project="local-project",
        credentials=AnonymousCredentials(),
        client_options={"api_endpoint": f"http://{emulator['host']}"},
    )
