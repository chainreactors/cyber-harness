"""Compare extraction commands: get-attribute, is-visible, input-value."""
from conftest import aiscan_playwright


def test_get_attribute(test_server, pw_page, aiscan_bin):
    url = f"{test_server}/forms.html"

    # Real Playwright
    pw_page.goto(url)
    pw_val = pw_page.get_attribute("#visible-div", "data-custom")
    assert pw_val == "test123"

    # aiscan
    aiscan_playwright(aiscan_bin, "open", url, "--session", "attr-test", "--timeout", "10")
    out = aiscan_playwright(aiscan_bin, "get-attribute", "attr-test", "#visible-div", "data-custom")
    assert "test123" in out
    aiscan_playwright(aiscan_bin, "close", "attr-test")


def test_get_attribute_null(test_server, pw_page, aiscan_bin):
    url = f"{test_server}/forms.html"

    # Real Playwright
    pw_page.goto(url)
    pw_val = pw_page.get_attribute("#visible-div", "nonexistent")
    assert pw_val is None

    # aiscan
    aiscan_playwright(aiscan_bin, "open", url, "--session", "attr-null", "--timeout", "10")
    out = aiscan_playwright(aiscan_bin, "get-attribute", "attr-null", "#visible-div", "nonexistent")
    assert "null" in out
    aiscan_playwright(aiscan_bin, "close", "attr-null")


def test_is_visible_hidden(test_server, pw_page, aiscan_bin):
    url = f"{test_server}/forms.html"

    # Real Playwright
    pw_page.goto(url)
    assert not pw_page.is_visible("#hidden-div")
    assert pw_page.is_visible("#visible-div")

    # aiscan
    aiscan_playwright(aiscan_bin, "open", url, "--session", "vis-test", "--timeout", "10")
    hidden = aiscan_playwright(aiscan_bin, "is-visible", "vis-test", "#hidden-div")
    assert "false" in hidden
    visible = aiscan_playwright(aiscan_bin, "is-visible", "vis-test", "#visible-div")
    assert "true" in visible
    aiscan_playwright(aiscan_bin, "close", "vis-test")
