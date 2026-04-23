from datetime import date
from decimal import Decimal

from pydantic import BaseModel


class TrialBalanceRow(BaseModel):
    account_id: int
    code: str
    name: str
    type: str
    currency: str
    debit: Decimal
    credit: Decimal
    net: Decimal  # debit - credit (natural sign per account-type is caller's concern)
    base_debit: Decimal
    base_credit: Decimal
    base_net: Decimal


class TrialBalanceReport(BaseModel):
    as_of: date
    base_currency: str
    rows: list[TrialBalanceRow]
    # Convenience totals in base currency; per-currency gross totals live on
    # rows so the UI can still present a per-currency breakdown.
    total_debit_base: Decimal
    total_credit_base: Decimal


class BalanceSheetLine(BaseModel):
    account_id: int
    code: str
    name: str
    base_balance: Decimal  # signed as stored (asset = positive debit, liability = positive credit)


class BalanceSheetGroup(BaseModel):
    type: str
    total_base: Decimal
    lines: list[BalanceSheetLine]


class BalanceSheetReport(BaseModel):
    as_of: date
    base_currency: str
    groups: list[BalanceSheetGroup]
    assets_total_base: Decimal
    liabilities_total_base: Decimal
    equity_total_base: Decimal
    income_total_base: Decimal
    expense_total_base: Decimal


class FxRevaluationLine(BaseModel):
    account_id: int
    code: str
    name: str
    type: str  # ASSET or LIABILITY
    currency: str
    face_balance: Decimal        # in `currency`, signed by account natural (AR positive = owed to us)
    original_base: Decimal       # stamped base value, signed by account natural
    revalued_base: Decimal | None  # face × rate, None when rate is missing
    rate: Decimal | None         # rate used to translate
    rate_as_of: date | None      # effective date of the rate actually picked
    unrealised_gain_loss: Decimal | None  # positive = gain for us, negative = loss
    rate_missing: bool = False


class FxRevaluationReport(BaseModel):
    as_of: date
    base_currency: str
    lines: list[FxRevaluationLine]
    total_unrealised_gain: Decimal   # sum of positive unrealised (rate-available rows)
    total_unrealised_loss: Decimal   # sum of |negative| unrealised
    missing_rates: list[str]         # currencies for which no rate was found on/before as_of


# ---------------- Aging (AR / AP) ----------------


class AgingBuckets(BaseModel):
    current: Decimal   # not yet due
    d_1_30: Decimal    # 1..30 days past due
    d_31_60: Decimal
    d_61_90: Decimal
    d_90_plus: Decimal
    total: Decimal


class AgingRow(BaseModel):
    party_id: str      # customer_id for AR, vendor_id for AP
    party_name: str
    currency: str
    buckets: AgingBuckets


class AgingReport(BaseModel):
    as_of: date
    kind: str          # "AR" or "AP"
    rows: list[AgingRow]
    totals_by_currency: dict[str, AgingBuckets]
