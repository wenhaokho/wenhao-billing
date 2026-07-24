from app.config import Settings, get_settings


def test_resend_settings_defaults():
    s = Settings()
    assert s.resend_api_key is None
    assert s.resend_from_email == "noreply@wenhao.id"
    assert s.resend_from_name == "Wenhao Dev Billing"
    assert s.resend_base_url == "https://api.resend.com"
    assert s.resend_timeout_seconds == 15.0


def test_resend_api_key_from_env(monkeypatch):
    monkeypatch.setenv("RESEND_API_KEY", "re_test_123")
    get_settings.cache_clear()
    try:
        assert get_settings().resend_api_key == "re_test_123"
    finally:
        get_settings.cache_clear()
