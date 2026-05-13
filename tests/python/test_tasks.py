import base64
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

import requests


PROJECT = "local-project"


class _Target(BaseHTTPRequestHandler):
    received = []

    def do_POST(self):
        n = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(n)
        _Target.received.append(body)
        self.send_response(200)
        self.end_headers()

    def log_message(self, format, *args):
        pass


def test_cloud_tasks_dispatches_http(emulator_url):
    _Target.received = []
    srv = HTTPServer(("127.0.0.1", 0), _Target)
    port = srv.server_address[1]
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    try:
        base = f"{emulator_url}/v2/projects/{PROJECT}/locations/loc/queues"
        r = requests.post(base, json={"name": "py-queue"})
        assert r.status_code == 200, r.text

        r = requests.post(
            f"{base}/py-queue/tasks",
            json={
                "task": {
                    "httpRequest": {
                        "url": f"http://127.0.0.1:{port}/",
                        "httpMethod": "POST",
                        "body": base64.b64encode(b"py-ping").decode(),
                    }
                }
            },
        )
        assert r.status_code == 200

        deadline = time.time() + 2
        while time.time() < deadline and not _Target.received:
            time.sleep(0.02)
        assert _Target.received == [b"py-ping"]
    finally:
        srv.shutdown()
