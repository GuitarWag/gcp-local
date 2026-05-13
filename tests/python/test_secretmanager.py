import base64
import requests


PROJECT = "local-project"


def test_secret_crud_and_access(emulator_url):
    base = f"{emulator_url}/v1/projects/{PROJECT}/secrets"
    r = requests.post(f"{base}?secretId=py-secret", json={})
    assert r.status_code == 200, r.text

    payload = base64.b64encode(b"py-classified").decode()
    r = requests.post(
        f"{base}/py-secret:addVersion",
        json={"payload": {"data": payload}},
    )
    assert r.status_code == 200, r.text

    r = requests.get(f"{base}/py-secret/versions/latest:access")
    assert r.status_code == 200
    body = r.json()
    decoded = base64.b64decode(body["payload"]["data"])
    assert decoded == b"py-classified"

    r = requests.delete(f"{base}/py-secret")
    assert r.status_code == 204


def test_secret_missing_id_returns_400(emulator_url):
    r = requests.post(f"{emulator_url}/v1/projects/{PROJECT}/secrets", json={})
    assert r.status_code == 400
