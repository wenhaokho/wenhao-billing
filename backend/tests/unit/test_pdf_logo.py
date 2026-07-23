from types import SimpleNamespace

from app.services.pdf.logo import resolve_logo


def test_passthrough_data_uri():
    biz = SimpleNamespace(logo_url="data:image/png;base64,AAAA")
    assert resolve_logo(biz) == "data:image/png;base64,AAAA"


def test_none_business_uses_default():
    assert resolve_logo(None).startswith("data:image/png;base64,")


def test_blank_logo_uses_default():
    biz = SimpleNamespace(logo_url=None)
    assert resolve_logo(biz).startswith("data:image/png;base64,")
