"""API responses must never be cacheable by a CDN or the browser.

Regression: Cloudflare edge-cached authenticated GETs (no Cache-Control from
origin), so the Awaiting Finalization queue kept serving an already-finalized
invoice as DRAFT and a second Finalize click returned 400.
"""


def test_api_responses_are_no_store(client):
    r = client.get("/api/v1/auth/me")
    assert r.headers.get("cache-control") == "no-store"


def test_api_404_is_no_store(client):
    r = client.get("/api/v1/does-not-exist")
    assert r.headers.get("cache-control") == "no-store"
