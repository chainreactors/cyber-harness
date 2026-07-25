"""Compare set-viewport command."""


def test_set_viewport(test_server, pw_page, pw_driver):
    url = f"{test_server}/dynamic.html"

    # Real Playwright
    pw_page.set_viewport_size({"width": 800, "height": 600})
    pw_page.goto(url)
    pw_w = pw_page.evaluate("window.innerWidth")
    pw_h = pw_page.evaluate("window.innerHeight")
    assert pw_w == 800
    assert pw_h == 600

    # aiscan — set viewport then reload to pick up new dimensions
    pw_driver.execute("open", url, "--session", "vp-t", "--timeout", "10")
    pw_driver.execute("set-viewport", "vp-t", "800", "600")
    pw_driver.execute("reload", "vp-t")
    out = pw_driver.execute("evaluate", "vp-t", "window.innerWidth + 'x' + window.innerHeight")
    assert "800" in out
    assert "600" in out
    pw_driver.execute("close", "vp-t")
