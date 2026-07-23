"""Unit tests for app.services.projects — extracted from routers/projects.py.

TDD sub-task A for Task 8's projects extraction.
"""
from __future__ import annotations

import uuid

import pytest

from app.schemas.project import ProjectCreate, ProjectUpdate
from app.services.projects import ProjectError, create_project, update_project


def test_create_project_normalizes_code_and_currency(db, seed_customer):
    payload = ProjectCreate(
        customer_id=seed_customer, code="atlas-26", name=" Atlas Project ", currency="usd"
    )
    project = create_project(db, payload)
    assert project.code == "ATLAS-26"
    assert project.currency == "USD"
    assert project.name == "Atlas Project"
    assert project.status == "ACTIVE"


def test_create_project_duplicate_code_raises_and_leaves_one_row(db, seed_customer):
    from sqlalchemy import func, select

    from app.models.project import Project

    create_project(
        db, ProjectCreate(customer_id=seed_customer, code="DUP-1", name="First", currency="USD")
    )
    db.commit()  # simulate the router's own commit boundary between requests
    with pytest.raises(ProjectError):
        create_project(
            db,
            ProjectCreate(customer_id=seed_customer, code="DUP-1", name="Second", currency="USD"),
        )
    count = db.scalar(
        select(func.count()).select_from(Project).where(Project.code == "DUP-1")
    )
    assert count == 1


def test_update_project_edits_and_normalizes(db, seed_project):
    payload = ProjectUpdate(code="atlas-27", currency="sgd", status="ON_HOLD")
    project = update_project(db, seed_project, payload)
    assert project.code == "ATLAS-27"
    assert project.currency == "SGD"
    assert project.status == "ON_HOLD"


def test_update_project_not_found_raises(db):
    with pytest.raises(ProjectError):
        update_project(db, uuid.uuid4(), ProjectUpdate(name="x"))
