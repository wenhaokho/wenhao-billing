from app.config import Settings


def test_pdf_settings_defaults():
    s = Settings()
    assert s.pdf_engine == "html"
    assert s.pdf_render_timeout_ms == 15000


def test_business_profile_has_payment_instructions_field():
    from app.schemas.business_profile import BusinessProfileUpdate
    m = BusinessProfileUpdate(payment_instructions="DBS 012-345678-9")
    assert m.payment_instructions == "DBS 012-345678-9"
