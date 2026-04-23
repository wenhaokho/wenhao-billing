"""Accounting reference data: chart of accounts CRUD.

Journal entry create/list is intentionally scoped to another module;
this is the reference-data face of accounting.
"""
from datetime import date, datetime, time
from decimal import Decimal

from fastapi import APIRouter, Depends, HTTPException, Query
from sqlalchemy import func, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from app.api.deps import current_admin
from app.config import get_settings
from app.db.session import get_db
from app.models.bill import Bill
from app.models.coa import ChartOfAccount
from app.models.customer import Customer
from app.models.fx import FxRate
from app.models.invoice import Invoice
from app.models.journal import JournalEntry, JournalLine
from app.models.user import User
from app.models.vendor import Vendor
from app.schemas.fx import FxRateCreate, FxRateOut
from app.schemas.item import AccountCreate, AccountOut, AccountUpdate
from app.schemas.reports import (
    AgingBuckets,
    AgingReport,
    AgingRow,
    BalanceSheetGroup,
    BalanceSheetLine,
    BalanceSheetReport,
    FxRevaluationLine,
    FxRevaluationReport,
    TrialBalanceReport,
    TrialBalanceRow,
)

router = APIRouter(prefix="/accounting", tags=["accounting"])


@router.get("/chart-of-accounts", response_model=list[AccountOut])
def chart_of_accounts(
    active: bool | None = None,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[ChartOfAccount]:
    stmt = select(ChartOfAccount).order_by(ChartOfAccount.code.asc())
    if active is not None:
        stmt = stmt.where(ChartOfAccount.active == active)
    return list(db.scalars(stmt))


@router.post(
    "/chart-of-accounts", response_model=AccountOut, status_code=201
)
def create_account(
    payload: AccountCreate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> ChartOfAccount:
    account = ChartOfAccount(**payload.model_dump())
    db.add(account)
    try:
        db.commit()
    except IntegrityError:
        db.rollback()
        raise HTTPException(status_code=409, detail="account code already exists")
    db.refresh(account)
    return account


@router.get("/chart-of-accounts/{account_id}", response_model=AccountOut)
def get_account(
    account_id: int,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> ChartOfAccount:
    account = db.get(ChartOfAccount, account_id)
    if account is None:
        raise HTTPException(status_code=404, detail="account not found")
    return account


@router.patch("/chart-of-accounts/{account_id}", response_model=AccountOut)
def update_account(
    account_id: int,
    payload: AccountUpdate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> ChartOfAccount:
    account = db.get(ChartOfAccount, account_id)
    if account is None:
        raise HTTPException(status_code=404, detail="account not found")
    for k, v in payload.model_dump(exclude_unset=True).items():
        setattr(account, k, v)
    try:
        db.commit()
    except IntegrityError:
        db.rollback()
        raise HTTPException(status_code=409, detail="account code already exists")
    db.refresh(account)
    return account


@router.delete("/chart-of-accounts/{account_id}", response_model=AccountOut)
def deactivate_account(
    account_id: int,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> ChartOfAccount:
    account = db.get(ChartOfAccount, account_id)
    if account is None:
        raise HTTPException(status_code=404, detail="account not found")
    account.active = False
    db.commit()
    db.refresh(account)
    return account


# ---------------------------------------------------------------------------
# FX rates
# ---------------------------------------------------------------------------


@router.get("/base-currency")
def base_currency(_: User = Depends(current_admin)) -> dict[str, str]:
    return {"base_currency": get_settings().base_currency}


@router.get("/fx-rates", response_model=list[FxRateOut])
def list_fx_rates(
    from_currency: str | None = Query(default=None, min_length=3, max_length=3),
    to_currency: str | None = Query(default=None, min_length=3, max_length=3),
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[FxRate]:
    stmt = select(FxRate).order_by(FxRate.as_of_date.desc(), FxRate.rate_id.desc())
    if from_currency:
        stmt = stmt.where(FxRate.from_currency == from_currency.upper())
    if to_currency:
        stmt = stmt.where(FxRate.to_currency == to_currency.upper())
    return list(db.scalars(stmt))


@router.post("/fx-rates", response_model=FxRateOut, status_code=201)
def create_fx_rate(
    payload: FxRateCreate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> FxRate:
    rate = FxRate(
        from_currency=payload.from_currency.upper(),
        to_currency=payload.to_currency.upper(),
        rate=payload.rate,
        as_of_date=payload.as_of_date,
        source=payload.source,
    )
    db.add(rate)
    try:
        db.commit()
    except IntegrityError:
        db.rollback()
        raise HTTPException(
            status_code=409,
            detail="fx rate for this currency pair and date already exists",
        )
    db.refresh(rate)
    return rate


@router.get("/fx-rates/latest", response_model=FxRateOut)
def latest_fx_rate(
    from_currency: str = Query(min_length=3, max_length=3),
    to_currency: str | None = Query(default=None, min_length=3, max_length=3),
    on_date: date | None = Query(default=None),
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> FxRate:
    base = (to_currency or get_settings().base_currency).upper()
    stmt = (
        select(FxRate)
        .where(FxRate.from_currency == from_currency.upper())
        .where(FxRate.to_currency == base)
        .order_by(FxRate.as_of_date.desc(), FxRate.rate_id.desc())
        .limit(1)
    )
    if on_date is not None:
        stmt = stmt.where(FxRate.as_of_date <= on_date)
    rate = db.scalar(stmt)
    if rate is None:
        raise HTTPException(status_code=404, detail="no rate found")
    return rate


@router.delete("/fx-rates/{rate_id}", status_code=204)
def delete_fx_rate(
    rate_id: int,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> None:
    rate = db.get(FxRate, rate_id)
    if rate is None:
        raise HTTPException(status_code=404, detail="rate not found")
    db.delete(rate)
    db.commit()


# ---------------------------------------------------------------------------
# Reports
# ---------------------------------------------------------------------------


def _as_of_cutoff(as_of: date) -> datetime:
    # Inclusive end-of-day. journal_entries.posted_at is a timestamp.
    return datetime.combine(as_of, time.max)


@router.get("/reports/trial-balance", response_model=TrialBalanceReport)
def trial_balance(
    as_of: date = Query(default_factory=date.today),
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> TrialBalanceReport:
    cutoff = _as_of_cutoff(as_of)
    stmt = (
        select(
            ChartOfAccount.account_id,
            ChartOfAccount.code,
            ChartOfAccount.name,
            ChartOfAccount.type,
            JournalLine.currency,
            func.coalesce(func.sum(JournalLine.debit), 0).label("debit"),
            func.coalesce(func.sum(JournalLine.credit), 0).label("credit"),
            func.coalesce(func.sum(JournalLine.base_amount_debit), 0).label("base_debit"),
            func.coalesce(func.sum(JournalLine.base_amount_credit), 0).label("base_credit"),
        )
        .join(JournalLine, JournalLine.account_id == ChartOfAccount.account_id)
        .join(JournalEntry, JournalEntry.entry_id == JournalLine.entry_id)
        .where(JournalEntry.posted_at <= cutoff)
        .group_by(
            ChartOfAccount.account_id,
            ChartOfAccount.code,
            ChartOfAccount.name,
            ChartOfAccount.type,
            JournalLine.currency,
        )
        .order_by(ChartOfAccount.code, JournalLine.currency)
    )
    rows: list[TrialBalanceRow] = []
    total_debit = Decimal("0")
    total_credit = Decimal("0")
    for r in db.execute(stmt):
        debit = Decimal(r.debit)
        credit = Decimal(r.credit)
        base_debit = Decimal(r.base_debit)
        base_credit = Decimal(r.base_credit)
        rows.append(
            TrialBalanceRow(
                account_id=r.account_id,
                code=r.code,
                name=r.name,
                type=r.type,
                currency=r.currency,
                debit=debit,
                credit=credit,
                net=debit - credit,
                base_debit=base_debit,
                base_credit=base_credit,
                base_net=base_debit - base_credit,
            )
        )
        total_debit += base_debit
        total_credit += base_credit
    return TrialBalanceReport(
        as_of=as_of,
        base_currency=get_settings().base_currency,
        rows=rows,
        total_debit_base=total_debit,
        total_credit_base=total_credit,
    )


# Account-type sign convention for the balance sheet / P&L:
# - ASSET, EXPENSE, COGS: natural balance is debit. net = debit - credit.
# - LIABILITY, EQUITY, INCOME: natural balance is credit. net = credit - debit.
_DEBIT_NATURAL = {"ASSET", "EXPENSE", "COGS"}


@router.get("/reports/balance-sheet", response_model=BalanceSheetReport)
def balance_sheet(
    as_of: date = Query(default_factory=date.today),
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> BalanceSheetReport:
    cutoff = _as_of_cutoff(as_of)
    stmt = (
        select(
            ChartOfAccount.account_id,
            ChartOfAccount.code,
            ChartOfAccount.name,
            ChartOfAccount.type,
            func.coalesce(func.sum(JournalLine.base_amount_debit), 0).label("base_debit"),
            func.coalesce(func.sum(JournalLine.base_amount_credit), 0).label("base_credit"),
        )
        .join(JournalLine, JournalLine.account_id == ChartOfAccount.account_id, isouter=True)
        .join(
            JournalEntry,
            (JournalEntry.entry_id == JournalLine.entry_id)
            & (JournalEntry.posted_at <= cutoff),
            isouter=True,
        )
        .group_by(
            ChartOfAccount.account_id,
            ChartOfAccount.code,
            ChartOfAccount.name,
            ChartOfAccount.type,
        )
        .order_by(ChartOfAccount.code)
    )

    by_type: dict[str, list[BalanceSheetLine]] = {}
    type_totals: dict[str, Decimal] = {}
    for r in db.execute(stmt):
        base_debit = Decimal(r.base_debit)
        base_credit = Decimal(r.base_credit)
        if r.type in _DEBIT_NATURAL:
            balance = base_debit - base_credit
        else:
            balance = base_credit - base_debit
        # Skip accounts with zero activity to keep the report readable.
        if balance == 0 and base_debit == 0 and base_credit == 0:
            continue
        by_type.setdefault(r.type, []).append(
            BalanceSheetLine(
                account_id=r.account_id, code=r.code, name=r.name, base_balance=balance
            )
        )
        type_totals[r.type] = type_totals.get(r.type, Decimal("0")) + balance

    groups = [
        BalanceSheetGroup(type=t, total_base=type_totals.get(t, Decimal("0")), lines=by_type[t])
        for t in ("ASSET", "LIABILITY", "EQUITY", "INCOME", "EXPENSE", "COGS")
        if t in by_type
    ]

    return BalanceSheetReport(
        as_of=as_of,
        base_currency=get_settings().base_currency,
        groups=groups,
        assets_total_base=type_totals.get("ASSET", Decimal("0")),
        liabilities_total_base=type_totals.get("LIABILITY", Decimal("0")),
        equity_total_base=type_totals.get("EQUITY", Decimal("0")),
        income_total_base=type_totals.get("INCOME", Decimal("0")),
        expense_total_base=type_totals.get("EXPENSE", Decimal("0"))
        + type_totals.get("COGS", Decimal("0")),
    )


# Monetary account types that IFRS/GAAP require to be revalued at period end.
# Non-monetary types (equity, P&L) are not re-translated.
_MONETARY_TYPES = {"ASSET", "LIABILITY"}


@router.get("/reports/fx-revaluation", response_model=FxRevaluationReport)
def fx_revaluation(
    as_of: date = Query(default_factory=date.today),
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> FxRevaluationReport:
    """Unrealised FX gain/loss on open monetary balances held in non-base currencies.

    For each (account, currency) bucket where currency != base and the account is
    ASSET or LIABILITY, compare the stamped base value against what the face
    amount would translate to at the newest rate on or before `as_of`.

    Report-only — does not post to the ledger.
    """
    cutoff = _as_of_cutoff(as_of)
    base_currency = get_settings().base_currency

    stmt = (
        select(
            ChartOfAccount.account_id,
            ChartOfAccount.code,
            ChartOfAccount.name,
            ChartOfAccount.type,
            JournalLine.currency,
            func.coalesce(func.sum(JournalLine.debit), 0).label("debit"),
            func.coalesce(func.sum(JournalLine.credit), 0).label("credit"),
            func.coalesce(func.sum(JournalLine.base_amount_debit), 0).label("base_debit"),
            func.coalesce(func.sum(JournalLine.base_amount_credit), 0).label("base_credit"),
        )
        .join(JournalLine, JournalLine.account_id == ChartOfAccount.account_id)
        .join(JournalEntry, JournalEntry.entry_id == JournalLine.entry_id)
        .where(JournalEntry.posted_at <= cutoff)
        .where(ChartOfAccount.type.in_(_MONETARY_TYPES))
        .where(JournalLine.currency != base_currency)
        .group_by(
            ChartOfAccount.account_id,
            ChartOfAccount.code,
            ChartOfAccount.name,
            ChartOfAccount.type,
            JournalLine.currency,
        )
        .order_by(ChartOfAccount.code, JournalLine.currency)
    )

    # Cache rate lookups by currency to avoid re-querying for the same ccy.
    rate_cache: dict[str, tuple[Decimal, date] | None] = {}

    def _latest_rate(currency: str) -> tuple[Decimal, date] | None:
        if currency in rate_cache:
            return rate_cache[currency]
        row = db.execute(
            select(FxRate.rate, FxRate.as_of_date)
            .where(FxRate.from_currency == currency)
            .where(FxRate.to_currency == base_currency)
            .where(FxRate.as_of_date <= as_of)
            .order_by(FxRate.as_of_date.desc())
            .limit(1)
        ).first()
        result = (Decimal(row.rate), row.as_of_date) if row else None
        rate_cache[currency] = result
        return result

    lines: list[FxRevaluationLine] = []
    total_gain = Decimal("0")
    total_loss = Decimal("0")
    missing_rates: set[str] = set()

    for r in db.execute(stmt):
        debit = Decimal(r.debit)
        credit = Decimal(r.credit)
        base_debit = Decimal(r.base_debit)
        base_credit = Decimal(r.base_credit)
        if r.type == "ASSET":
            face_balance = debit - credit
            original_base = base_debit - base_credit
            asset_sign = Decimal("1")
        else:  # LIABILITY
            face_balance = credit - debit
            original_base = base_credit - base_debit
            asset_sign = Decimal("-1")

        # Skip fully-settled buckets.
        if face_balance == 0 and original_base == 0:
            continue

        rate_hit = _latest_rate(r.currency)
        if rate_hit is None:
            missing_rates.add(r.currency)
            lines.append(
                FxRevaluationLine(
                    account_id=r.account_id,
                    code=r.code,
                    name=r.name,
                    type=r.type,
                    currency=r.currency,
                    face_balance=face_balance,
                    original_base=original_base,
                    revalued_base=None,
                    rate=None,
                    rate_as_of=None,
                    unrealised_gain_loss=None,
                    rate_missing=True,
                )
            )
            continue

        rate, rate_date = rate_hit
        revalued_base = (face_balance * rate).quantize(Decimal("0.0001"))
        # Gain/loss: asset up-value = gain; liability up-value = loss.
        unrealised = (revalued_base - original_base) * asset_sign
        if unrealised > 0:
            total_gain += unrealised
        elif unrealised < 0:
            total_loss += -unrealised

        lines.append(
            FxRevaluationLine(
                account_id=r.account_id,
                code=r.code,
                name=r.name,
                type=r.type,
                currency=r.currency,
                face_balance=face_balance,
                original_base=original_base,
                revalued_base=revalued_base,
                rate=rate,
                rate_as_of=rate_date,
                unrealised_gain_loss=unrealised,
                rate_missing=False,
            )
        )

    return FxRevaluationReport(
        as_of=as_of,
        base_currency=base_currency,
        lines=lines,
        total_unrealised_gain=total_gain,
        total_unrealised_loss=total_loss,
        missing_rates=sorted(missing_rates),
    )


# ---------------- AR / AP aging ----------------


_ZERO = Decimal("0")


def _empty_buckets() -> AgingBuckets:
    return AgingBuckets(
        current=_ZERO, d_1_30=_ZERO, d_31_60=_ZERO, d_61_90=_ZERO,
        d_90_plus=_ZERO, total=_ZERO,
    )


def _add_to_bucket(buckets: AgingBuckets, days_past_due: int, amount: Decimal) -> None:
    if days_past_due <= 0:
        buckets.current += amount
    elif days_past_due <= 30:
        buckets.d_1_30 += amount
    elif days_past_due <= 60:
        buckets.d_31_60 += amount
    elif days_past_due <= 90:
        buckets.d_61_90 += amount
    else:
        buckets.d_90_plus += amount
    buckets.total += amount


@router.get("/reports/ar-aging", response_model=AgingReport)
def ar_aging(
    as_of: date = Query(default_factory=date.today),
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> AgingReport:
    """Open AR balances bucketed by days past due.

    Includes invoices with status SENT or PARTIAL and positive balance_due.
    Grouped by (customer, currency). "Current" = not yet due as of `as_of`.
    """
    rows_raw = db.execute(
        select(
            Invoice.customer_id,
            Invoice.currency,
            Invoice.due_date,
            Invoice.balance_due,
            Customer.name,
        )
        .join(Customer, Customer.customer_id == Invoice.customer_id, isouter=True)
        .where(Invoice.status.in_(("SENT", "PARTIAL")))
        .where(Invoice.is_template.is_(False))
        .where(Invoice.balance_due > 0)
    ).all()

    by_key: dict[tuple[str, str], AgingRow] = {}
    totals: dict[str, AgingBuckets] = {}
    for customer_id, currency, due_date, balance, name in rows_raw:
        key = (str(customer_id) if customer_id else "-", currency)
        if key not in by_key:
            by_key[key] = AgingRow(
                party_id=key[0],
                party_name=name or "(no customer)",
                currency=currency,
                buckets=_empty_buckets(),
            )
        days = (as_of - due_date).days if due_date else 0
        _add_to_bucket(by_key[key].buckets, days, Decimal(balance or 0))
        totals.setdefault(currency, _empty_buckets())
        _add_to_bucket(totals[currency], days, Decimal(balance or 0))

    return AgingReport(
        as_of=as_of,
        kind="AR",
        rows=sorted(by_key.values(), key=lambda r: (r.party_name.lower(), r.currency)),
        totals_by_currency=totals,
    )


@router.get("/reports/ap-aging", response_model=AgingReport)
def ap_aging(
    as_of: date = Query(default_factory=date.today),
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> AgingReport:
    """Open AP balances bucketed by days past due.

    Includes bills with status OPEN or PARTIAL and positive balance_due.
    Grouped by (vendor, currency).
    """
    rows_raw = db.execute(
        select(
            Bill.vendor_id,
            Bill.currency,
            Bill.due_date,
            Bill.balance_due,
            Vendor.name,
        )
        .join(Vendor, Vendor.vendor_id == Bill.vendor_id, isouter=True)
        .where(Bill.status.in_(("OPEN", "PARTIAL")))
        .where(Bill.balance_due > 0)
    ).all()

    by_key: dict[tuple[str, str], AgingRow] = {}
    totals: dict[str, AgingBuckets] = {}
    for vendor_id, currency, due_date, balance, name in rows_raw:
        key = (str(vendor_id) if vendor_id else "-", currency)
        if key not in by_key:
            by_key[key] = AgingRow(
                party_id=key[0],
                party_name=name or "(no vendor)",
                currency=currency,
                buckets=_empty_buckets(),
            )
        days = (as_of - due_date).days if due_date else 0
        _add_to_bucket(by_key[key].buckets, days, Decimal(balance or 0))
        totals.setdefault(currency, _empty_buckets())
        _add_to_bucket(totals[currency], days, Decimal(balance or 0))

    return AgingReport(
        as_of=as_of,
        kind="AP",
        rows=sorted(by_key.values(), key=lambda r: (r.party_name.lower(), r.currency)),
        totals_by_currency=totals,
    )
