import pytest

from app.services.pdf.engine import _fetcher, html_to_pdf


def test_html_to_pdf_returns_pdf_bytes():
    out = html_to_pdf("<html><body><h1>Hello</h1></body></html>")
    assert out[:5] == b"%PDF-"
    assert len(out) > 500


@pytest.mark.parametrize(
    "url", ["file:///etc/passwd", "http://169.254.169.254/", "https://x.test/a.png"]
)
def test_fetcher_refuses_non_embedded_urls(url):
    with pytest.raises(ValueError):
        _fetcher().fetch(url)


def test_fetcher_allows_data_uris():
    assert _fetcher().fetch("data:text/plain;base64,aGk=").read() == b"hi"


def test_external_reference_is_skipped_not_fatal():
    # WeasyPrint logs a failed fetch and renders without the resource.
    out = html_to_pdf('<html><body><img src="file:///etc/passwd"><p>x</p></body></html>')
    assert out[:5] == b"%PDF-"
