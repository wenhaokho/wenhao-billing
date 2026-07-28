"""Repair malformed recurring-invoice cycle keys and de-duplicate cycles.

Background
----------
`trigger_recurring_cycle` used to accept any caller-supplied `cycle_key`.
Values like ``"2026-03"`` (year-month, no day) slipped in via manual/MCP
triggers. Because ``"2026-03" != "2026-03-20"``, the (template, cycle_key)
idempotency guard didn't dedupe them, so a single cycle ended up with two
DRAFT invoices — a double-billing risk — and the malformed key made the
"Previous" column show the wrong date.

The trigger now validates keys (see `invoicing.trigger_recurring_cycle`), so
no *new* bad rows appear. This script cleans up the rows that already exist.

What it does
------------
For every generated recurring child (``is_template = False``):

  * Determine the child's true cycle-start from its schedule and issue_date.
  * Group children by (template, true cycle-start).
  * Per group:
      - one child, malformed key  -> **FIX** the key to the ISO cycle-start.
      - several children (duplicate cycle) -> **KEEP** one, **DELETE** the
        rest. A non-DRAFT invoice is never deleted (kept as the survivor, or
        flagged for manual handling if two non-DRAFT collide).
  * Children whose true cycle can't be determined are **SKIPPED** (reported).

Safety
------
Dry-run by default: prints the full plan and changes nothing. Pass ``--apply``
to execute inside a single transaction. Deletion goes through
``invoicing.delete_invoice`` (DRAFT-only).

Usage
-----
    python -m scripts.fix_recurring_cycle_keys            # dry-run (default)
    python -m scripts.fix_recurring_cycle_keys --apply    # execute
"""

from __future__ import annotations

import argparse
from dataclasses import dataclass
from datetime import date
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.db.session import SessionLocal
from app.models.customer import Customer
from app.models.invoice import Invoice
from app.services import invoicing
from app.services.recurring_schedule import (
    Schedule,
    ScheduleError,
    current_cycle,
    is_cycle_start,
    parse_schedule,
)

FIX = "FIX-KEY"
KEEP = "KEEP"
DELETE = "DELETE"
SKIP = "SKIP"


@dataclass
class Action:
    kind: str  # FIX / KEEP / DELETE / SKIP
    invoice_id: UUID
    invoice_number: str | None
    customer: str | None
    issue_date: date | None
    status: str
    old_key: str | None
    new_key: str | None  # target key for FIX; None otherwise
    reason: str = ""


def _valid_key(schedule: Schedule, key: str | None) -> bool:
    if not key:
        return False
    try:
        d = date.fromisoformat(key)
    except ValueError:
        return False
    return is_cycle_start(schedule, d)


def _true_cycle_start(schedule: Schedule, child: Invoice) -> date | None:
    """Best-effort true cycle-start for a child: the cycle its issue_date
    falls in. Returns None if it can't be resolved (e.g. no issue_date)."""
    if child.issue_date is None:
        return None
    cycle = current_cycle(schedule, child.issue_date)
    return cycle.start if cycle else None


def build_plan(db: Session) -> list[Action]:
    children = list(
        db.scalars(
            select(Invoice)
            .where(Invoice.is_template.is_(False))
            .where(Invoice.invoice_type == "RECURRING")
            .order_by(Invoice.created_at.asc())
        )
    )
    cust_names: dict[UUID, str] = {
        c.customer_id: c.name for c in db.scalars(select(Customer))
    }
    sched_cache: dict[UUID, Schedule | None] = {}

    def schedule_for(template_id: UUID | None) -> Schedule | None:
        if template_id is None:
            return None
        if template_id not in sched_cache:
            tpl = db.get(Invoice, template_id)
            try:
                sched_cache[template_id] = (
                    parse_schedule(tpl.billing_cycle_ref) if tpl else None
                )
            except ScheduleError:
                sched_cache[template_id] = None
        return sched_cache[template_id]

    # Bucket children by (template, true cycle-start). Unresolvable -> SKIP.
    groups: dict[tuple[UUID, date], list[tuple[Invoice, Schedule, str | None]]] = {}
    actions: list[Action] = []
    for c in children:
        ref = c.billing_cycle_ref or {}
        old_key = ref.get("cycle_key")
        tpl_id = ref.get("template_invoice_id")
        tpl_uuid = UUID(tpl_id) if tpl_id else None
        schedule = schedule_for(tpl_uuid)
        if schedule is None or tpl_uuid is None:
            actions.append(_mk_action(SKIP, c, cust_names, old_key, None,
                                      "template schedule unresolved"))
            continue
        true_start = _true_cycle_start(schedule, c)
        if true_start is None:
            actions.append(_mk_action(SKIP, c, cust_names, old_key, None,
                                      "cannot resolve cycle from issue_date"))
            continue
        groups.setdefault((tpl_uuid, true_start), []).append((c, schedule, old_key))

    for (tpl_uuid, true_start), members in groups.items():
        target = true_start.isoformat()
        # Keeper preference: never plan to delete a non-DRAFT; then prefer an
        # already-valid key; then the earliest created (list is created_at asc).
        def sort_key(m: tuple[Invoice, Schedule, str | None]) -> tuple:
            inv, sched, key = m
            return (inv.status == "DRAFT", not _valid_key(sched, key))

        ordered = sorted(members, key=sort_key)
        keeper = ordered[0]
        for inv, sched, key in members:
            if inv is keeper[0]:
                if key == target:
                    actions.append(_mk_action(KEEP, inv, cust_names, key, None,
                                              "canonical key"))
                else:
                    actions.append(_mk_action(FIX, inv, cust_names, key, target,
                                              "rewrite to ISO cycle-start"))
            elif inv.status == "DRAFT":
                actions.append(_mk_action(DELETE, inv, cust_names, key, None,
                                          f"duplicate of cycle {target}"))
            else:
                actions.append(_mk_action(SKIP, inv, cust_names, key, None,
                                          f"non-DRAFT duplicate of {target} — "
                                          "resolve manually"))
    return actions


def _mk_action(kind, inv, cust_names, old_key, new_key, reason) -> Action:
    return Action(
        kind=kind,
        invoice_id=inv.invoice_id,
        invoice_number=inv.invoice_number,
        customer=cust_names.get(inv.customer_id),
        issue_date=inv.issue_date,
        status=inv.status,
        old_key=old_key,
        new_key=new_key,
        reason=reason,
    )


def apply_plan(db: Session, actions: list[Action]) -> None:
    for a in actions:
        if a.kind == FIX:
            inv = db.get(Invoice, a.invoice_id)
            ref = dict(inv.billing_cycle_ref or {})
            ref["cycle_key"] = a.new_key
            inv.billing_cycle_ref = ref
            db.flush()
        elif a.kind == DELETE:
            invoicing.delete_invoice(db, a.invoice_id)


def _print_plan(actions: list[Action]) -> None:
    order = {FIX: 0, DELETE: 1, SKIP: 2, KEEP: 3}
    for a in sorted(actions, key=lambda x: (order.get(x.kind, 9), x.customer or "")):
        old = a.old_key or "—"
        arrow = f" -> {a.new_key}" if a.new_key else ""
        print(
            f"[{a.kind:8}] {(a.customer or '?'):28} "
            f"{a.invoice_number or '(no #)':11} "
            f"issue={a.issue_date} status={a.status:8} "
            f"key={old!r}{arrow}  ({a.reason})"
        )
    counts = {k: sum(1 for a in actions if a.kind == k) for k in (FIX, DELETE, SKIP, KEEP)}
    print(
        f"\nSummary: {counts[FIX]} to fix, {counts[DELETE]} to delete, "
        f"{counts[SKIP]} skipped, {counts[KEEP]} unchanged."
    )


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--apply", action="store_true",
                    help="execute the plan (default: dry-run)")
    args = ap.parse_args()

    with SessionLocal() as db:
        actions = build_plan(db)
        _print_plan(actions)
        if not args.apply:
            print("\nDry-run only. Re-run with --apply to execute.")
            return
        apply_plan(db, actions)
        db.commit()
        print("\nApplied.")


if __name__ == "__main__":
    main()
