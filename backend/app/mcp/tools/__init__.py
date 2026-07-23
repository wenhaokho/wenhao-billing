"""Importing this package registers all tools on the FastMCP instance."""
from app.mcp.tools import (  # noqa: F401
    read_bills,
    read_catalog,
    read_customers,
    read_invoices,
    read_misc,
    read_projects,
    read_quotations,
    read_recon,
    write_bills,
    write_catalog,
    write_customers,
    write_invoices,
    write_projects,
    write_quotations,
    write_recon,
)
