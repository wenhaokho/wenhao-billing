from app.config import Settings


def test_pdf_settings_defaults():
    s = Settings()
    assert s.pdf_engine == "html"
