"""Customer read tools + the ambiguity-stop resolver.

Covers:
- list_customers: name-substring filter, default ordering by name.
- get_customer: found vs. not-found (returns {}).
- resolve_customer: the KEY ambiguity-stop primitive later write tools
  depend on — unique match, ambiguous (0 or >1 matches) -> candidates,
  no other action taken.

FastMCP 3.4.3's `@mcp.tool` returns the plain function (no `.fn`) — tools
are called directly, per app/mcp/tools/read_misc.py's established pattern.
"""
from __future__ import annotations

from app.mcp.tools.read_customers import get_customer, list_customers, resolve_customer


def test_resolve_unique_match(mcp_tool_db, seed_customer):
    # seed_customer: inserts customer name="Acme Pte Ltd"; returns its id.
    out = resolve_customer(query="Acme")
    assert out == {"customer_id": str(seed_customer)}


def test_resolve_ambiguous_returns_candidates(mcp_tool_db, seed_two_acme):
    out = resolve_customer(query="Acme")
    assert "candidates" in out
    assert len(out["candidates"]) == 2
    assert "customer_id" not in out  # took NO action
    ids = {c["customer_id"] for c in out["candidates"]}
    assert ids == {str(cid) for cid in seed_two_acme}
    for c in out["candidates"]:
        assert set(c.keys()) == {"customer_id", "name"}


def test_resolve_no_match_returns_empty_candidates(mcp_tool_db, seed_customer):
    out = resolve_customer(query="Nonexistent Corp")
    assert out == {"candidates": []}


def test_list_customers_filters_by_name_substring(mcp_tool_db, seed_two_acme, db):
    from app.models.customer import Customer

    other = Customer(name="Zephyr Trading", matching_aliases=[])
    db.add(other)
    db.flush()

    out = list_customers(query="Acme")
    assert len(out) == 2
    assert all("Acme" in c["name"] for c in out)


def test_list_customers_no_query_returns_all(mcp_tool_db, seed_customer, db):
    out = list_customers()
    assert len(out) == 1
    assert out[0]["customer_id"] == str(seed_customer)
    assert out[0]["name"] == "Acme Pte Ltd"


def test_get_customer_found(mcp_tool_db, seed_customer):
    out = get_customer(customer_id=str(seed_customer))
    assert out["customer_id"] == str(seed_customer)
    assert out["name"] == "Acme Pte Ltd"


def test_get_customer_not_found_returns_empty_dict(mcp_tool_db, db):
    import uuid

    out = get_customer(customer_id=str(uuid.uuid4()))
    assert out == {}
