"""Customer write tools — thin wrappers over app.services.customers.

Never re-implement customer write logic here: each tool builds the
matching Pydantic payload from tool args, calls the corresponding
`app.services.customers` function (the exact logic extracted out of
routers/customers.py — see the refactor commit that precedes this one),
and serializes the result via `_CUSTOMER_FIELDS` (imported from
read_customers.py, the deliberate field subset shared by read and write
tools).

Error handling: `customers.CustomerError` is caught at the tool boundary
and returned as `{"error": str(e)}` — see write_invoices.py's module
docstring for why this is safe (the `with tool_session():` rollback fires
before this except catches it).
"""
from __future__ import annotations

from uuid import UUID

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.mcp.tools.read_customers import _CUSTOMER_DETAIL_FIELDS
from app.mcp.validate import reject_unknown_changes
from app.schemas.customer import CustomerCreate, CustomerUpdate
from app.services import customers as customer_service


@mcp.tool
def create_customer(
    name: str,
    matching_aliases: list[str] | None = None,
    contact_first_name: str | None = None,
    contact_last_name: str | None = None,
    contact_email: str | None = None,
    contact_phone: str | None = None,
    contact_phone_2: str | None = None,
    account_number: str | None = None,
    website: str | None = None,
    notes: str | None = None,
    default_currency: str | None = None,
    billing_address1: str | None = None,
    billing_address2: str | None = None,
    billing_city: str | None = None,
    billing_state: str | None = None,
    billing_postal_code: str | None = None,
    billing_country: str | None = None,
    ship_to_name: str | None = None,
    shipping_address1: str | None = None,
    shipping_address2: str | None = None,
    shipping_city: str | None = None,
    shipping_state: str | None = None,
    shipping_postal_code: str | None = None,
    shipping_country: str | None = None,
    shipping_phone: str | None = None,
    shipping_delivery_instructions: str | None = None,
) -> dict:
    """Create a customer. Autonomous.

    Every writable customer column is an explicit arg on purpose. When this
    tool only exposed name/email/contact-name/currency/notes, a caller asked
    for a customer "with phone X at address Y" had nowhere to put either and
    ended up folding both into `notes` — the data was in the record but not
    in the fields the UI, invoices, and matching read from.

    Address is structured (address1/address2/city/state/postal_code/country),
    matching the customer form. The legacy unstructured `billing_address`
    column is deliberately not writable here — it is read-only back-compat.

    The primary contact name is stored as first/last — the same fields the
    customer edit form writes. There is deliberately no combined
    `contact_name` arg: the legacy column was dropped in
    0024_customer_contact_single because writing it here left records whose
    contact name could not be edited in the UI.
    """
    # No try/except: the customer service's create path never raises
    # CustomerError (only update does, on not-found), so wrapping it here would
    # be dead code.
    payload = CustomerCreate(
        name=name,
        matching_aliases=matching_aliases or [],
        contact_first_name=contact_first_name,
        contact_last_name=contact_last_name,
        contact_email=contact_email,
        contact_phone=contact_phone,
        contact_phone_2=contact_phone_2,
        account_number=account_number,
        website=website,
        notes=notes,
        default_currency=default_currency,
        billing_address1=billing_address1,
        billing_address2=billing_address2,
        billing_city=billing_city,
        billing_state=billing_state,
        billing_postal_code=billing_postal_code,
        billing_country=billing_country,
        ship_to_name=ship_to_name,
        shipping_address1=shipping_address1,
        shipping_address2=shipping_address2,
        shipping_city=shipping_city,
        shipping_state=shipping_state,
        shipping_postal_code=shipping_postal_code,
        shipping_country=shipping_country,
        shipping_phone=shipping_phone,
        shipping_delivery_instructions=shipping_delivery_instructions,
    )
    with tool_session() as db:
        customer = customer_service.create_customer(db, payload)
        db.flush()
        return to_dict(customer, _CUSTOMER_DETAIL_FIELDS)


@mcp.tool
def update_customer(customer_id: str, changes: dict) -> dict:
    """Edit a customer. `changes` keys are CustomerUpdate field names — the
    same names `create_customer` takes (e.g. contact_phone, billing_address1,
    billing_city, notes, default_currency, active). Autonomous.

    Unknown keys are rejected rather than applied: CustomerUpdate's default
    extra="ignore" would otherwise drop a near-miss key like "phone" or
    "address" and still return success, the edit silently doing nothing. See
    app/mcp/validate.py — every `update_*` tool shares that check.
    """
    if (err := reject_unknown_changes(changes, CustomerUpdate)) is not None:
        return err
    try:
        with tool_session() as db:
            customer = customer_service.update_customer(
                db, UUID(customer_id), CustomerUpdate(**changes)
            )
            db.flush()
            return to_dict(customer, _CUSTOMER_DETAIL_FIELDS)
    except customer_service.CustomerError as e:
        return {"error": str(e)}
