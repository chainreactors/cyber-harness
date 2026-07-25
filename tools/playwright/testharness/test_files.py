"""Compare set-input-files command."""
import tempfile
from pathlib import Path


def test_set_input_files(test_server, pw_page, pw_driver):
    url = f"{test_server}/forms.html"

    # Create a temp file to upload
    with tempfile.NamedTemporaryFile(suffix=".txt", delete=False, mode="w") as f:
        f.write("test upload content")
        tmp_path = f.name

    try:
        # Real Playwright
        pw_page.goto(url)
        pw_page.set_input_files("#upload", tmp_path)
        file_name = pw_page.evaluate(
            "document.getElementById('upload').files[0]?.name"
        )
        assert Path(tmp_path).name in file_name

        # aiscan
        pw_driver.execute("open", url, "--session", "file-t", "--timeout", "10")
        pw_driver.execute("set-input-files", "file-t", "#upload", tmp_path)
        out = pw_driver.execute(
            "evaluate", "file-t",
            "document.getElementById('upload').files[0]?.name"
        )
        assert Path(tmp_path).name in out
        pw_driver.execute("close", "file-t")
    finally:
        Path(tmp_path).unlink(missing_ok=True)
