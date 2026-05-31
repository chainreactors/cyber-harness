"""Compare route/unroute (request interception) command."""


def test_route_fulfill(test_server, pw_page, pw_driver):
    """Route intercepts /api/data and returns custom response."""
    url = f"{test_server}/dynamic.html"

    # Real Playwright
    pw_page.route(
        "**/api/data",
        lambda route: route.fulfill(
            status=200,
            content_type="application/json",
            body='{"intercepted":true}'
        ),
    )
    pw_page.goto(url)
    pw_page.click("#fetch-btn")
    pw_page.wait_for_timeout(500)
    result = pw_page.text_content("#fetch-result")
    assert "intercepted" in result
    pw_page.unroute("**/api/data")

    # aiscan — set route first, then navigate, then click
    pw_driver.execute("open", url, "--session", "route-t", "--timeout", "10")
    pw_driver.execute(
        "route", "route-t", "*/api/data",
        "--fulfill", "--status", "200",
        "--body", '{"intercepted":true}',
        "--content-type", "application/json",
    )
    # Use evaluate to trigger fetch and wait for result inline
    pw_driver.execute(
        "evaluate", "route-t",
        "fetch('/api/data').then(r=>r.json()).then(d=>{document.getElementById('fetch-result').textContent=JSON.stringify(d)})"
    )
    pw_driver.execute("wait-for", "route-t", "--stable")
    out = pw_driver.execute("text-content", "route-t", "#fetch-result")
    assert "intercepted" in out
    pw_driver.execute("unroute", "route-t")
    pw_driver.execute("close", "route-t")


def test_route_abort(test_server, pw_page, pw_driver):
    """Route aborts /api/data — fetch should fail."""
    url = f"{test_server}/dynamic.html"

    # Real Playwright
    pw_page.route("**/api/data", lambda route: route.abort())
    pw_page.goto(url)
    pw_page.click("#fetch-btn")
    pw_page.wait_for_timeout(500)
    result = pw_page.text_content("#fetch-result")
    assert "error" in result.lower()
    pw_page.unroute("**/api/data")

    # aiscan
    pw_driver.execute("open", url, "--session", "abort-t", "--timeout", "10")
    pw_driver.execute("route", "abort-t", "*/api/data", "--abort")
    pw_driver.execute(
        "evaluate", "abort-t",
        "fetch('/api/data').then(r=>r.text()).then(t=>{document.getElementById('fetch-result').textContent=t}).catch(e=>{document.getElementById('fetch-result').textContent='error:'+e.message})"
    )
    pw_driver.execute("wait-for", "abort-t", "--stable")
    out = pw_driver.execute("text-content", "abort-t", "#fetch-result")
    assert "error" in out.lower()
    pw_driver.execute("unroute", "abort-t")
    pw_driver.execute("close", "abort-t")
