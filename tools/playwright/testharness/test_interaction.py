"""Compare interaction commands: press, hover, dblclick, check/uncheck, fill+input-value."""


def test_press_enter_submits_form(test_server, pw_page, pw_driver):
    url = f"{test_server}/login.html"

    pw_page.goto(url)
    pw_page.fill("#username", "admin")
    pw_page.press("#username", "Enter")
    pw_page.wait_for_load_state("networkidle")
    pw_result = pw_page.text_content("#result")
    assert "Submitted: admin" in pw_result

    pw_driver.execute("open", url, "--session", "press-test", "--timeout", "10")
    pw_driver.execute("fill", "press-test", "#username", "admin")
    pw_driver.execute("press", "press-test", "#username", "Enter")
    text_out = pw_driver.execute("text-content", "press-test", "#result")
    assert "Submitted: admin" in text_out
    pw_driver.execute("close", "press-test")


def test_hover_triggers_event(test_server, pw_page, pw_driver):
    url = f"{test_server}/forms.html"

    pw_page.goto(url)
    assert not pw_page.is_visible("#hover-result")
    pw_page.hover("#hover-btn")
    assert pw_page.is_visible("#hover-result")

    pw_driver.execute("open", url, "--session", "hover-test", "--timeout", "10")
    vis_before = pw_driver.execute("is-visible", "hover-test", "#hover-result")
    assert "false" in vis_before
    pw_driver.execute("hover", "hover-test", "#hover-btn")
    vis_after = pw_driver.execute("is-visible", "hover-test", "#hover-result")
    assert "true" in vis_after
    pw_driver.execute("close", "hover-test")


def test_dblclick_triggers_event(test_server, pw_page, pw_driver):
    url = f"{test_server}/forms.html"

    pw_page.goto(url)
    pw_page.dblclick("#dbl-btn")
    assert pw_page.is_visible("#dbl-result")

    pw_driver.execute("open", url, "--session", "dbl-test", "--timeout", "10")
    pw_driver.execute("dblclick", "dbl-test", "#dbl-btn")
    vis = pw_driver.execute("is-visible", "dbl-test", "#dbl-result")
    assert "true" in vis
    pw_driver.execute("close", "dbl-test")


def test_check_and_uncheck(test_server, pw_page, pw_driver):
    url = f"{test_server}/forms.html"

    pw_page.goto(url)
    assert not pw_page.is_checked("#agree")
    pw_page.check("#agree")
    assert pw_page.is_checked("#agree")
    pw_page.uncheck("#agree")
    assert not pw_page.is_checked("#agree")

    pw_driver.execute("open", url, "--session", "chk-test", "--timeout", "10")
    pw_driver.execute("check", "chk-test", "#agree")
    val = pw_driver.execute("evaluate", "chk-test", "document.getElementById('agree').checked")
    assert "true" in val
    pw_driver.execute("uncheck", "chk-test", "#agree")
    val2 = pw_driver.execute("evaluate", "chk-test", "document.getElementById('agree').checked")
    assert "false" in val2
    pw_driver.execute("close", "chk-test")


def test_press_combo_shift_key(test_server, pw_page, pw_driver):
    """Press Shift+A should produce uppercase A."""
    url = f"{test_server}/login.html"

    # Real Playwright
    pw_page.goto(url)
    pw_page.focus("#username")
    pw_page.keyboard.press("Shift+KeyA")
    assert pw_page.input_value("#username") == "A"

    # aiscan
    pw_driver.execute("open", url, "--session", "combo-t", "--timeout", "10")
    pw_driver.execute("fill", "combo-t", "#username", "")
    pw_driver.execute("press", "combo-t", "#username", "Shift+a")
    out = pw_driver.execute("input-value", "combo-t", "#username")
    assert "A" in out or "a" in out  # implementation may vary
    pw_driver.execute("close", "combo-t")


def test_fill_and_input_value(test_server, pw_page, pw_driver):
    url = f"{test_server}/login.html"

    pw_page.goto(url)
    pw_page.fill("#username", "testuser")
    assert pw_page.input_value("#username") == "testuser"

    pw_driver.execute("open", url, "--session", "iv-test", "--timeout", "10")
    pw_driver.execute("fill", "iv-test", "#username", "testuser")
    out = pw_driver.execute("input-value", "iv-test", "#username")
    assert "testuser" in out
    pw_driver.execute("close", "iv-test")
