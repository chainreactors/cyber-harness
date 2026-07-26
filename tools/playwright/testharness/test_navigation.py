"""Compare navigation commands: goto, reload, go-back, go-forward."""


def test_goto_returns_text(test_server, pw_page, pw_driver):
    url = f"{test_server}/login.html"

    pw_page.goto(url)
    pw_text = pw_page.text_content("h1")
    assert "Login Page" in pw_text

    out = pw_driver.execute("goto", url)
    assert "Login Page" in out


def test_reload_preserves_url(test_server, pw_page, pw_driver):
    url = f"{test_server}/login.html"

    pw_page.goto(url)
    pw_page.reload()
    assert "login.html" in pw_page.url

    pw_driver.execute("open", url, "--session", "nav-reload", "--timeout", "10")
    reload_out = pw_driver.execute("reload", "nav-reload")
    assert "Reloaded" in reload_out
    assert "login.html" in reload_out
    pw_driver.execute("close", "nav-reload")


def test_go_back_and_forward(test_server, pw_page, pw_driver):
    page1 = f"{test_server}/navigation.html"
    page2 = f"{test_server}/page2.html"

    pw_page.goto(page1)
    assert "Page 1" in pw_page.text_content("#page-title")
    pw_page.goto(page2)
    assert "Page 2" in pw_page.text_content("#page-title")
    pw_page.go_back()
    assert "navigation.html" in pw_page.url
    pw_page.go_forward()
    assert "page2.html" in pw_page.url

    pw_driver.execute("open", page1, "--session", "nav-hist", "--timeout", "10")
    pw_driver.execute("click", "nav-hist", "#link-page2")
    back_out = pw_driver.execute("go-back", "nav-hist")
    assert "navigation.html" in back_out
    fwd_out = pw_driver.execute("go-forward", "nav-hist")
    assert "page2.html" in fwd_out
    pw_driver.execute("close", "nav-hist")
