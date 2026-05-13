import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

import requests


PROJECT = "local-project"


def test_cloudsql_instance_crud(emulator_url):
    base = f"{emulator_url}/sql/v1beta4/projects/{PROJECT}/instances"
    r = requests.post(base, json={"name": "py-sql", "databaseVersion": "POSTGRES_15"})
    assert r.status_code == 200, r.text

    r = requests.get(f"{base}/py-sql")
    assert r.status_code == 200
    body = r.json()
    assert body["name"] == "py-sql"
    assert body["state"] == "RUNNABLE"

    r = requests.delete(f"{base}/py-sql")
    assert r.status_code == 200


class _Echo(BaseHTTPRequestHandler):
    last = None

    def do_POST(self):
        n = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(n)
        _Echo.last = body
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        pass


def test_cloudrun_invoke_proxies(emulator_url):
    srv = HTTPServer(("127.0.0.1", 0), _Echo)
    port = srv.server_address[1]
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    try:
        base = f"{emulator_url}/v2/projects/{PROJECT}/locations/loc/services"
        r = requests.post(base, json={"name": "py-svc", "backendUrl": f"http://127.0.0.1:{port}/"})
        assert r.status_code == 200

        r = requests.post(f"{base}/py-svc/invoke", json={"echo": "py"})
        assert r.status_code == 200
        assert r.json() == {"echo": "py"}
    finally:
        srv.shutdown()


def test_dashboard_serves_html(emulator_url):
    r = requests.get(f"{emulator_url}/dashboard")
    assert r.status_code == 200
    assert "text/html" in r.headers.get("content-type", "")
    assert "gcp-local" in r.text
