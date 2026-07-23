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
from app.mcp.oauth import build_oauth_router


def create_app() -> FastAPI:
    settings = get_settings()

    # The FastMCP sub-app owns its own lifespan (streamable-HTTP session
    # manager startup/shutdown) — it must be imported before FastAPI() is
    # constructed so it can be passed as the outer app's lifespan too,
    # otherwise the MCP session manager never starts and /mcp requests hang.
    mcp_http_app = None
    if settings.mcp_enabled:
        from app.mcp.server import mcp_http_app as _mcp_http_app

        mcp_http_app = _mcp_http_app

    app = FastAPI(
        title="wenhao-billing",
        version="0.1.0",
        lifespan=mcp_http_app.lifespan if mcp_http_app is not None else None,
    )

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

    # MCP OAuth authorization server — mounted at the root (well-known + /oauth/*),
    # not under /api/v1, so discovery URLs match the public base origin.
    if settings.mcp_enabled:
        app.include_router(build_oauth_router())
        app.mount("/mcp", mcp_http_app)

    return app


app = create_app()
