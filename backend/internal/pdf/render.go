package pdf

import (
	"bytes"
	"context"
	"embed"
	_ "embed" // the default logo
	"fmt"
	"time"

	"github.com/go-pdf/fpdf"
)

// reproEpoch is the creation date of a Document without one, and every PDF's
// modification date: a document renders to the same bytes every time.
var reproEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// The three families of the design (inventory §2.5), static TTF instances of
// Google Fonts' OFL releases (licences beside them in fonts/). Each family
// has a normal and a bold style ("B"), mapped to two weights.
const (
	fontBody    = "dmsans"  // DM Sans 400 / 500
	fontDisplay = "manrope" // Manrope 500 / 700
	fontMono    = "mono"    // JetBrains Mono 400 / 600
)

//go:embed fonts/*.ttf
var fontFS embed.FS

//go:embed default-logo.png
var defaultLogo []byte

var fontFiles = []struct {
	family string
	style  string
	file   string
}{
	{fontBody, "", "DMSans-Regular.ttf"},
	{fontBody, "B", "DMSans-Medium.ttf"},
	// Manrope-Medium.ttf is a static instance (wght 500) of Google Fonts'
	// variable Manrope, cut with fontTools' instancer.
	{fontDisplay, "", "Manrope-Medium.ttf"},
	{fontDisplay, "B", "Manrope-Bold.ttf"},
	{fontMono, "", "JetBrainsMono-Regular.ttf"},
	{fontMono, "B", "JetBrainsMono-SemiBold.ttf"},
}

// addFonts adds the embedded fonts to f.
func addFonts(f *fpdf.Fpdf) error {
	for _, ff := range fontFiles {
		b, err := fontFS.ReadFile("fonts/" + ff.file)
		if err != nil {
			return err
		}
		f.AddUTF8FontFromBytes(ff.family, ff.style, b)
	}
	return f.Error()
}

// DefaultLogo is a copy of the bundled logo (a PNG), printed when the
// business has none or it cannot be used.
func DefaultLogo() []byte { return bytes.Clone(defaultLogo) }

// RenderInvoice renders an invoice's Document (InvoiceDocument) as an A4 PDF.
func RenderInvoice(ctx context.Context, d Document) ([]byte, error) {
	return render(ctx, d, DefaultLogos)
}

// RenderQuotation renders a quotation's Document (QuotationDocument).
func RenderQuotation(ctx context.Context, d Document) ([]byte, error) {
	return render(ctx, d, DefaultLogos)
}

// RenderReceipt renders a receipt's Document (ReceiptDocument).
func RenderReceipt(ctx context.Context, d Document) ([]byte, error) {
	return render(ctx, d, DefaultLogos)
}

// render lays out any of the three documents: they differ only in their
// Document (labels, a simple table, rows present or not).
func render(ctx context.Context, d Document, logos *LogoResolver) ([]byte, error) {
	logoURL := d.LogoURL
	logo := logos.Resolve(ctx, &logoURL)
	created := d.Date
	if created.IsZero() {
		created = reproEpoch
	}
	f := fpdf.New("P", "mm", "A4", "")
	if err := addFonts(f); err != nil {
		return nil, fmt.Errorf("pdf fonts: %w", err)
	}
	f.SetMargins(0, 0, 0)
	f.SetAutoPageBreak(false, 0)
	f.SetCompression(true)
	f.SetCatalogSort(true)
	f.SetCreationDate(created)
	f.SetModificationDate(reproEpoch)
	// The template's "{{ doc_title|title }} {{ ref }}".
	f.SetTitle(pyTitle(d.DocTitle)+" "+d.Ref, true)
	f.AddPage()
	// A first run counts the pages for the footer's "Page X of Y".
	layout(&canvas{pdf: f}, d, logo, layout(nil, d, logo, 0))
	var b bytes.Buffer
	if err := f.Output(&b); err != nil {
		return nil, fmt.Errorf("pdf generate: %w", err)
	}
	return b.Bytes(), nil
}
