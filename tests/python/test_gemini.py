"""Gemini API (generativelanguage) tests via the real google-genai client."""

import subprocess
import time

import pytest
import requests
from google import genai
from google.genai import types

from conftest import REPO_ROOT, _free_port, _wait_ready


GEMINI_CONFIG = """
project: local-project
state: memory
services:
  gemini:
    models:
      - name: gemini-2.0-flash
        reply: "Hello from the fixture model"
        chunks: 3
        countTokens: 42
      - name: gemini-1.5-pro
        reply: "Pro reply words here"
"""


@pytest.fixture(scope="session")
def gemini_emulator(gcp_local_binary, tmp_path_factory):
    """A second emulator instance with fixture Gemini models configured."""
    cfg = tmp_path_factory.mktemp("gemini") / "gcp-local.yaml"
    cfg.write_text(GEMINI_CONFIG)
    port = _free_port()
    host = f"localhost:{port}"
    proc = subprocess.Popen(
        [gcp_local_binary, "start", f"--port={port}", f"--config={cfg}", "--no-daemon"],
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
def gemini_client(gemini_emulator):
    """A real google-genai client pointed at the fixture-enabled emulator."""
    return genai.Client(
        api_key="test-key",
        http_options=types.HttpOptions(base_url=f"http://{gemini_emulator['host']}"),
    )


def test_gemini_generate_content(gemini_client):
    resp = gemini_client.models.generate_content(
        model="gemini-2.0-flash",
        contents="Say hello",
    )
    assert resp.text == "Hello from the fixture model"
    assert resp.model_version == "gemini-2.0-flash"
    assert resp.candidates[0].finish_reason == types.FinishReason.STOP
    assert resp.usage_metadata.total_token_count > 0


def test_gemini_generate_content_streaming(gemini_client):
    chunks = list(
        gemini_client.models.generate_content_stream(
            model="gemini-2.0-flash",
            contents="Say hello",
        )
    )
    assert len(chunks) == 3
    text = "".join(c.text or "" for c in chunks)
    assert text == "Hello from the fixture model"
    # finishReason only on the last frame.
    for chunk in chunks[:-1]:
        assert chunk.candidates[0].finish_reason is None
    assert chunks[-1].candidates[0].finish_reason == types.FinishReason.STOP


def test_gemini_count_tokens(gemini_client):
    resp = gemini_client.models.count_tokens(
        model="gemini-2.0-flash",
        contents="count me",
    )
    assert resp.total_tokens == 42


def test_gemini_list_models(gemini_client):
    models = list(gemini_client.models.list())
    names = [m.name for m in models]
    assert "models/gemini-2.0-flash" in names
    assert "models/gemini-1.5-pro" in names
    flash = next(m for m in models if m.name == "models/gemini-2.0-flash")
    assert "generateContent" in flash.supported_actions


def test_gemini_get_model(gemini_client):
    model = gemini_client.models.get(model="gemini-1.5-pro")
    assert model.name == "models/gemini-1.5-pro"


def test_gemini_unknown_model_404(gemini_client):
    from google.genai import errors as genai_errors

    with pytest.raises(genai_errors.APIError) as exc:
        gemini_client.models.generate_content(
            model="gemini-nope",
            contents="hi",
        )
    assert exc.value.code == 404
