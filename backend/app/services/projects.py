"""Project create/update — extracted from routers/projects.py so the MCP
write tools (write_projects.py) can call the exact same write logic
without duplicating it in the tool layer.

Behavior preserved exactly, including the code/currency normalization
(strip + upper) and the unique-code-conflict handling that was previously
inline in the router (db.commit() -> IntegrityError -> HTTPException).
Here it's db.flush() -> IntegrityError -> ProjectError, since the service
doesn't own the transaction boundary (caller commits).
"""
from __future__ import annotations

from uuid import UUID

from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from app.models.project import Project
from app.schemas.project import ProjectCreate, ProjectUpdate


class ProjectError(Exception):
    """Raised on domain errors in the project layer (not found, duplicate code)."""


def create_project(db: Session, payload: ProjectCreate) -> Project:
    project = Project(
        customer_id=payload.customer_id,
        code=payload.code.strip().upper(),
        name=payload.name.strip(),
        currency=payload.currency.upper(),
        contract_value=payload.contract_value,
        status=payload.status,
        start_date=payload.start_date,
        end_date=payload.end_date,
        notes=payload.notes,
    )
    db.add(project)
    try:
        db.flush()
    except IntegrityError:
        db.rollback()
        raise ProjectError(f"project code {project.code!r} already exists")
    return project


def update_project(db: Session, project_id: UUID, payload: ProjectUpdate) -> Project:
    project = db.get(Project, project_id)
    if project is None:
        raise ProjectError(f"project {project_id} not found")
    data = payload.model_dump(exclude_unset=True)
    if "code" in data and data["code"] is not None:
        data["code"] = data["code"].strip().upper()
    if "currency" in data and data["currency"] is not None:
        data["currency"] = data["currency"].upper()
    for k, v in data.items():
        setattr(project, k, v)
    try:
        db.flush()
    except IntegrityError:
        db.rollback()
        raise ProjectError("project code already exists")
    return project
