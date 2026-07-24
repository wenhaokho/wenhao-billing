import os

from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import FileResponse
from fastapi.staticfiles import StaticFiles
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

    # Serve the built SPA from the same origin as the API (single-container
    # deploy). Registered LAST so it never shadows the API, MCP, OAuth, or docs
    # routes above — the catch-all only fires for paths nothing else matched.
    _mount_frontend(app, settings.frontend_dist)

    return app


def _mount_frontend(app: FastAPI, dist_dir: str | None) -> None:
    if not dist_dir or not os.path.isdir(dist_dir):
        return

    root = os.path.abspath(dist_dir)
    index_file = os.path.join(root, "index.html")
    assets_dir = os.path.join(root, "assets")
    if os.path.isdir(assets_dir):
        # Hashed JS/CSS bundles — immutable, safe to serve directly.
        app.mount("/assets", StaticFiles(directory=assets_dir), name="assets")

    # Backend paths that must never resolve to the SPA shell. Unknown routes
    # under these return a real 404 (JSON) instead of index.html, so a bad API
    # call doesn't silently get an HTML 200.
    reserved = ("api/", "mcp", "oauth", ".well-known", "docs", "redoc", "openapi.json")

    @app.get("/{full_path:path}", include_in_schema=False)
    async def spa_fallback(full_path: str) -> FileResponse:
        if full_path.startswith(reserved):
            raise HTTPException(status_code=404, detail="Not Found")
        candidate = os.path.normpath(os.path.join(root, full_path))
        # Serve a real static file (favicon, etc.) only if it stays inside the
        # dist root — guards against path-traversal escaping via `..`.
        if (
            full_path
            and candidate.startswith(root + os.sep)
            and os.path.isfile(candidate)
        ):
            return FileResponse(candidate)
        # History-mode client route — hand back the SPA shell.
        return FileResponse(index_file)


app = create_app()
