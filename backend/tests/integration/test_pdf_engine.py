import pytest

pw = pytest.importorskip("playwright.sync_api")


def _chromium_available() -> bool:
    try:
        with pw.sync_playwright() as p:
            b = p.chromium.launch(args=["--no-sandbox"])
            b.close()
        return True
    except Exception:
        return False


pytestmark = pytest.mark.skipif(
    not _chromium_available(), reason="Chromium not installed (run: playwright install chromium)"
)


def test_html_to_pdf_returns_pdf_bytes():
    from app.services.pdf.engine import html_to_pdf
    out = html_to_pdf("<html><body><h1>Hello</h1></body></html>")
    assert out[:5] == b"%PDF-"
    assert len(out) > 500
