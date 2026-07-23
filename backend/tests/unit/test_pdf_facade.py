import app.services.pdf as pdf


def test_falls_back_to_reportlab_on_engine_error(monkeypatch):
    monkeypatch.setattr(pdf, "html_to_pdf", lambda html: (_ for _ in ()).throw(RuntimeError("boom")))
    monkeypatch.setattr(pdf, "render_invoice_html", lambda *a, **k: "<html></html>")
    called = {}
    monkeypatch.setattr(pdf, "_reportlab_invoice", lambda inv, cust, biz=None: (called.setdefault("hit", True) and b"%PDF-RL"))
    out = pdf.render_invoice_pdf(object(), object(), None)
    assert called.get("hit") is True
    assert out == b"%PDF-RL"


def test_engine_reportlab_skips_html(monkeypatch):
    from app.config import get_settings
    get_settings.cache_clear()
    monkeypatch.setenv("PDF_ENGINE", "reportlab")
    monkeypatch.setattr(pdf, "render_invoice_html",
                        lambda *a, **k: (_ for _ in ()).throw(AssertionError("HTML path must not run")))
    monkeypatch.setattr(pdf, "_reportlab_invoice", lambda inv, cust, biz=None: b"%PDF-RL")
    out = pdf.render_invoice_pdf(object(), object(), None)
    assert out == b"%PDF-RL"
    get_settings.cache_clear()


def test_html_path_used_on_success(monkeypatch):
    from app.config import get_settings
    get_settings.cache_clear()
    monkeypatch.setattr(pdf, "render_invoice_html", lambda *a, **k: "<html></html>")
    monkeypatch.setattr(pdf, "html_to_pdf", lambda html: b"%PDF-HTML")
    out = pdf.render_invoice_pdf(object(), object(), None)
    assert out == b"%PDF-HTML"
