"""Fixtures for playwright comparison harness.

The aiscan 'playwright' is a pseudo-command (not a standalone CLI).
This harness drives it through pw_driver — a persistent Go process that
wraps Command.Execute() and speaks JSON-line over stdin/stdout.
"""
import http.server
import json
import os
import subprocess
import threading
from pathlib import Path

import pytest
from playwright.sync_api import sync_playwright

HARNESS_DIR = Path(__file__).parent
FIXTURES_DIR = HARNESS_DIR / "fixtures"
PROJECT_ROOT = HARNESS_DIR.parents[3]


class _FixtureHandler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=str(FIXTURES_DIR), **kwargs)

    def do_GET(self):
        if self.path == "/api/data":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"status":"ok","value":42}')
            return
        if self.path == "/api/echo-headers":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            headers = {k: v for k, v in self.headers.items()}
            self.wfile.write(json.dumps(headers).encode())
            return
        super().do_GET()

    def log_message(self, fmt, *args):
        pass


@pytest.fixture(scope="session")
def test_server():
    server = http.server.HTTPServer(("127.0.0.1", 0), _FixtureHandler)
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    yield f"http://127.0.0.1:{port}"
    server.shutdown()


@pytest.fixture(scope="session")
def pw():
    p = sync_playwright().start()
    yield p
    p.stop()


@pytest.fixture(scope="session")
def pw_browser(pw):
    browser = pw.chromium.launch(headless=True)
    yield browser
    browser.close()


@pytest.fixture
def pw_page(pw_browser):
    page = pw_browser.new_page()
    yield page
    page.close()


class PWDriver:
    """Persistent driver for aiscan playwright pseudo-command."""

    def __init__(self, bin_path: str):
        self.proc = subprocess.Popen(
            [bin_path],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
        )

    def execute(self, *args: str) -> str:
        req = json.dumps({"args": list(args)}) + "\n"
        self.proc.stdin.write(req)
        self.proc.stdin.flush()
        line = self.proc.stdout.readline()
        if not line:
            stderr = self.proc.stderr.read()
            raise RuntimeError(f"pw_driver died: {stderr}")
        resp = json.loads(line)
        if resp.get("error"):
            raise RuntimeError(f"playwright {' '.join(args)}: {resp['error']}")
        return resp.get("output", "")

    def close(self):
        if self.proc.poll() is None:
            try:
                self.proc.stdin.write(json.dumps({"args": ["__quit__"]}) + "\n")
                self.proc.stdin.flush()
                self.proc.wait(timeout=10)
            except Exception:
                self.proc.kill()


@pytest.fixture(scope="session")
def pw_driver():
    """Build and start the persistent pw_driver process."""
    driver_src = str(HARNESS_DIR / "pw_driver.go")
    bin_path = str(PROJECT_ROOT / "pw_driver_bin")

    if not os.path.isfile(bin_path):
        result = subprocess.run(
            ["go", "build", "-tags", "browser", "-o", bin_path, driver_src],
            cwd=str(PROJECT_ROOT),
            capture_output=True,
            text=True,
            timeout=120,
        )
        if result.returncode != 0:
            pytest.skip(f"Failed to build pw_driver: {result.stderr}")

    driver = PWDriver(bin_path)
    yield driver
    driver.close()
