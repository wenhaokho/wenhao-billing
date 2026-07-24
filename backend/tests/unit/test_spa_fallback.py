"""SPA fallback routing — deep links resolve to index.html, backend paths 404.

Regression: a client route whose name starts with a reserved prefix (e.g.
``/mcp-integration`` vs the reserved ``mcp`` segment) must still be served the
SPA shell instead of a JSON 404. See ``_mount_frontend`` in ``app.main``.
"""

from __future__ import annotations

from pathlib import Path

from fastapi import FastAPI
from fastapi.testclient import TestClient

from app.main import _mount_frontend


def _client_with_dist(tmp_path: Path) -> TestClient:
    (tmp_path / "index.html").write_text("<!doctype html><div id=app></div>")
    (tmp_path / "favicon.ico").write_text("icon")
    assets = tmp_path / "assets"
    assets.mkdir()
    (assets / "app.js").write_text("console.log(1)")

    app = FastAPI()
    _mount_frontend(app, str(tmp_path))
    return TestClient(app)


def test_client_route_starting_with_reserved_prefix_serves_spa(tmp_path: Path) -> None:
    # "mcp-integration" starts with the reserved "mcp" segment but is a real
    # SPA route — it must get the index shell, not a 404.
    client = _client_with_dist(tmp_path)
    resp = client.get("/mcp-integration")
    assert resp.status_code == 200
    assert "id=app" in resp.text


def test_plain_client_route_serves_spa(tmp_path: Path) -> None:
    client = _client_with_dist(tmp_path)
    resp = client.get("/dashboard")
    assert resp.status_code == 200
    assert "id=app" in resp.text


def test_real_static_file_is_served(tmp_path: Path) -> None:
    client = _client_with_dist(tmp_path)
    resp = client.get("/favicon.ico")
    assert resp.status_code == 200
    assert resp.text == "icon"


def test_reserved_backend_segments_return_json_404(tmp_path: Path) -> None:
    client = _client_with_dist(tmp_path)
    for path in ("/api/nope", "/mcp/nope", "/oauth/nope", "/docs/nope"):
        resp = client.get(path)
        assert resp.status_code == 404, path
        assert resp.json() == {"detail": "Not Found"}, path
