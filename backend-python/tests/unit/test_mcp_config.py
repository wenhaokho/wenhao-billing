from app.config import Settings
from app.mcp.server import _transport_allowlist


def test_mcp_settings_have_secure_defaults_shape():
    s = Settings(
        mcp_confirm_secret="x" * 32,
        mcp_token_secret="y" * 32,
    )
    assert s.mcp_enabled is True
    assert s.mcp_access_token_ttl_seconds == 3600
    assert len(s.mcp_confirm_secret) >= 32
    assert len(s.mcp_token_secret) >= 32


def test_transport_allowlist_public_https_host():
    """A production base URL must allow-list the bare public host (the Host
    header the domain sends) so FastMCP's DNS-rebinding guard doesn't 421 it."""
    hosts, origins = _transport_allowlist("https://billing.wenhao.id")
    assert hosts == ["billing.wenhao.id"]  # hostname == netloc when no port
    assert origins == ["https://billing.wenhao.id"]


def test_transport_allowlist_includes_host_with_port():
    """When the base URL carries a port, both the bare host and host:port forms
    are allow-listed so either Host header shape passes the guard."""
    hosts, origins = _transport_allowlist("http://localhost:8000")
    assert set(hosts) == {"localhost", "localhost:8000"}
    assert origins == ["http://localhost:8000"]
