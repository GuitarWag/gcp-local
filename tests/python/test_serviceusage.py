import requests


PROJECT = "local-project"


def test_list_enabled_filter(emulator_url):
    r = requests.get(
        f"{emulator_url}/v1/projects/{PROJECT}/services",
        params={"filter": "state:ENABLED"},
    )
    assert r.status_code == 200
    services = r.json()["services"]
    assert len(services) == 19
    assert all(s["state"] == "ENABLED" for s in services)
    names = [s["name"] for s in services]
    assert all(n.endswith(".googleapis.com") for n in names)
    assert names == sorted(names)
    pubsub = next(s for s in services if s["config"]["name"] == "pubsub.googleapis.com")
    assert pubsub["config"]["title"] == "Cloud Pub/Sub API"


def test_get_unknown_service(emulator_url):
    r = requests.get(f"{emulator_url}/v1/projects/{PROJECT}/services/bogus.googleapis.com")
    assert r.status_code == 404
    body = r.json()
    assert body["error"]["code"] == 404
    assert body["error"]["status"] == "NOT_FOUND"


def test_enable_operation_get(emulator_url):
    base = f"{emulator_url}/v1/projects/{PROJECT}/services/pubsub.googleapis.com"
    r = requests.post(f"{base}:enable")
    assert r.status_code == 200
    op = r.json()
    assert op["done"] is True
    assert (
        op["response"]["@type"]
        == "type.googleapis.com/google.api.serviceusage.v1.EnableServiceResponse"
    )
    r = requests.get(f"{emulator_url}/v1/{op['name']}")
    assert r.status_code == 200
    assert r.json()["done"] is True
