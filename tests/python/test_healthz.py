import requests


def test_healthz_reports_services_ready(emulator_url):
    r = requests.get(f"{emulator_url}/healthz", timeout=2)
    assert r.status_code == 200
    body = r.json()
    assert body["status"] == "ok"
    assert body["services"]["storage"] == "ready"
    assert body["services"]["pubsub"] == "ready"
