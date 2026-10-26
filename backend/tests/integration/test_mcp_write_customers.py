"""Customer write-tool tests (withholding defaults)."""
from __future__ import annotations

from decimal import Decimal

from app.mcp.tools.write_customers import create_customer, update_customer


def test_customer_default_withholding_roundtrip(mcp_tool_db):
    created = create_customer(
        name="Withholder Co",
        default_wht_type="PPH23",
        default_wht_gross_up=True,
    )
    assert created["default_wht_type"] == "PPH23"
    assert Decimal(created["default_wht_rate"]) == Decimal(2)
    assert created["default_wht_gross_up"] is True

    out = update_customer(
        customer_id=created["customer_id"],
        changes={"default_wht_type": "NONE"},
    )
    assert out["default_wht_type"] == "NONE"
    assert out["default_wht_rate"] is None
    assert out["default_wht_gross_up"] is False
