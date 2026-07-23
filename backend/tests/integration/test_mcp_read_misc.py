"""Read tools: business profile + dashboard stats.

Covers:
- get_business_profile: returns the singleton BusinessProfile row (id=1,
  seeded by migration 0010_business_profile) as a plain, JSON-safe dict.
- get_stats: returns the same dashboard summary figures as
  GET /api/v1/stats/dashboard, as a plain dict (counts + per-currency
  totals), safe on an empty database.

Note: FastMCP 3.4.3's `@mcp.tool` decorator returns the original function
unchanged (no `.fn` wrapper attribute — verified via
`type(get_business_profile)` inside the backend container, which reports
plain `function`). So tools are called directly, not via `.fn()`.
"""
from __future__ import annotations

from app.mcp.tools.read_misc import get_business_profile, get_stats


def test_get_business_profile_returns_dict(mcp_tool_db, seed_business_profile):
    # seed_business_profile: populates the singleton BusinessProfile row in `db`.
    result = get_business_profile()
    assert isinstance(result, dict)
    assert "name" in result or "legal_name" in result
    assert result["name"] == "Wenhao Studio"
    assert result["contact_email"] == "hello@wenhao.example"
    # Decimal/UUID/datetime fields (if any) must already be JSON-safe strings.
    assert isinstance(result["updated_at"], str)


def test_get_stats_returns_dict_with_expected_keys(mcp_tool_db, db):
    result = get_stats()
    assert isinstance(result, dict)
    for key in (
        "awaiting_review_count",
        "draft_count",
        "sent_count",
        "customer_count",
        "auto_cleared_last_30d",
        "open_ar_by_currency",
        "open_quotation_count",
        "open_quotation_pipeline",
    ):
        assert key in result

    # Zero-state on an empty (rolled-back) test database.
    assert result["draft_count"] == 0
    assert result["customer_count"] == 0
    assert result["open_ar_by_currency"] == []
    assert result["open_quotation_pipeline"] == []
