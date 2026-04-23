from datetime import date, datetime
from decimal import Decimal

from pydantic import BaseModel, ConfigDict, Field


class FxRateCreate(BaseModel):
    from_currency: str = Field(min_length=3, max_length=3)
    to_currency: str = Field(min_length=3, max_length=3)
    rate: Decimal = Field(gt=0, max_digits=19, decimal_places=8)
    as_of_date: date
    source: str = Field(default="MANUAL", min_length=1, max_length=50)


class FxRateOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    rate_id: int
    from_currency: str
    to_currency: str
    rate: Decimal
    as_of_date: date
    source: str
    created_at: datetime
