"""Compare navigation commands: goto, reload, go-back, go-forward."""
from conftest import aiscan_playwright


def test_goto_returns_text(test_server, pw_page, aiscan_bin):
    url = f"{test_server}/login.html"

    # Real Playwright
    pw_page.goto(url)
    pw_text = pw_page.text_content("h1")

    # aiscan
    out = aiscan_playwright(aiscan_bin, "goto", url)

    assert "Login Page" in pw_text
    assert "Login Page" in out


def test_reload_preserves_url(test_server, pw_page, aiscan_bin):
    url = f"{test_server}/login.html"

    # Real Playwright
    pw_page.goto(url)
    pw_page.reload()
    assert "login.html" in pw_page.url

    # aiscan
    open_out = aiscan_playwright(aiscan_bin, "open", url, "--session", "nav-reload", "--timeout", "10")
    assert "Session: nav-reload" in open_out
    reload_out = aiscan_playwright(aiscan_bin, "reload", "nav-reload")
    assert "Reloaded" in reload_out
    assert "login.html" in reload_out
    aiscan_playwright(aiscan_bin, "close", "nav-reload")


def test_go_back_and_forward(test_server, pw_page, aiscan_bin):
    page1 = f"{test_server}/navigation.html"
    page2 = f"{test_server}/page2.html"

    # Real Playwright
    pw_page.goto(page1)
    assert "Page 1" in pw_page.text_content("#page-title")
    pw_page.goto(page2)
    assert "Page 2" in pw_page.text_content("#page-title")
    pw_page.go_back()
    assert "navigation.html" in pw_page.url
    pw_page.go_forward()
    assert "page2.html" in pw_page.url

    # aiscan
    aiscan_playwright(aiscan_bin, "open", page1, "--session", "nav-hist", "--timeout", "10")
    aiscan_playwright(aiscan_bin, "click", "nav-hist", "#link-page2")
    back_out = aiscan_playwright(aiscan_bin, "go-back", "nav-hist")
    assert "navigation.html" in back_out
    fwd_out = aiscan_playwright(aiscan_bin, "go-forward", "nav-hist")
    assert "page2.html" in fwd_out
    aiscan_playwright(aiscan_bin, "close", "nav-hist")
