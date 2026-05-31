"""Compare interaction commands: press, hover, dblclick, check/uncheck, fill+input-value."""
from conftest import aiscan_playwright


def test_press_enter_submits_form(test_server, pw_page, aiscan_bin):
    url = f"{test_server}/login.html"

    # Real Playwright
    pw_page.goto(url)
    pw_page.fill("#username", "admin")
    pw_page.press("#username", "Enter")
    pw_page.wait_for_load_state("networkidle")
    pw_result = pw_page.text_content("#result")
    assert "Submitted: admin" in pw_result

    # aiscan
    aiscan_playwright(aiscan_bin, "open", url, "--session", "press-test", "--timeout", "10")
    aiscan_playwright(aiscan_bin, "fill", "press-test", "#username", "admin")
    aiscan_playwright(aiscan_bin, "press", "press-test", "#username", "Enter")
    text_out = aiscan_playwright(aiscan_bin, "text-content", "press-test", "#result")
    assert "Submitted: admin" in text_out
    aiscan_playwright(aiscan_bin, "close", "press-test")


def test_hover_triggers_event(test_server, pw_page, aiscan_bin):
    url = f"{test_server}/forms.html"

    # Real Playwright
    pw_page.goto(url)
    assert not pw_page.is_visible("#hover-result")
    pw_page.hover("#hover-btn")
    assert pw_page.is_visible("#hover-result")

    # aiscan
    aiscan_playwright(aiscan_bin, "open", url, "--session", "hover-test", "--timeout", "10")
    vis_before = aiscan_playwright(aiscan_bin, "is-visible", "hover-test", "#hover-result")
    assert "false" in vis_before
    aiscan_playwright(aiscan_bin, "hover", "hover-test", "#hover-btn")
    vis_after = aiscan_playwright(aiscan_bin, "is-visible", "hover-test", "#hover-result")
    assert "true" in vis_after
    aiscan_playwright(aiscan_bin, "close", "hover-test")


def test_dblclick_triggers_event(test_server, pw_page, aiscan_bin):
    url = f"{test_server}/forms.html"

    # Real Playwright
    pw_page.goto(url)
    pw_page.dblclick("#dbl-btn")
    assert pw_page.is_visible("#dbl-result")

    # aiscan
    aiscan_playwright(aiscan_bin, "open", url, "--session", "dbl-test", "--timeout", "10")
    aiscan_playwright(aiscan_bin, "dblclick", "dbl-test", "#dbl-btn")
    vis = aiscan_playwright(aiscan_bin, "is-visible", "dbl-test", "#dbl-result")
    assert "true" in vis
    aiscan_playwright(aiscan_bin, "close", "dbl-test")


def test_check_and_uncheck(test_server, pw_page, aiscan_bin):
    url = f"{test_server}/forms.html"

    # Real Playwright
    pw_page.goto(url)
    assert not pw_page.is_checked("#agree")
    pw_page.check("#agree")
    assert pw_page.is_checked("#agree")
    pw_page.uncheck("#agree")
    assert not pw_page.is_checked("#agree")

    # aiscan
    aiscan_playwright(aiscan_bin, "open", url, "--session", "chk-test", "--timeout", "10")
    aiscan_playwright(aiscan_bin, "check", "chk-test", "#agree")
    val = aiscan_playwright(aiscan_bin, "evaluate", "chk-test", "document.getElementById('agree').checked")
    assert "true" in val
    aiscan_playwright(aiscan_bin, "uncheck", "chk-test", "#agree")
    val2 = aiscan_playwright(aiscan_bin, "evaluate", "chk-test", "document.getElementById('agree').checked")
    assert "false" in val2
    aiscan_playwright(aiscan_bin, "close", "chk-test")


def test_fill_and_input_value(test_server, pw_page, aiscan_bin):
    url = f"{test_server}/login.html"

    # Real Playwright
    pw_page.goto(url)
    pw_page.fill("#username", "testuser")
    assert pw_page.input_value("#username") == "testuser"

    # aiscan
    aiscan_playwright(aiscan_bin, "open", url, "--session", "iv-test", "--timeout", "10")
    aiscan_playwright(aiscan_bin, "fill", "iv-test", "#username", "testuser")
    out = aiscan_playwright(aiscan_bin, "input-value", "iv-test", "#username")
    assert "testuser" in out
    aiscan_playwright(aiscan_bin, "close", "iv-test")
