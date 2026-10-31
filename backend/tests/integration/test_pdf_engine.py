import pytest

pw = pytest.importorskip("playwright.sync_api")


@pytest.fixture(scope="session")
def chromium_available() -> None:
    """Skip when Playwright's Chromium isn't installed.

    Probed lazily in a session fixture rather than at import time: a
    module-level probe launched a browser during *collection* of every run
    (and of every xdist worker), costing ~1.7s even for `--collect-only`.
    """
    try:
        with pw.sync_playwright() as p:
            b = p.chromium.launch(args=["--no-sandbox"])
            b.close()
    except Exception as exc:  # noqa: BLE001 - any launch failure means "not available"
        pytest.skip(f"Chromium not installed (run: playwright install chromium): {exc}")


def test_html_to_pdf_returns_pdf_bytes(chromium_available):
    from app.services.pdf.engine import html_to_pdf
    out = html_to_pdf("<html><body><h1>Hello</h1></body></html>")
    assert out[:5] == b"%PDF-"
    assert len(out) > 500
