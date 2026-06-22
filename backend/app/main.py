from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from starlette.middleware.sessions import SessionMiddleware

from app.api.v1.routers import (
    accounting,
    auth,
    bills,
    business_profile,
    customers,
    invoices,
    items,
    payments,
    projects,
    quotations,
    recon,
    stats,
    users,
    vendors,
)
from app.config import get_settings


def create_app() -> FastAPI:
    settings = get_settings()
    app = FastAPI(title="wenhao-billing", version="0.1.0")

    app.add_middleware(
        SessionMiddleware,
        secret_key=settings.session_secret,
        session_cookie=settings.session_cookie_name,
        max_age=settings.session_max_age_seconds,
        same_site="lax",
        https_only=False,
    )
    app.add_middleware(
        CORSMiddleware,
        allow_origins=settings.cors_origins,
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    prefix = "/api/v1"
    app.include_router(auth.router, prefix=prefix)
    app.include_router(invoices.router, prefix=prefix)
    app.include_router(bills.router, prefix=prefix)
    app.include_router(customers.router, prefix=prefix)
    app.include_router(payments.router, prefix=prefix)
    app.include_router(recon.router, prefix=prefix)
    app.include_router(stats.router, prefix=prefix)
    app.include_router(vendors.router, prefix=prefix)
    app.include_router(items.router, prefix=prefix)
    app.include_router(accounting.router, prefix=prefix)
    app.include_router(users.router, prefix=prefix)
    app.include_router(business_profile.router, prefix=prefix)
    app.include_router(projects.router, prefix=prefix)
    app.include_router(quotations.router, prefix=prefix)

    return app


app = create_app()
