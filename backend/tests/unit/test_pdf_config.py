from app.config import Settings


def test_pdf_settings_defaults():
    s = Settings()
    assert s.pdf_engine == "html"
    assert s.pdf_render_timeout_ms == 15000
