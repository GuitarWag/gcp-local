"""End-to-end test of the CloudSQL MySQL-wire shim from Python.

Creates a mysql-engine instance via the admin REST API, reads the assigned
host/port, then drives schema + CRUD with mysql-connector-python.
"""

import pytest
import requests

mysql_connector = pytest.importorskip("mysql.connector")


PROJECT = "local-project"


def test_cloudsql_mysql_wire(emulator_url):
    base = f"{emulator_url}/sql/v1beta4/projects/{PROJECT}/instances"
    r = requests.post(
        base,
        json={
            "name": "py-mysql-wire",
            "engine": "mysql",
            "database": "appdb",
        },
    )
    assert r.status_code == 200, r.text
    inst = r.json()
    assert inst["port"] > 0
    assert inst["host"] == "127.0.0.1"

    conn = mysql_connector.connect(
        host=inst["host"],
        port=inst["port"],
        database="appdb",
        user="gcp-local",
        password="local",
        auth_plugin="mysql_native_password",
        autocommit=True,
    )
    try:
        cur = conn.cursor()
        cur.execute(
            "CREATE TABLE items (id INT PRIMARY KEY, label VARCHAR(100), qty INT) "
            "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
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
        assert row == (1, "widget", 7), row

        cur.execute("UPDATE items SET qty = %s WHERE label = %s", (8, "widget"))
        assert cur.rowcount == 1

        cur.execute("SELECT label FROM items ORDER BY id")
        labels = [r[0] for r in cur.fetchall()]
        assert labels == ["widget", "gadget"]

        cur.execute("DELETE FROM items WHERE id = %s", (2,))
        assert cur.rowcount == 1
        cur.close()
    finally:
        conn.close()
