import app.api.v1.routers.invoices as inv
import app.api.v1.routers.quotations as quo
import app.services.pdf as pdf


def test_routers_use_facade():
    assert inv.render_invoice_pdf is pdf.render_invoice_pdf
    assert quo.render_quotation_pdf is pdf.render_quotation_pdf
