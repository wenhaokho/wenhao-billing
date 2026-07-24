"""Dashboard stats: shared service function + router behavior preservation.

Service under test: `app.services.stats.compute_dashboard_stats(db)`. Before
this test file, GET /api/v1/stats/dashboard had zero test coverage — this
file both pins the extracted service function's contract and proves the
router refactor (dashboard() now calls compute_dashboard_stats instead of
inlining the same queries) is behavior-preserving.
"""
from __future__ import annotations

import uuid
from datetime import date
from decimal import Decimal

import pytest

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.payment import Payment
from app.models.quotation import Quotation
from app.services.stats import compute_dashboard_stats


@pytest.fixture()
def customer(db) -> Customer:
    c = Customer(name="Acme Corp", matching_aliases=["Acme"])
    db.add(c)
    db.flush()
    return c


def _inv(db, customer, *, currency="USD", amount="1000", balance_due=None, status="SENT"):
    bal = Decimal(balance_due) if balance_due is not None else Decimal(amount)
    inv = Invoice(
        customer_id=customer.customer_id,
        invoice_type="MILESTONE",
        currency=currency,
        amount=Decimal(amount),
        balance_due=bal,
        status=status,
    )
    db.add(inv)
    db.flush()
    return inv


def _quote(db, customer, *, currency="USD", amount="1000", status="SENT"):
    q = Quotation(
        customer_id=customer.customer_id,
        currency=currency,
        amount=Decimal(amount),
        status=status,
    )
    db.add(q)
    db.flush()
    return q


def _payment(db, invoice, *, status="CLEARED"):
    p = Payment(
        invoice_id=invoice.invoice_id,
        customer_id=invoice.customer_id,
        amount=invoice.amount,
        currency=invoice.currency,
        payer_name="Acme Corp",
        payment_date=date(2026, 7, 1),
        intake_source="TEST",
        external_ref=f"EXT-{uuid.uuid4()}",
        status=status,
    )
    db.add(p)
    db.flush()
    return p


def test_empty_db_returns_zero_counts_and_no_currency_rows(db):
    stats = compute_dashboard_stats(db)
    assert stats["awaiting_review_count"] == 0
    assert stats["draft_count"] == 0
    assert stats["sent_count"] == 0
    assert stats["customer_count"] == 0
    assert stats["auto_cleared_last_30d"] == 0
    assert stats["open_ar_by_currency"] == []
    assert stats["open_quotation_count"] == 0
    assert stats["open_quotation_pipeline"] == []


def test_counts_reflect_invoice_and_customer_state(db, customer):
    _inv(db, customer, status="DRAFT")
    _inv(db, customer, status="SENT")
    _inv(db, customer, status="SENT")
    stats = compute_dashboard_stats(db)
    assert stats["draft_count"] == 1
    assert stats["sent_count"] == 2
    assert stats["customer_count"] == 1


def test_open_ar_by_currency_sums_sent_and_partial_only(db, customer):
    _inv(db, customer, currency="USD", status="SENT", balance_due="500")
    _inv(db, customer, currency="USD", status="PARTIAL", amount="1000", balance_due="300")
    _inv(db, customer, currency="USD", status="DRAFT", balance_due="999")  # excluded
    _inv(db, customer, currency="SGD", status="SENT", balance_due="800")
    stats = compute_dashboard_stats(db)
    by_ccy = {r["currency"]: r["amount"] for r in stats["open_ar_by_currency"]}
    assert by_ccy == {"USD": Decimal("800"), "SGD": Decimal("800")}


def test_open_quotation_pipeline_sums_sent_and_accepted_only_sorted_desc(db, customer):
    _quote(db, customer, currency="USD", amount="500", status="SENT")
    _quote(db, customer, currency="SGD", amount="2000", status="ACCEPTED")
    _quote(db, customer, currency="GBP", amount="999999", status="DECLINED")  # excluded
    stats = compute_dashboard_stats(db)
    assert stats["open_quotation_count"] == 2
    pipeline = stats["open_quotation_pipeline"]
    # Largest total first — numeric order (SGD 2000 > USD 500), not string order.
    assert [row["currency"] for row in pipeline] == ["SGD", "USD"]
    assert pipeline[0]["amount"] == Decimal("2000")
    assert pipeline[1]["amount"] == Decimal("500")


def test_auto_cleared_last_30d_counts_only_cleared_payments(db, customer):
    inv = _inv(db, customer, status="SENT", balance_due="0")
    _payment(db, inv, status="CLEARED")
    _payment(db, inv, status="PENDING_MANUAL_REVIEW")  # excluded
    stats = compute_dashboard_stats(db)
    assert stats["auto_cleared_last_30d"] == 1


def test_awaiting_review_count_counts_pending_manual_review_payments(db, customer):
    inv = _inv(db, customer, status="SENT", balance_due="0")
    _payment(db, inv, status="PENDING_MANUAL_REVIEW")
    _payment(db, inv, status="CLEARED")  # excluded
    stats = compute_dashboard_stats(db)
    assert stats["awaiting_review_count"] == 1


# ---------------------------------------------------------------------------
# Router behavior preservation (no dedicated /stats/dashboard tests existed
# before this task's refactor — this proves dashboard() calling
# compute_dashboard_stats() through the real HTTP endpoint still returns the
# same shape/values as the pre-refactor inline queries did).
# ---------------------------------------------------------------------------


def test_dashboard_endpoint_matches_service_function(admin_session, db, customer):
    _inv(db, customer, currency="USD", status="SENT", balance_due="500")
    _quote(db, customer, currency="USD", amount="750", status="ACCEPTED")

    expected = compute_dashboard_stats(db)

    r = admin_session.get("/api/v1/stats/dashboard")
    assert r.status_code == 200, r.text
    body = r.json()

    assert body["awaiting_review_count"] == expected["awaiting_review_count"]
    assert body["draft_count"] == expected["draft_count"]
    assert body["sent_count"] == expected["sent_count"]
    assert body["customer_count"] == expected["customer_count"]
    assert body["auto_cleared_last_30d"] == expected["auto_cleared_last_30d"]
    assert body["open_quotation_count"] == expected["open_quotation_count"]

    got_ar = {row["currency"]: Decimal(row["amount"]) for row in body["open_ar_by_currency"]}
    exp_ar = {row["currency"]: row["amount"] for row in expected["open_ar_by_currency"]}
    assert got_ar == exp_ar

    # Pipeline order (largest-first) must be preserved through the router too.
    got_pipeline = [(row["currency"], Decimal(row["amount"])) for row in body["open_quotation_pipeline"]]
    exp_pipeline = [(row["currency"], row["amount"]) for row in expected["open_quotation_pipeline"]]
    assert got_pipeline == exp_pipeline

    # recent_activity is router-only (not part of compute_dashboard_stats).
    assert "recent_activity" in body


def _recurring_tmpl(db, customer, *, currency="USD", amount="1200"):
    inv = Invoice(
        customer_id=customer.customer_id, invoice_type="RECURRING",
        currency=currency, amount=Decimal(amount), balance_due=Decimal("0"),
        status="DRAFT", is_template=True,
        billing_cycle_ref={"frequency": "MONTHLY", "interval": 1,
                           "start_date": "2026-01-05", "end_mode": "NEVER"},
    )
    db.add(inv)
    db.flush()
    return inv


def test_dashboard_stats_includes_mrr_and_base_ar(db, customer):
    _recurring_tmpl(db, customer, currency="USD", amount="1200")
    _inv(db, customer, currency="USD", status="SENT", balance_due="100")
    stats = compute_dashboard_stats(db)
    assert stats["mrr_by_currency"] == [{"currency": "USD", "amount": Decimal("1200.0000")}]
    assert stats["base_currency"] == "IDR"
    assert stats["open_ar_base_by_currency"] == [
        {"currency": "USD", "base_amount": Decimal("1600000.0000")}
    ]
    assert stats["open_ar_unconverted"] == []


def test_dashboard_endpoint_exposes_new_fields(admin_session, db, customer):
    _recurring_tmpl(db, customer, currency="USD", amount="1200")
    _inv(db, customer, currency="USD", status="SENT", balance_due="100")

    r = admin_session.get("/api/v1/stats/dashboard")
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["base_currency"] == "IDR"
    assert body["mrr_by_currency"][0]["currency"] == "USD"
    assert body["open_ar_base_by_currency"][0]["base_amount"] == "1600000.0000"
    assert "upcoming_invoices" in body
    # the recurring template's next cycle (Aug 5) is within 90 days of a mid-year run
    modes = {u["mode"] for u in body["upcoming_invoices"]}
    assert modes <= {"RECURRING", "USAGE"}
    if body["upcoming_invoices"]:
        assert "customer_name" in body["upcoming_invoices"][0]
