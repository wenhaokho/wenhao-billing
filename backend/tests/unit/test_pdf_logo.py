import base64
from types import SimpleNamespace

from app.services.pdf.assets import default_logo_data_uri
from app.services.pdf.logo import resolve_logo


def test_passthrough_data_uri():
    biz = SimpleNamespace(logo_url="data:image/png;base64,AAAA")
    assert resolve_logo(biz) == "data:image/png;base64,AAAA"


def test_none_business_uses_default():
    assert resolve_logo(None).startswith("data:image/png;base64,")


def test_blank_logo_uses_default():
    biz = SimpleNamespace(logo_url=None)
    assert resolve_logo(biz).startswith("data:image/png;base64,")


def test_http_fetch_success(monkeypatch):
    class FakeResponse:
        content = b"PNGDATA"
        headers = {"content-type": "image/png"}

        def raise_for_status(self):
            pass

    monkeypatch.setattr("app.services.pdf.logo.httpx.get", lambda *a, **k: FakeResponse())

    biz = SimpleNamespace(logo_url="https://example.com/logo.png")
    expected = "data:image/png;base64," + base64.b64encode(b"PNGDATA").decode()
    assert resolve_logo(biz) == expected


def test_http_fetch_failure_falls_back_to_default(monkeypatch):
    def fake_get(*a, **k):
        raise RuntimeError("boom")

    monkeypatch.setattr("app.services.pdf.logo.httpx.get", fake_get)

    biz = SimpleNamespace(logo_url="https://example.com/logo.png")
    assert resolve_logo(biz) == default_logo_data_uri()


def test_http_fetch_non_image_mime_falls_back_to_png(monkeypatch):
    class FakeResponse:
        content = b"<html>not an image</html>"
        headers = {"content-type": "text/html"}

        def raise_for_status(self):
            pass

    monkeypatch.setattr("app.services.pdf.logo.httpx.get", lambda *a, **k: FakeResponse())

    biz = SimpleNamespace(logo_url="https://example.com/logo.png")
    expected = "data:image/png;base64," + base64.b64encode(b"<html>not an image</html>").decode()
    assert resolve_logo(biz) == expected
