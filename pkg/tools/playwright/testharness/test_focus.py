"""Compare focus/blur commands."""


def test_focus_triggers_event(test_server, pw_page, pw_driver):
    url = f"{test_server}/forms.html"

    # Real Playwright
    pw_page.goto(url)
    pw_page.focus("#focus-target")
    assert pw_page.text_content("#focus-result") == "focused"

    # aiscan
    pw_driver.execute("open", url, "--session", "focus-t", "--timeout", "10")
    pw_driver.execute("focus", "focus-t", "#focus-target")
    out = pw_driver.execute("text-content", "focus-t", "#focus-result")
    assert "focused" in out
    pw_driver.execute("close", "focus-t")


def test_blur_triggers_event(test_server, pw_page, pw_driver):
    url = f"{test_server}/forms.html"

    # Real Playwright
    pw_page.goto(url)
    pw_page.focus("#focus-target")
    assert pw_page.text_content("#focus-result") == "focused"
    pw_page.locator("#focus-target").blur()
    assert pw_page.text_content("#focus-result") == "blurred"

    # aiscan
    pw_driver.execute("open", url, "--session", "blur-t", "--timeout", "10")
    pw_driver.execute("focus", "blur-t", "#focus-target")
    pw_driver.execute("blur", "blur-t", "#focus-target")
    out = pw_driver.execute("text-content", "blur-t", "#focus-result")
    assert "blurred" in out
    pw_driver.execute("close", "blur-t")
