"""Compare dispatch-event command."""


def test_dispatch_custom_event(test_server, pw_page, pw_driver):
    url = f"{test_server}/forms.html"

    # Real Playwright
    pw_page.goto(url)
    assert pw_page.text_content("#dispatch-target") == "waiting"
    pw_page.dispatch_event("#dispatch-target", "custom-ping")
    assert pw_page.text_content("#dispatch-target") == "pinged"

    # aiscan
    pw_driver.execute("open", url, "--session", "disp-t", "--timeout", "10")
    before = pw_driver.execute("text-content", "disp-t", "#dispatch-target")
    assert "waiting" in before
    pw_driver.execute("dispatch-event", "disp-t", "#dispatch-target", "custom-ping")
    after = pw_driver.execute("text-content", "disp-t", "#dispatch-target")
    assert "pinged" in after
    pw_driver.execute("close", "disp-t")
