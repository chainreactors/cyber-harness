"""Compare extraction commands: get-attribute, is-visible, input-value."""


def test_get_attribute(test_server, pw_page, pw_driver):
    url = f"{test_server}/forms.html"

    pw_page.goto(url)
    pw_val = pw_page.get_attribute("#visible-div", "data-custom")
    assert pw_val == "test123"

    pw_driver.execute("open", url, "--session", "attr-test", "--timeout", "10")
    out = pw_driver.execute("get-attribute", "attr-test", "#visible-div", "data-custom")
    assert "test123" in out
    pw_driver.execute("close", "attr-test")


def test_get_attribute_null(test_server, pw_page, pw_driver):
    url = f"{test_server}/forms.html"

    pw_page.goto(url)
    pw_val = pw_page.get_attribute("#visible-div", "nonexistent")
    assert pw_val is None

    pw_driver.execute("open", url, "--session", "attr-null", "--timeout", "10")
    out = pw_driver.execute("get-attribute", "attr-null", "#visible-div", "nonexistent")
    assert "null" in out
    pw_driver.execute("close", "attr-null")


def test_is_visible_hidden(test_server, pw_page, pw_driver):
    url = f"{test_server}/forms.html"

    pw_page.goto(url)
    assert not pw_page.is_visible("#hidden-div")
    assert pw_page.is_visible("#visible-div")

    pw_driver.execute("open", url, "--session", "vis-test", "--timeout", "10")
    hidden = pw_driver.execute("is-visible", "vis-test", "#hidden-div")
    assert "false" in hidden
    visible = pw_driver.execute("is-visible", "vis-test", "#visible-div")
    assert "true" in visible
    pw_driver.execute("close", "vis-test")
