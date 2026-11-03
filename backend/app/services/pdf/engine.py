"""HTML→PDF via WeasyPrint.

The rendered HTML is fully self-contained (fonts and logo are data URIs), so
the URL fetcher allows only data: URIs: a stray file:// or http(s) reference
in the template or in user-entered content can never read the container's
filesystem or reach the network.
"""
from __future__ import annotations

from weasyprint import HTML
from weasyprint.urls import URLFetcher


def _fetcher() -> URLFetcher:
    return URLFetcher(allowed_protocols={"data"})


def html_to_pdf(html: str) -> bytes:
    return HTML(string=html, url_fetcher=_fetcher()).write_pdf()
