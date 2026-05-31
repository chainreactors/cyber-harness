"""Fixtures for playwright comparison harness."""
import http.server
import os
import subprocess
import threading
from pathlib import Path

import pytest
from playwright.sync_api import sync_playwright

HARNESS_DIR = Path(__file__).parent
FIXTURES_DIR = HARNESS_DIR / "fixtures"
PROJECT_ROOT = HARNESS_DIR.parents[3]  # pkg/tools/playwright/testharness -> root


class _FixtureHandler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=str(FIXTURES_DIR), **kwargs)

    def log_message(self, fmt, *args):
        pass  # suppress request logs


@pytest.fixture(scope="session")
def test_server():
    """Start a local HTTP server serving fixture HTML pages."""
    server = http.server.HTTPServer(("127.0.0.1", 0), _FixtureHandler)
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    yield f"http://127.0.0.1:{port}"
    server.shutdown()


@pytest.fixture(scope="session")
def pw():
    """Real Playwright instance (session-scoped)."""
    p = sync_playwright().start()
    yield p
    p.stop()


@pytest.fixture(scope="session")
def pw_browser(pw):
    """Chromium browser for real Playwright tests."""
    browser = pw.chromium.launch(headless=True)
    yield browser
    browser.close()


@pytest.fixture
def pw_page(pw_browser):
    """Fresh Playwright page per test."""
    page = pw_browser.new_page()
    yield page
    page.close()


@pytest.fixture(scope="session")
def aiscan_bin():
    """Path to the aiscan binary compiled with browser tag.

    Set AISCAN_BIN env var to skip compilation, or let the fixture
    build it into a temp location.
    """
    env_bin = os.environ.get("AISCAN_BIN")
    if env_bin and os.path.isfile(env_bin):
        return env_bin

    bin_path = PROJECT_ROOT / "aiscan_test_bin"
    if bin_path.exists():
        return str(bin_path)

    result = subprocess.run(
        ["go", "build", "-tags", "browser", "-o", str(bin_path), "."],
        cwd=str(PROJECT_ROOT),
        capture_output=True,
        text=True,
        timeout=120,
    )
    if result.returncode != 0:
        pytest.skip(f"Failed to build aiscan: {result.stderr}")
    return str(bin_path)


def aiscan_playwright(aiscan_bin_path: str, *args, timeout=30) -> str:
    """Run `aiscan playwright <args>` and return stdout."""
    cmd = [aiscan_bin_path, "playwright", *args]
    result = subprocess.run(
        cmd, capture_output=True, text=True, timeout=timeout
    )
    if result.returncode != 0:
        raise RuntimeError(
            f"aiscan playwright failed: {result.stderr}\nstdout: {result.stdout}"
        )
    return result.stdout.strip()
