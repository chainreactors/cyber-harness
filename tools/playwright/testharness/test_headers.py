"""Compare set-extra-headers command."""
import json


def test_set_extra_headers(test_server, pw_page, pw_driver):
    url = f"{test_server}/api/echo-headers"

    # Real Playwright — set headers via context
    pw_page.set_extra_http_headers({"X-Custom-Test": "pw-val-123"})
    pw_page.goto(url)
    body = pw_page.text_content("body")
    headers = json.loads(body)
    assert headers.get("X-Custom-Test") == "pw-val-123" or headers.get("x-custom-test") == "pw-val-123"

    # aiscan
    pw_driver.execute("open", url, "--session", "hdr-t", "--timeout", "10")
    pw_driver.execute(
        "set-extra-headers", "hdr-t",
        '{"X-Custom-Test":"aiscan-val-456"}'
    )
    pw_driver.execute("reload", "hdr-t")
    out = pw_driver.execute("text-content", "hdr-t", "body")
    assert "aiscan-val-456" in out
    pw_driver.execute("close", "hdr-t")
