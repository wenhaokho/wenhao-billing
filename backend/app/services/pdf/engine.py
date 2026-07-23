"""HTML→PDF via headless Chromium (Playwright), launched per request.

Sync Playwright is created and destroyed within one call, so it is safe
inside FastAPI's sync-endpoint threadpool (no cross-thread object sharing).
"""
from __future__ import annotations

from playwright.sync_api import sync_playwright

from app.config import get_settings


def html_to_pdf(html: str) -> bytes:
    timeout = get_settings().pdf_render_timeout_ms
    with sync_playwright() as p:
        browser = p.chromium.launch(args=["--no-sandbox"])
        try:
            page = browser.new_page()
            page.set_default_timeout(timeout)
            page.set_content(html, wait_until="load")
            return page.pdf(
                format="A4",
                print_background=True,
                margin={"top": "0", "right": "0", "bottom": "0", "left": "0"},
                prefer_css_page_size=True,
            )
        finally:
            browser.close()
