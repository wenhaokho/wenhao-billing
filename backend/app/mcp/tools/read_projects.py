"""Project read tools.

Mirrors the projects router's list/get queries
(backend/app/api/v1/routers/projects.py: list_projects, get_project).

Real columns on Project (backend/app/models/project.py): project_id,
customer_id, code, name, currency, contract_value, status, start_date,
end_date, notes, active, created_at (_PROJECT_FIELDS is a deliberate
subset, matching the read_invoices.py pattern).
"""
from __future__ import annotations

from uuid import UUID

from sqlalchemy import select

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.models.project import Project

_PROJECT_FIELDS = [
    "project_id",
    "customer_id",
    "code",
    "name",
    "currency",
    "contract_value",
    "status",
    "start_date",
    "end_date",
    "active",
]


@mcp.tool
def list_projects(customer_id: str | None = None, limit: int = 200) -> list[dict]:
    """List projects, optionally filtered by customer_id."""
    with tool_session() as db:
        stmt = (
            select(Project)
            .order_by(Project.created_at.desc())
            .limit(min(limit, 500))
        )
        if customer_id:
            stmt = stmt.where(Project.customer_id == UUID(customer_id))
        return [to_dict(p, _PROJECT_FIELDS) for p in db.scalars(stmt)]


@mcp.tool
def get_project(project_id: str) -> dict:
    """Fetch one project by id. Returns {} if not found."""
    with tool_session() as db:
        p = db.get(Project, UUID(project_id))
        return to_dict(p, _PROJECT_FIELDS) if p else {}
