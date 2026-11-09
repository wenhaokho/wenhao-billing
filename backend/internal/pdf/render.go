package pdf

import (
	"bytes"
	"context"
	"embed"
	_ "embed" // the default logo
	"fmt"
	"sync"
	"time"

	"github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/pagesize"
	"github.com/johnfercher/maroto/v2/pkg/core/entity"
	"github.com/johnfercher/maroto/v2/pkg/fontrepository"
	"github.com/johnfercher/maroto/v2/pkg/props"
	"github.com/phpdave11/gofpdf"
)

// reproEpoch is the creation date of a Document without one, and every PDF's
// modification date: a document renders to the same bytes every time.
var reproEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// gofpdf (Maroto's writer) stamps the modification date with the clock and
// writes its catalogs in map order unless told otherwise; Maroto exposes
// neither, so the package defaults are set once, before any render.
func init() {
	gofpdf.SetDefaultModificationDate(reproEpoch)
	gofpdf.SetDefaultCatalogSort(true)
}

// The three families of the design (inventory §2.5), static TTF instances of
// Google Fonts' OFL releases (licences beside them in fonts/). Maroto has a
// normal and a bold style per family, so each family maps two weights.
const (
	fontBody    = "inter"         // Inter 400 / 600
	fontDisplay = "manrope"       // Manrope 700 / 800
	fontMono    = "jetbrainsmono" // JetBrains Mono 400 / 600
)

//go:embed fonts/*.ttf
var fontFS embed.FS

//go:embed default-logo.png
var defaultLogo []byte

var fontFiles = []struct {
	family string
	style  fontstyle.Type
	file   string
}{
	{fontBody, fontstyle.Normal, "Inter-Regular.ttf"},
	{fontBody, fontstyle.Bold, "Inter-SemiBold.ttf"},
	{fontDisplay, fontstyle.Normal, "Manrope-Bold.ttf"},
	{fontDisplay, fontstyle.Bold, "Manrope-ExtraBold.ttf"},
	{fontMono, fontstyle.Normal, "JetBrainsMono-Regular.ttf"},
	{fontMono, fontstyle.Bold, "JetBrainsMono-SemiBold.ttf"},
}

// customFonts are the embedded fonts, read once.
var customFonts = sync.OnceValues(func() ([]entity.CustomFont, error) {
	repo := fontrepository.New()
	for _, f := range fontFiles {
		b, err := fontFS.ReadFile("fonts/" + f.file)
		if err != nil {
			return nil, err
		}
		repo = repo.AddUTF8FontFromBytes(f.family, f.style, b)
	}
	return repo.Load()
})

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
	fonts, err := customFonts()
	if err != nil {
		return nil, fmt.Errorf("pdf fonts: %w", err)
	}
	logoURL := d.LogoURL
	logo := logos.Resolve(ctx, &logoURL)
	created := d.Date
	if created.IsZero() {
		created = reproEpoch
	}
	cfg := config.NewBuilder().
		WithPageSize(pagesize.A4).
		WithLeftMargin(0).WithTopMargin(0).WithRightMargin(0).WithBottomMargin(0).
		WithMaxGridSize(gridSize).
		WithCustomFonts(fonts).
		WithDefaultFont(&props.Font{Family: fontBody, Size: 10, Color: colInk}).
		// The template's "{{ doc_title|title }} {{ ref }}".
		WithTitle(pyTitle(d.DocTitle)+" "+d.Ref, true).
		WithCompression(true).
		WithCreationDate(created).
		Build()
	m := maroto.New(cfg)
	if err := layout(m, cfg, d, logo); err != nil {
		return nil, fmt.Errorf("pdf layout: %w", err)
	}
	doc, err := m.Generate()
	if err != nil {
		return nil, fmt.Errorf("pdf generate: %w", err)
	}
	return doc.GetBytes(), nil
}
