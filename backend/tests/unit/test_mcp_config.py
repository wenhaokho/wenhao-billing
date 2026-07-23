from app.config import Settings


def test_mcp_settings_have_secure_defaults_shape():
    s = Settings(
        mcp_confirm_secret="x" * 32,
        mcp_token_secret="y" * 32,
    )
    assert s.mcp_enabled is True
    assert s.mcp_access_token_ttl_seconds == 3600
    assert len(s.mcp_confirm_secret) >= 32
    assert len(s.mcp_token_secret) >= 32
