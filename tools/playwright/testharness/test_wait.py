"""Compare wait-for-url / wait-for-request / wait-for-response commands.

These commands block until a condition is met. Since the pw_driver is
single-threaded, we can't send click + wait-for concurrently. Instead:
- For wait-for-url: navigate first, then check that wait-for-url
  returns immediately if the URL already matches.
- For wait-for-request/response: use evaluate to trigger a fetch
  inside the same withPage call (via a timed JS setTimeout), then
  wait-for picks it up.
"""


def test_wait_for_url_immediate(test_server, pw_page, pw_driver):
    """wait-for-url should return immediately if current URL already matches."""
    page1 = f"{test_server}/navigation.html"
    page2 = f"{test_server}/page2.html"

    # Real Playwright
    pw_page.goto(page2)
    assert "page2.html" in pw_page.url

    # aiscan — navigate to page2, then wait-for-url "page2" should match immediately
    pw_driver.execute("open", page1, "--session", "wurl-t", "--timeout", "10")
    pw_driver.execute("click", "wurl-t", "#link-page2")
    # page has navigated to page2; now wait-for-url should match immediately
    out = pw_driver.execute("wait-for-url", "wurl-t", "page2")
    assert "page2" in out
    pw_driver.execute("close", "wurl-t")


def test_wait_for_request_via_timed_fetch(test_server, pw_page, pw_driver):
    """Trigger a fetch via JS setTimeout, then wait-for-request catches it."""
    url = f"{test_server}/dynamic.html"

    # Real Playwright
    pw_page.goto(url)
    with pw_page.expect_request("**/api/data"):
        pw_page.click("#fetch-btn")

    # aiscan — schedule a fetch via setTimeout, then wait-for-request
    pw_driver.execute("open", url, "--session", "wreq-t", "--timeout", "10")
    # Schedule fetch to fire after 200ms
    pw_driver.execute(
        "evaluate", "wreq-t",
        "setTimeout(() => fetch('/api/data'), 200)"
    )
    out = pw_driver.execute("wait-for-request", "wreq-t", "/api/data")
    assert "/api/data" in out
    pw_driver.execute("close", "wreq-t")


def test_wait_for_response_via_timed_fetch(test_server, pw_page, pw_driver):
    """Trigger a fetch via JS setTimeout, then wait-for-response catches it."""
    url = f"{test_server}/dynamic.html"

    # Real Playwright
    pw_page.goto(url)
    with pw_page.expect_response("**/api/data"):
        pw_page.click("#fetch-btn")

    # aiscan
    pw_driver.execute("open", url, "--session", "wrsp-t", "--timeout", "10")
    pw_driver.execute(
        "evaluate", "wrsp-t",
        "setTimeout(() => fetch('/api/data'), 200)"
    )
    out = pw_driver.execute("wait-for-response", "wrsp-t", "/api/data")
    assert "/api/data" in out
    assert "200" in out
    pw_driver.execute("close", "wrsp-t")
