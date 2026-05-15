"""End-to-end test of the CloudSQL pg-wire shim from Python.

Creates an instance via the admin REST API, reads the assigned host/port,
then drives schema + CRUD with psycopg2 (extended-query protocol by default).
"""

import pytest
import requests

psycopg2 = pytest.importorskip("psycopg2")


PROJECT = "local-project"


def test_cloudsql_postgres_wire(emulator_url):
    base = f"{emulator_url}/sql/v1beta4/projects/{PROJECT}/instances"
    r = requests.post(
        base,
        json={
            "name": "py-wire",
            "databaseVersion": "POSTGRES_15",
            "engine": "sqlite",
            "database": "appdb",
        },
    )
    assert r.status_code == 200, r.text
    inst = r.json()
    assert inst["port"] > 0
    assert inst["host"] == "127.0.0.1"

    conn = psycopg2.connect(
        host=inst["host"],
        port=inst["port"],
        dbname="appdb",
        user="gcp-local",
        password="local",
        sslmode="disable",
    )
    conn.autocommit = True
    try:
        with conn.cursor() as cur:
            cur.execute(
                "CREATE TABLE items (id INTEGER PRIMARY KEY, label TEXT, qty INTEGER)"
            )
            cur.execute(
                "INSERT INTO items (id, label, qty) VALUES (%s, %s, %s)",
                (1, "widget", 7),
            )
            cur.execute(
                "INSERT INTO items (id, label, qty) VALUES (%s, %s, %s)",
                (2, "gadget", 3),
            )

            cur.execute("SELECT id, label, qty FROM items WHERE id = %s", (1,))
            row = cur.fetchone()
            assert row == (1, "widget", 7)

            cur.execute("UPDATE items SET qty = %s WHERE label = %s", (8, "widget"))
            assert cur.rowcount == 1

            cur.execute("SELECT label FROM items ORDER BY id")
            labels = [r[0] for r in cur.fetchall()]
            assert labels == ["widget", "gadget"]

            cur.execute("DELETE FROM items WHERE id = %s", (2,))
            assert cur.rowcount == 1
    finally:
        conn.close()
