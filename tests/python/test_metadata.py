import base64
import json
import os

import requests

FLAVOR = {"Metadata-Flavor": "Google"}


def test_metadata_project_id(emulator_url):
    r = requests.get(f"{emulator_url}/computeMetadata/v1/project/project-id", headers=FLAVOR)
    assert r.status_code == 200, r.text
    assert r.text.strip() == "local-project"
    assert r.headers["Metadata-Flavor"] == "Google"


def test_metadata_token(emulator_url):
    r = requests.get(
        f"{emulator_url}/computeMetadata/v1/instance/service-accounts/default/token",
        headers=FLAVOR,
    )
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["token_type"] == "Bearer"
    assert body["access_token"]
    assert body["expires_in"] > 0


def test_metadata_identity_audience(emulator_url):
    r = requests.get(
        f"{emulator_url}/computeMetadata/v1/instance/service-accounts/default/identity",
        params={"audience": "https://py.test"},
        headers=FLAVOR,
    )
    assert r.status_code == 200, r.text
    parts = r.text.strip().split(".")
    assert len(parts) == 3
    payload = parts[1] + "=" * (-len(parts[1]) % 4)
    claims = json.loads(base64.urlsafe_b64decode(payload))
    assert claims["aud"] == "https://py.test"


def test_metadata_missing_flavor_header_rejected(emulator_url):
    r = requests.get(f"{emulator_url}/computeMetadata/v1/project/project-id")
    assert r.status_code == 403


def test_metadata_google_auth_metadata_client(emulator_url, monkeypatch):
    # The google-auth Python SDK reads GCE_METADATA_HOST to redirect the
    # metadata client at a non-default endpoint. This proves the same
    # primitive google.auth.default() falls through to on a real VM works
    # against the emulator without bringing gcloud or ADC files into play.
    monkeypatch.setenv("GCE_METADATA_HOST", emulator_url.replace("http://", ""))

    from google.auth.compute_engine import _metadata

    import google.auth.transport.requests as g_requests

    request = g_requests.Request()
    # Skip _metadata.ping() — it probes 169.254.169.254 by default rather
    # than GCE_METADATA_HOST, so without also overriding GCE_METADATA_IP it
    # never reaches the emulator. get_project_id and get_service_account_*
    # take the GCE_METADATA_HOST path that ADC actually uses for token
    # fetches once it's decided it's on GCE.
    project = _metadata.get_project_id(request)
    assert project == "local-project"
    info = _metadata.get_service_account_info(request)
    assert info["email"].endswith("@local-project.iam.gserviceaccount.com")
