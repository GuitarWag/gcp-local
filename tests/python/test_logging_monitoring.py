import requests


PROJECT = "local-project"


def test_logging_write_and_list(emulator_url):
    base = f"{emulator_url}/v2/entries"
    r = requests.post(
        f"{base}:write",
        json={
            "logName": f"projects/{PROJECT}/logs/py",
            "resource": {"type": "global"},
            "entries": [{"severity": "INFO", "textPayload": "py-1"}],
        },
    )
    assert r.status_code == 200
    r = requests.post(
        f"{base}:list",
        json={"resourceNames": [f"projects/{PROJECT}"]},
    )
    assert r.status_code == 200
    entries = r.json().get("entries", [])
    assert any(e.get("textPayload") == "py-1" for e in entries)


def test_monitoring_create_and_list_timeseries(emulator_url):
    base = f"{emulator_url}/v3/projects/{PROJECT}/timeSeries"
    r = requests.post(
        base,
        json={
            "timeSeries": [
                {
                    "metric": {"type": "custom.googleapis.com/py"},
                    "resource": {"type": "global"},
                    "points": [
                        {
                            "interval": {"endTime": "2026-01-01T00:00:00Z"},
                            "value": {"doubleValue": 7.0},
                        }
                    ],
                }
            ]
        },
    )
    assert r.status_code == 200
    r = requests.get(base)
    assert r.status_code == 200
    assert len(r.json().get("timeSeries", [])) >= 1
