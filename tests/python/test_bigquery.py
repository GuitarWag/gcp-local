import requests


PROJECT = "local-project"


def test_bigquery_dataset_table_insert_query(emulator_url):
    base = f"{emulator_url}/bigquery/v2/projects/{PROJECT}"

    r = requests.post(
        f"{base}/datasets",
        json={"datasetReference": {"datasetId": "pyds"}},
    )
    assert r.status_code == 200, r.text

    r = requests.post(
        f"{base}/datasets/pyds/tables",
        json={
            "tableReference": {"tableId": "pyt"},
            "schema": {
                "fields": [
                    {"name": "id", "type": "INTEGER"},
                    {"name": "name", "type": "STRING"},
                ]
            },
        },
    )
    assert r.status_code == 200, r.text

    r = requests.post(
        f"{base}/datasets/pyds/tables/pyt/insertAll",
        json={
            "rows": [
                {"json": {"id": 1, "name": "alpha"}},
                {"json": {"id": 2, "name": "beta"}},
            ]
        },
    )
    assert r.status_code == 200, r.text

    r = requests.post(
        f"{base}/queries",
        json={"query": "SELECT name FROM pyds.pyt ORDER BY id"},
    )
    assert r.status_code == 200, r.text
    rows = r.json()["rows"]
    values = [c["v"] for r2 in rows for c in r2["f"]]
    assert values == ["alpha", "beta"]
