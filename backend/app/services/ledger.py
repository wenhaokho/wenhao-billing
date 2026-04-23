"""Double-entry ledger posting.

Invariants enforced here:
- Posting refuses any payment whose status is not CLEARED (safe-stop belt-and-suspenders).
- Base-currency balance is always enforced: sum(base_debit - base_credit) == 0.
- Per-currency balance is enforced only for single-currency entries. Cross-currency
  entries balance via an FX gain/loss plug line booked in the base currency; they
  do NOT balance per-raw-currency by construction.
- Reversals create a second entry with swapped debit/credit and wire `reversed_by_entry_id`.
"""

from __future__ import annotations

from datetime import date
from decimal import Decimal
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.models.coa import ChartOfAccount
from app.models.invoice import Invoice
from app.models.journal import JournalEntry, JournalLine
from app.models.payment import Payment
from app.config import get_settings
from app.services import fx

OPERATING_BANK_CODE = "1000"
ACCOUNTS_RECEIVABLE_CODE = "1100"
FX_GAIN_CODE = "4900"
FX_LOSS_CODE = "5900"


class LedgerError(RuntimeError):
    pass


def _account_id(db: Session, code: str) -> int:
    account_id = db.scalar(select(ChartOfAccount.account_id).where(ChartOfAccount.code == code))
    if account_id is None:
        raise LedgerError(f"chart_of_accounts missing code {code}")
    return account_id


def _assert_balanced(entry: JournalEntry) -> None:
    # Base-currency balance is the universal invariant — the FX gain/loss plug
    # line (if any) is what makes a cross-currency entry balance here.
    base_net = sum(
        (line.base_amount_debit - line.base_amount_credit for line in entry.lines),
        Decimal("0"),
    )
    if base_net != Decimal("0"):
        raise LedgerError(f"journal entry unbalanced in base currency: net={base_net}")

    # Per-currency balance is additionally enforced when the entry only touches
    # one raw currency. A multi-currency entry cannot balance per-raw-currency
    # by definition (the plug lives in the base currency) — only base balance
    # is meaningful there.
    currencies = {line.currency for line in entry.lines}
    if len(currencies) > 1:
        return
    by_currency: dict[str, Decimal] = {}
    for line in entry.lines:
        by_currency[line.currency] = by_currency.get(line.currency, Decimal("0")) + (
            line.debit - line.credit
        )
    for currency, net in by_currency.items():
        if net != Decimal("0"):
            raise LedgerError(f"journal entry unbalanced in {currency}: net={net}")


def _build_line(
    db: Session,
    *,
    account_id: int,
    debit: Decimal,
    credit: Decimal,
    currency: str,
    on_date: date,
) -> JournalLine:
    """Construct a JournalLine with base_amount_* and fx_rate populated."""
    conv_debit = fx.convert(db, debit, currency, on_date) if debit > 0 else None
    conv_credit = fx.convert(db, credit, currency, on_date) if credit > 0 else None
    # debit/credit is one-sided (CK on journal_lines), so exactly one conv is set.
    rate = (conv_debit or conv_credit).rate  # type: ignore[union-attr]
    return JournalLine(
        account_id=account_id,
        debit=debit,
        credit=credit,
        currency=currency,
        base_amount_debit=conv_debit.base_amount if conv_debit else Decimal("0"),
        base_amount_credit=conv_credit.base_amount if conv_credit else Decimal("0"),
        fx_rate=rate,
    )


def post_payment_cleared(db: Session, payment: Payment, invoice: Invoice) -> JournalEntry:
    """DR Operating Bank / CR Accounts Receivable for a CLEARED payment.

    Same-currency: AR is relieved for payment.amount in the shared currency.

    Cross-currency: the payment is treated as fully settling the invoice's
    remaining balance. Bank is debited in payment.currency for payment.amount;
    AR is credited in invoice.currency for invoice.balance_due; the base-amount
    difference is booked as realised FX Gain or FX Loss.
    """
    if payment.status != "CLEARED":
        raise LedgerError(
            f"refuse to post ledger for payment {payment.payment_id} "
            f"with status {payment.status!r} — only CLEARED may post"
        )

    on_date = payment.payment_date
    same_currency = payment.currency == invoice.currency

    if same_currency:
        lines = [
            _build_line(
                db,
                account_id=_account_id(db, OPERATING_BANK_CODE),
                debit=payment.amount,
                credit=Decimal("0"),
                currency=payment.currency,
                on_date=on_date,
            ),
            _build_line(
                db,
                account_id=_account_id(db, ACCOUNTS_RECEIVABLE_CODE),
                debit=Decimal("0"),
                credit=payment.amount,
                currency=payment.currency,
                on_date=on_date,
            ),
        ]
    else:
        ar_amount = invoice.balance_due
        if ar_amount <= 0:
            raise LedgerError(
                f"cross-currency settlement requires invoice balance_due > 0 "
                f"(invoice {invoice.invoice_id} balance={ar_amount})"
            )
        bank_line = _build_line(
            db,
            account_id=_account_id(db, OPERATING_BANK_CODE),
            debit=payment.amount,
            credit=Decimal("0"),
            currency=payment.currency,
            on_date=on_date,
        )
        ar_line = _build_line(
            db,
            account_id=_account_id(db, ACCOUNTS_RECEIVABLE_CODE),
            debit=Decimal("0"),
            credit=ar_amount,
            currency=invoice.currency,
            on_date=on_date,
        )
        lines = [bank_line, ar_line]
        # Base-currency plug. bank_base is DR; ar_base is CR. If bank_base <
        # ar_base we received less value than we relieved — an FX loss (DR).
        # If bank_base > ar_base we received more — an FX gain (CR).
        diff = bank_line.base_amount_debit - ar_line.base_amount_credit
        base_ccy = get_settings().base_currency
        if diff < 0:
            lines.append(
                _build_line(
                    db,
                    account_id=_account_id(db, FX_LOSS_CODE),
                    debit=-diff,
                    credit=Decimal("0"),
                    currency=base_ccy,
                    on_date=on_date,
                )
            )
        elif diff > 0:
            lines.append(
                _build_line(
                    db,
                    account_id=_account_id(db, FX_GAIN_CODE),
                    debit=Decimal("0"),
                    credit=diff,
                    currency=base_ccy,
                    on_date=on_date,
                )
            )

    entry = JournalEntry(
        source_type="PAYMENT",
        source_id=payment.payment_id,
        memo=f"Payment {payment.payment_id} cleared against invoice {invoice.invoice_id}",
        lines=lines,
    )
    _assert_balanced(entry)
    db.add(entry)
    db.flush()
    return entry


def post_reversal(db: Session, original_entry_id: UUID, memo: str | None = None) -> JournalEntry:
    original = db.get(JournalEntry, original_entry_id)
    if original is None:
        raise LedgerError(f"entry {original_entry_id} not found")
    if original.reversed_by_entry_id is not None:
        raise LedgerError(f"entry {original_entry_id} already reversed")

    reversal = JournalEntry(
        source_type=original.source_type,
        source_id=original.source_id,
        memo=memo or f"Reversal of {original.entry_id}",
        lines=[
            JournalLine(
                account_id=line.account_id,
                debit=line.credit,
                credit=line.debit,
                currency=line.currency,
                base_amount_debit=line.base_amount_credit,
                base_amount_credit=line.base_amount_debit,
                fx_rate=line.fx_rate,
            )
            for line in original.lines
        ],
    )
    _assert_balanced(reversal)
    db.add(reversal)
    db.flush()
    original.reversed_by_entry_id = reversal.entry_id
    db.flush()
    return reversal
