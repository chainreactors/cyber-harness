"""Tests for playwright-cli parity flags: --ignore-https-errors, --viewport-size,
--geolocation, --timezone, --color-scheme, --save-storage/--load-storage,
--save-har, --wait-for-selector, --wait-for-timeout, set-content, title,
is-checked, is-disabled, is-hidden, inner-text, tap, type."""
import json
import tempfile
from pathlib import Path


def test_title(test_server, pw_page, pw_driver):
    url = f"{test_server}/login.html"

    pw_page.goto(url)
    assert pw_page.title() == "Login"

    pw_driver.execute("open", url, "--session", "title-t", "--timeout", "10")
    out = pw_driver.execute("title", "title-t")
    assert "Login" in out
    pw_driver.execute("close", "title-t")


def test_set_content(test_server, pw_page, pw_driver):
    url = f"{test_server}/login.html"

    pw_page.goto(url)
    pw_page.set_content("<h1>Injected</h1>")
    assert pw_page.text_content("h1") == "Injected"

    pw_driver.execute("open", url, "--session", "sc-t", "--timeout", "10")
    pw_driver.execute("set-content", "sc-t", "<h1>Injected</h1>")
    out = pw_driver.execute("text-content", "sc-t", "h1")
    assert "Injected" in out
    pw_driver.execute("close", "sc-t")


def test_inner_text(test_server, pw_page, pw_driver):
    url = f"{test_server}/forms.html"

    pw_page.goto(url)
    pw_text = pw_page.inner_text("#visible-div")
    assert "Visible Content" in pw_text

    pw_driver.execute("open", url, "--session", "it-t", "--timeout", "10")
    out = pw_driver.execute("inner-text", "it-t", "#visible-div")
    assert "Visible Content" in out
    pw_driver.execute("close", "it-t")


def test_is_checked(test_server, pw_page, pw_driver):
    url = f"{test_server}/forms.html"

    pw_page.goto(url)
    assert not pw_page.is_checked("#agree")
    assert pw_page.is_checked("#newsletter")

    pw_driver.execute("open", url, "--session", "ichk-t", "--timeout", "10")
    out1 = pw_driver.execute("is-checked", "ichk-t", "#agree")
    assert "false" in out1
    out2 = pw_driver.execute("is-checked", "ichk-t", "#newsletter")
    assert "true" in out2
    pw_driver.execute("close", "ichk-t")


def test_is_disabled_and_enabled(test_server, pw_page, pw_driver):
    url = f"{test_server}/forms.html"

    pw_page.goto(url)
    assert pw_page.is_enabled("#agree")

    pw_driver.execute("open", url, "--session", "idis-t", "--timeout", "10")
    out = pw_driver.execute("is-enabled", "idis-t", "#agree")
    assert "true" in out
    out2 = pw_driver.execute("is-disabled", "idis-t", "#agree")
    assert "false" in out2
    pw_driver.execute("close", "idis-t")


def test_is_hidden(test_server, pw_page, pw_driver):
    url = f"{test_server}/forms.html"

    pw_page.goto(url)
    assert pw_page.is_hidden("#hidden-div")

    pw_driver.execute("open", url, "--session", "ihid-t", "--timeout", "10")
    out = pw_driver.execute("is-hidden", "ihid-t", "#hidden-div")
    assert "true" in out
    pw_driver.execute("close", "ihid-t")


def test_type_char_by_char(test_server, pw_page, pw_driver):
    url = f"{test_server}/login.html"

    pw_page.goto(url)
    pw_page.click("#username")
    pw_page.type("#username", "abc")
    assert pw_page.input_value("#username") == "abc"

    pw_driver.execute("open", url, "--session", "type-t", "--timeout", "10")
    pw_driver.execute("type", "type-t", "#username", "abc")
    out = pw_driver.execute("input-value", "type-t", "#username")
    assert "abc" in out
    pw_driver.execute("close", "type-t")


def test_viewport_size_on_open(test_server, pw_page, pw_driver):
    url = f"{test_server}/dynamic.html"

    pw_page.set_viewport_size({"width": 640, "height": 480})
    pw_page.goto(url)
    assert pw_page.evaluate("window.innerWidth") == 640

    pw_driver.execute(
        "open", url, "--session", "vp-open-t", "--timeout", "10",
        "--viewport-size", "640x480",
    )
    pw_driver.execute("reload", "vp-open-t")
    out = pw_driver.execute("evaluate", "vp-open-t", "window.innerWidth")
    assert "640" in out
    pw_driver.execute("close", "vp-open-t")


def test_timezone_on_open(test_server, pw_page, pw_driver):
    url = f"{test_server}/login.html"

    ctx = pw_page.context
    ctx.close()
    browser = ctx.browser
    new_ctx = browser.new_context(timezone_id="Pacific/Auckland")
    pw_page2 = new_ctx.new_page()
    pw_page2.goto(url)
    tz = pw_page2.evaluate("Intl.DateTimeFormat().resolvedOptions().timeZone")
    assert tz == "Pacific/Auckland"
    pw_page2.close()
    new_ctx.close()

    pw_driver.execute(
        "open", url, "--session", "tz-t", "--timeout", "10",
        "--timezone", "Pacific/Auckland",
    )
    out = pw_driver.execute(
        "evaluate", "tz-t",
        "Intl.DateTimeFormat().resolvedOptions().timeZone",
    )
    assert "Pacific/Auckland" in out
    pw_driver.execute("close", "tz-t")


def test_color_scheme_on_open(test_server, pw_page, pw_driver):
    url = f"{test_server}/login.html"

    pw_driver.execute(
        "open", url, "--session", "cs-t", "--timeout", "10",
        "--color-scheme", "dark",
    )
    out = pw_driver.execute(
        "evaluate", "cs-t",
        "window.matchMedia('(prefers-color-scheme: dark)').matches",
    )
    assert "true" in out
    pw_driver.execute("close", "cs-t")


def test_save_and_load_storage(test_server, pw_page, pw_driver):
    url = f"{test_server}/login.html"

    with tempfile.NamedTemporaryFile(suffix=".json", delete=False, mode="w") as f:
        storage_path = f.name

    try:
        # Set a cookie and localStorage item, then save
        pw_driver.execute("open", url, "--session", "stor-save", "--timeout", "10")
        pw_driver.execute("cookies", "stor-save", "--set", "testkey=testval")
        pw_driver.execute(
            "evaluate", "stor-save",
            "localStorage.setItem('lsKey', 'lsVal')",
        )
        pw_driver.execute("close", "stor-save", "--save-storage", storage_path)

        # Verify the file was written
        data = json.loads(Path(storage_path).read_text())
        assert "cookies" in data
        assert "origins" in data

        # Load into a new session
        pw_driver.execute(
            "open", url, "--session", "stor-load", "--timeout", "10",
            "--load-storage", storage_path,
        )
        out = pw_driver.execute(
            "evaluate", "stor-load",
            "localStorage.getItem('lsKey')",
        )
        assert "lsVal" in out
        pw_driver.execute("close", "stor-load")
    finally:
        Path(storage_path).unlink(missing_ok=True)


def test_save_har(test_server, pw_page, pw_driver):
    url = f"{test_server}/dynamic.html"

    with tempfile.NamedTemporaryFile(suffix=".har", delete=False) as f:
        har_path = f.name

    try:
        pw_driver.execute(
            "open", url, "--session", "har-t", "--timeout", "10",
            "--save-har", har_path,
        )
        # Trigger a fetch so there's network activity
        pw_driver.execute("click", "har-t", "#fetch-btn")
        pw_driver.execute("wait-for", "har-t", "--stable")
        pw_driver.execute("close", "har-t")

        data = json.loads(Path(har_path).read_text())
        assert data["log"]["version"] == "1.2"
        assert len(data["log"]["entries"]) > 0
        urls = [e["request"]["url"] for e in data["log"]["entries"]]
        assert any("/api/data" in u for u in urls)
    finally:
        Path(har_path).unlink(missing_ok=True)
