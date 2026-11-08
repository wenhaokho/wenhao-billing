"""Project write tools — thin wrappers over app.services.projects.

Same pattern as write_customers.py / write_invoices.py. Only
`projects.ProjectError` is caught (covers both "not found" on update and
the duplicate-code conflict on create — see app/services/projects.py).

`update_project` vets its `changes` dict with `reject_unknown_changes`
before opening a session — see app/mcp/validate.py. Returns use
`_PROJECT_DETAIL_FIELDS` so a supplied `notes` is echoed back.
"""
from __future__ import annotations

from decimal import Decimal
from uuid import UUID

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.mcp.tools.read_projects import _PROJECT_DETAIL_FIELDS
from app.mcp.validate import reject_unknown_changes
from app.schemas.project import ProjectCreate, ProjectUpdate
from app.services import projects as project_service


@mcp.tool
def create_project(
    customer_id: str,
    code: str,
    name: str,
    currency: str,
    contract_value: str | None = None,
    status: str = "ACTIVE",
    start_date: str | None = None,
    end_date: str | None = None,
    notes: str | None = None,
) -> dict:
    """Create a project. Autonomous."""
    try:
        payload = ProjectCreate(
            customer_id=UUID(customer_id),
            code=code,
            name=name,
            currency=currency,
            contract_value=Decimal(contract_value) if contract_value else None,
            status=status,
            start_date=start_date,
            end_date=end_date,
            notes=notes,
        )
        with tool_session() as db:
            project = project_service.create_project(db, payload)
            db.flush()
            return to_dict(project, _PROJECT_DETAIL_FIELDS)
    except project_service.ProjectError as e:
        return {"error": str(e)}


@mcp.tool
def update_project(project_id: str, changes: dict) -> dict:
    """Edit a project. `changes` keys are ProjectUpdate field names — the same
    names `create_project` takes (e.g. code, name, currency, status,
    contract_value, notes, active). Autonomous.

    Unknown keys are rejected rather than applied, so a near-miss like
    "project_name" errors instead of being dropped by Pydantic's default
    extra="ignore" while the tool still reports success.
    """
    if (err := reject_unknown_changes(changes, ProjectUpdate)) is not None:
        return err
    try:
        with tool_session() as db:
            project = project_service.update_project(
                db, UUID(project_id), ProjectUpdate(**changes)
            )
            db.flush()
            return to_dict(project, _PROJECT_DETAIL_FIELDS)
    except project_service.ProjectError as e:
        return {"error": str(e)}
