package pdf

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // logo sizes
	_ "image/png"  // logo sizes
	"strconv"
	"strings"
	"sync"

	"github.com/johnfercher/go-tree/node"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/border"
	"github.com/johnfercher/maroto/v2/pkg/consts/breakline"
	"github.com/johnfercher/maroto/v2/pkg/consts/extension"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/linestyle"
	"github.com/johnfercher/maroto/v2/pkg/consts/orientation"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/core/entity"
	"github.com/johnfercher/maroto/v2/pkg/props"
	"github.com/phpdave11/gofpdf"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// The page is A4 with no margins; the grid has one unit per millimetre, so
// column sizes are widths in mm. Sizes below are the mockup's (mm and pt).
const (
	gridSize = 210
	padX     = 18 // the mockup's side padding
	contentW = gridSize - 2*padX
	hair     = 0.26 // a CSS 1px rule, in mm
)

func hexColor(s string) *props.Color {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		panic(fmt.Sprintf("pdf: bad colour %q", s))
	}
	return &props.Color{Red: int(v >> 16 & 0xff), Green: int(v >> 8 & 0xff), Blue: int(v & 0xff)}
}

// The brand palette and the few literal colours around it.
var (
	cRed       = hexColor("a51c30") // band, total bar, PPh
	cGold      = hexColor("d8bf84") // location line, titles on the band
	cInk       = hexColor("501921")
	cLine      = hexColor("e1d7c8")
	cMuted     = hexColor("75665f")
	cMutedSoft = hexColor("9d8a6e")
	cSurface2  = hexColor("f6f2ea") //nolint:unused // the receipt's tinted panels (Task 4)
	cPaidFg    = hexColor("2f6b4f") // paid pill
	cPaidBg    = hexColor("e4efe7")
	colWhite   = hexColor("ffffff")
	// On the red band: body text, labels, and the rule between the bands
	// (white at 22% over the red).
	cBandText  = hexColor("f2dde0")
	cBandLabel = hexColor("e4d2a9")
	cBandRule  = hexColor("b94e5e")
)

// chipColours is a status chip's text and fill per badge class; an unknown
// class gets draft's neutral pair.
func chipColours(class string) (fg, bg *props.Color) {
	switch class {
	case "paid":
		return cPaidFg, cPaidBg
	case "sent":
		return hexColor("3b5a7a"), hexColor("e3e9ef")
	case "partial":
		return hexColor("8a5a12"), hexColor("f6ecd6")
	case "overdue":
		return cRed, hexColor("f3dfe1")
	}
	return cMuted, hexColor("ede7de")
}

// summaryColour is a summary value's colour per row class.
var summaryColour = map[string]*props.Color{
	"discount": cPaidFg,
	"grossup":  cPaidFg,
	"wht":      cRed,
	"paid":     cPaidFg,
	"total":    cInk,
	"subtotal": cInk,
}

// style is a text style: family, bold (the family's heavier weight), size in
// pt, colour.
func style(family string, bold bool, size float64, c *props.Color) props.Text {
	st := fontstyle.Normal
	if bold {
		st = fontstyle.Bold
	}
	return props.Text{Family: family, Style: st, Size: size, Color: c, Align: align.Left, BreakLineStrategy: breakline.EmptySpaceStrategy}
}

func upper(s string) string { return platform.PyUpper(s) }

// --- drawing ---------------------------------------------------------------

func line(p core.Provider, x, y, length float64, o orientation.Type, thick float64, c *props.Color) {
	cell := &entity.Cell{X: x, Y: y, Width: length}
	if o == orientation.Vertical {
		cell.Width, cell.Height = 0, length
	}
	p.AddLine(cell, &props.Line{Color: c, Style: linestyle.Solid, Thickness: thick, Orientation: o, SizePercent: 100})
}

// fillRect paints a rectangle (a horizontal line as thick as the box: gofpdf
// draws butt caps, so it is exactly the box).
func fillRect(p core.Provider, x, y, w, h float64, c *props.Color) {
	line(p, x, y+h/2, w, orientation.Horizontal, h, c)
}

// strokeRect outlines a rectangle; the stroke is centred on its edges.
func strokeRect(p core.Provider, x, y, w, h, thick float64, c *props.Color) {
	line(p, x, y, w, orientation.Horizontal, thick, c)
	line(p, x, y+h, w, orientation.Horizontal, thick, c)
	line(p, x, y, h, orientation.Vertical, thick, c)
	line(p, x+w, y, h, orientation.Vertical, thick, c)
}

// measurer is a gofpdf document holding the embedded fonts, used only to
// measure text: Maroto's provider cannot say how wide a string is, and the
// status chip and the strip's title are sized by their text. Maroto writes
// with gofpdf and the same fonts, so the widths agree.
var measurer = sync.OnceValues(func() (*gofpdf.Fpdf, error) {
	f := gofpdf.New("P", "mm", "A4", "")
	for _, ff := range fontFiles {
		b, err := fontFS.ReadFile("fonts/" + ff.file)
		if err != nil {
			return nil, err
		}
		f.AddUTF8FontFromBytes(ff.family, string(ff.style), b)
	}
	return f, f.Error()
})

var measureMu sync.Mutex

// textWidth is the printed width of s in mm, in t's family, style and size.
func textWidth(t props.Text, s string) float64 {
	f, err := measurer()
	if err != nil {
		panic(fmt.Sprintf("pdf: fonts: %v", err))
	}
	measureMu.Lock()
	defer measureMu.Unlock()
	f.SetFont(t.Family, string(t.Style), t.Size)
	return f.GetStringWidth(s)
}

// --- custom components -----------------------------------------------------

// leading is the space under each printed line, as a share of the font
// height (CSS line-height about 1.4).
const leading = 0.4

// block is lines of one text style. Each line is a paragraph: it wraps at
// spaces, and a blank line keeps its height (white-space: pre-line).
type block struct {
	lines []string
	prop  props.Text // Top is space above the block; Left and Right inset it
	// after is the space under the block.
	after float64
}

// stack prints blocks one under the other in its cell; its height is what
// they need, so a row holding it grows with wrapped text. Maroto's own text
// component prints one paragraph only.
type stack struct {
	blocks      []block
	top, bottom float64
}

func newStack(top, bottom float64, blocks ...block) *stack {
	return &stack{blocks: blocks, top: top, bottom: bottom}
}

func fontHeight(p core.Provider, t *props.Text) float64 {
	return p.GetFontHeight(&props.Font{Family: t.Family, Style: t.Style, Size: t.Size, Color: t.Color})
}

// lineCount is the printed lines of one paragraph: at least one.
func lineCount(p core.Provider, t *props.Text, line string, width float64) int {
	if strings.TrimSpace(line) == "" {
		return 1
	}
	return max(p.GetLinesQuantity(line, t, width), 1)
}

func (s *stack) walk(p core.Provider, cell *entity.Cell, draw bool) float64 {
	y := s.top
	for _, b := range s.blocks {
		prop := b.prop
		fh := fontHeight(p, &prop)
		prop.VerticalPadding = fh * leading
		width := cell.Width - prop.Left - prop.Right
		y += b.prop.Top
		for _, line := range b.lines {
			n := lineCount(p, &prop, line, width)
			if draw && strings.TrimSpace(line) != "" {
				lp := prop
				lp.Top = y
				p.AddText(line, cell, &lp)
			}
			y += float64(n) * (fh + prop.VerticalPadding)
		}
		y += b.after
	}
	return y + s.bottom
}

func (s *stack) GetHeight(p core.Provider, cell *entity.Cell) float64 { return s.walk(p, cell, false) }
func (s *stack) Render(p core.Provider, cell *entity.Cell)            { s.walk(p, cell, true) }

func (s *stack) SetConfig(cfg *entity.Config) {
	for i := range s.blocks {
		s.blocks[i].prop.MakeValid(cfg.DefaultFont)
	}
}

func (s *stack) GetStructure() *node.Node[core.Structure] {
	var lines []string
	for _, b := range s.blocks {
		lines = append(lines, b.lines...)
	}
	return node.New(core.Structure{Type: "stack", Value: strings.Join(lines, "\n")})
}

// seq prints components one under the other, each in the cell's width.
type seq []core.Component

func (s seq) walk(p core.Provider, cell *entity.Cell, draw bool) float64 {
	y := 0.0
	for _, c := range s {
		if c == nil {
			continue
		}
		sub := *cell
		sub.Y += y
		h := c.GetHeight(p, &sub)
		if draw {
			sub.Height = h
			c.Render(p, &sub)
		}
		y += h
	}
	return y
}

func (s seq) GetHeight(p core.Provider, cell *entity.Cell) float64 { return s.walk(p, cell, false) }
func (s seq) Render(p core.Provider, cell *entity.Cell)            { s.walk(p, cell, true) }

func (s seq) SetConfig(cfg *entity.Config) {
	for _, c := range s {
		if c != nil {
			c.SetConfig(cfg)
		}
	}
}

func (s seq) GetStructure() *node.Node[core.Structure] {
	return node.New(core.Structure{Type: "seq"})
}

// gap is empty vertical space inside a seq.
type gap float64

func (g gap) GetHeight(core.Provider, *entity.Cell) float64 { return float64(g) }
func (g gap) Render(core.Provider, *entity.Cell)            {}
func (g gap) SetConfig(*entity.Config)                      {}
func (g gap) GetStructure() *node.Node[core.Structure] {
	return node.New(core.Structure{Type: "gap"})
}

// kv is key/value lines: keys in one column as wide as the widest, values
// beside them, wrapping in what is left.
type kv struct {
	rows   []MetaCell
	kp, vp props.Text
	gap    float64 // between the columns
	rowGap float64 // between the lines
}

func (k *kv) walk(p core.Provider, cell *entity.Cell, draw bool) float64 {
	keyW := 0.0
	for _, r := range k.rows {
		keyW = max(keyW, textWidth(k.kp, r.Key))
	}
	kp, vp := k.kp, k.vp
	vp.Left = keyW + k.gap
	kh, vh := fontHeight(p, &kp), fontHeight(p, &vp)
	kp.VerticalPadding, vp.VerticalPadding = kh*leading, vh*leading
	y := 0.0
	for i, r := range k.rows {
		if i > 0 {
			y += k.rowGap
		}
		n := lineCount(p, &vp, r.Value, cell.Width-vp.Left)
		if draw {
			kp.Top, vp.Top = y, y
			p.AddText(r.Key, cell, &kp)
			p.AddText(r.Value, cell, &vp)
		}
		y += float64(n) * (vh + vp.VerticalPadding)
	}
	return y
}

func (k *kv) GetHeight(p core.Provider, cell *entity.Cell) float64 { return k.walk(p, cell, false) }
func (k *kv) Render(p core.Provider, cell *entity.Cell)            { k.walk(p, cell, true) }

func (k *kv) SetConfig(cfg *entity.Config) {
	k.kp.MakeValid(cfg.DefaultFont)
	k.vp.MakeValid(cfg.DefaultFont)
}

func (k *kv) GetStructure() *node.Node[core.Structure] {
	return node.New(core.Structure{Type: "kv"})
}

// probe takes no space; measuring it hands over Maroto's provider, which
// the pager needs to measure rows before adding them.
type probe struct{ provider core.Provider }

func (pr *probe) GetHeight(p core.Provider, _ *entity.Cell) float64 { pr.provider = p; return 0 }
func (pr *probe) Render(core.Provider, *entity.Cell)                {}
func (pr *probe) SetConfig(*entity.Config)                          {}
func (pr *probe) GetStructure() *node.Node[core.Structure] {
	return node.New(core.Structure{Type: "probe"})
}

// --- paging ----------------------------------------------------------------

// pager adds rows to Maroto while keeping its arithmetic: Maroto moves a row
// that does not fit to a new page whole (rows never split) and repeats the
// registered footer on every page. The pager mirrors that sum so it knows
// where a page breaks: it ends the page itself and starts the next with the
// continuation strip (page 1 has the full header band instead), repeats the
// items table's header row, keeps a section's rows together, and counts the
// pages for "Page X of Y".
type pager struct {
	m     core.Maroto
	cfg   *entity.Config
	probe *probe
	root  entity.Cell
	// header is the top of every page after the first.
	header []core.Row
	footH  float64
	used   float64
	pages  int
}

func (p *pager) height(r core.Row) float64 {
	r.SetConfig(p.cfg)
	return r.GetHeight(p.probe.provider, &p.root)
}

func (p *pager) headerHeight() float64 {
	h := 0.0
	for _, r := range p.header {
		h += p.height(r)
	}
	return h
}

// fits is Maroto's own test (maroto.go addRow), term for term.
func (p *pager) fits(h float64) bool { return h+p.used+p.footH <= p.root.Height }

func (p *pager) add(rows ...core.Row) {
	for _, r := range rows {
		h := p.height(r)
		if !p.fits(h) {
			p.newPage()
		}
		p.used += h
		p.m.AddRows(r)
	}
}

// newPage fills what is left of the page and starts the next with the
// continuation header: the filler leaves less room than the header needs, so
// Maroto moves the header to a new page.
func (p *pager) newPage() {
	if space := p.root.Height - p.used - p.footH - 1e-6; space > 0 {
		p.m.AddRows(row.New(space).Add(col.New()))
	}
	p.m.AddRows(p.header...)
	p.used = p.headerHeight()
	p.pages++
}

// keep adds rows on one page: if they do not fit what is left but fit a
// fresh page, they start one.
func (p *pager) keep(rows ...core.Row) {
	total := 0.0
	for _, r := range rows {
		total += p.height(r)
	}
	if !p.fits(total) && total+p.headerHeight()+p.footH <= p.root.Height {
		p.newPage()
	}
	p.add(rows...)
}

// --- layout ----------------------------------------------------------------

func pad(n int) core.Col { return col.New(n) }

// band is a full-width row: side padding and content between.
func band(style *props.Cell, cols ...core.Col) core.Row {
	r := row.New()
	r.Add(pad(padX))
	r.Add(cols...)
	r.Add(pad(padX))
	if style != nil {
		r.WithStyle(style)
	}
	return r
}

func spacer(h float64) core.Row { return row.New(h).Add(col.New()) }

// Vertical rhythm, in mm.
const (
	sectionGap = 5   // between the bands, the table and the summary
	notesGap   = 6.5 // above Notes and Terms
	stripGap   = 5   // under the continuation strip
)

// layout builds the document: on page 1 the header band and the parties
// band, then the items, the summary with the payment panel, and the notes;
// later pages start with the continuation strip, and the footer is on every
// page.
func layout(m core.Maroto, cfg *entity.Config, d Document, logo []byte) error {
	pr := &probe{}
	pages := &pageCount{}
	footer := []core.Row{row.New().Add(col.New().Add(pr)), footerBand(d, pages)}
	if err := m.RegisterFooter(footer...); err != nil {
		return err
	}
	p := &pager{
		m: m, cfg: cfg, probe: pr, pages: 1,
		header: []core.Row{continuationStrip(d), spacer(stripGap)},
		root:   entity.NewRootCell(cfg.Dimensions.Width, cfg.Dimensions.Height, *cfg.Margins),
	}
	for _, r := range footer {
		p.footH += p.height(r)
	}

	p.add(headerBand(d, logo), partiesBand(documentParties(d)), spacer(sectionGap))
	items(p, d)
	p.add(spacer(sectionGap))
	p.keep(summaryBox(d, paymentPanel(d)))
	notes(p, d)
	pages.total = p.pages
	return nil
}

// --- header ----------------------------------------------------------------

// Header band sizes, in mm.
const (
	bandTop    = 11
	bandBottom = 8
	logoBoxH   = 14
	logoInset  = 1.5
	logoMaxW   = 46 // a wide logo's box grows up to this
	headLeftW  = 84
)

// headerBand is page 1's top: full-bleed red; on the left a white box
// holding the logo, the business name and the gold location line; on the
// right the document title, its number, the dates and the status chip.
func headerBand(d Document, logo []byte) core.Row {
	title := style(fontBody, true, 7.5, cGold)
	title.Align = align.Right
	number := style(fontDisplay, false, 22, colWhite)
	number.Align, number.Top = align.Right, 1
	right := seq{
		gap(bandTop),
		newStack(0, 0, block{lines: []string{d.DocTitle}, prop: title}, block{lines: []string{d.Ref}, prop: number}),
		&datesChip{dates: headerDates(d), chip: statusChip(d), top: 1.5},
		gap(bandBottom),
	}
	left := seq{gap(bandTop), &brand{logo: logo, name: d.FootName, location: d.Location}, gap(bandBottom)}
	return band(&props.Cell{BackgroundColor: cRed},
		col.New(headLeftW).Add(left),
		col.New(contentW-headLeftW).Add(right),
	)
}

// shortDateKey is a meta key as the header's date line words it.
var shortDateKey = map[string]string{
	"Issue Date":   "Issued",
	"Due Date":     "Due",
	"Valid Until":  "Valid until",
	"Payment Date": "Paid",
}

// headerDates is the header's date line: the meta cells after the number
// (issue and due dates, or the validity date), "Issued 23 Jul 2026 · Due
// 22 Aug 2026".
func headerDates(d Document) string {
	var parts []string
	for i, c := range d.Meta {
		if i == 0 || i > 2 {
			continue
		}
		k := c.Key
		if s, ok := shortDateKey[k]; ok {
			k = s
		}
		parts = append(parts, k+" "+c.Value)
	}
	return strings.Join(parts, " · ")
}

// brand is the logo box, and beside it the business name over the location
// line, centred on the box.
type brand struct {
	logo           []byte
	name, location string
}

// box is the logo box's width and the logo's extension (ok false when the
// logo cannot be read).
func (b *brand) box() (w float64, ext extension.Type, ok bool) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b.logo))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		return 0, "", false
	}
	inner := logoBoxH - 2*logoInset
	w = min(max(inner*float64(cfg.Width)/float64(cfg.Height), inner), logoMaxW-2*logoInset) + 2*logoInset
	ext = extension.Png
	if logoIsJPEG(b.logo) {
		ext = extension.Jpg
	}
	return w, ext, true
}

func (b *brand) text() *stack {
	name := style(fontDisplay, true, 15, colWhite)
	var blocks []block
	if b.name != "" {
		blocks = append(blocks, block{lines: []string{b.name}, prop: name})
	}
	if b.location != "" {
		loc := style(fontBody, true, 7.5, cGold)
		loc.Top = 0.3
		blocks = append(blocks, block{lines: []string{upper(b.location)}, prop: loc})
	}
	return newStack(0, 0, blocks...)
}

func (b *brand) textCell(cell *entity.Cell) entity.Cell {
	w, _, ok := b.box()
	sub := *cell
	if ok {
		sub.X += w + 5
		sub.Width -= w + 5
	}
	return sub
}

func (b *brand) GetHeight(p core.Provider, cell *entity.Cell) float64 {
	sub := b.textCell(cell)
	return max(b.text().GetHeight(p, &sub), logoBoxH)
}

func (b *brand) Render(p core.Provider, cell *entity.Cell) {
	if w, ext, ok := b.box(); ok {
		fillRect(p, cell.X, cell.Y, w, logoBoxH, colWhite)
		in := entity.Cell{X: cell.X + logoInset, Y: cell.Y + logoInset, Width: w - 2*logoInset, Height: logoBoxH - 2*logoInset}
		p.AddImageFromBytes(b.logo, &in, &props.Rect{Center: true, Percent: 100}, ext)
	}
	sub := b.textCell(cell)
	t := b.text()
	if h := t.GetHeight(p, &sub); h < logoBoxH {
		sub.Y += (logoBoxH - h) / 2
	}
	t.Render(p, &sub)
}

func (b *brand) SetConfig(*entity.Config) {}
func (b *brand) GetStructure() *node.Node[core.Structure] {
	return node.New(core.Structure{Type: "brand", Value: b.name})
}

// chip is an outlined status chip: its label in capitals, in the colours of
// its status class.
type chip struct {
	label  string
	fg, bg *props.Color
}

// statusChip is the header's chip for the document's status.
func statusChip(d Document) chip {
	fg, bg := chipColours(d.StatusClass)
	return chip{label: upper(d.StatusLabel), fg: fg, bg: bg}
}

const chipPadX, chipPadY = 2.5, 1

func (c chip) prop() props.Text {
	t := style(fontBody, true, 7, c.fg)
	t.Left = chipPadX
	return t
}

func (c chip) size(p core.Provider) (w, h float64) {
	t := c.prop()
	return textWidth(t, c.label) + 2*chipPadX, fontHeight(p, &t) + 2*chipPadY
}

// datesChip is the date line with the status chip at its right end.
type datesChip struct {
	dates string
	chip  chip
	top   float64
}

func (dc *datesChip) datesProp() props.Text {
	t := style(fontBody, false, 9, cBandText)
	t.Align = align.Right
	return t
}

const chipGap = 3 // between the dates and the chip

// place is where the dates and the chip go: on one line when they fit the
// cell, else the chip on its own line under the dates. y values are from the
// component's top.
func (dc *datesChip) place(p core.Provider, cell *entity.Cell) (datesY, datesRight, chipY, h float64) {
	cw, ch := dc.chip.size(p)
	dp := dc.datesProp()
	dh := fontHeight(p, &dp)
	if dc.chip.label == "" {
		return dc.top, 0, 0, dc.top + dh
	}
	if textWidth(dp, dc.dates)+chipGap+cw <= cell.Width {
		line := max(ch, dh)
		return dc.top + (line-dh)/2, cw + chipGap, dc.top + (line-ch)/2, dc.top + line
	}
	chipY = dc.top + dh*(1+leading)
	return dc.top, 0, chipY, chipY + ch
}

func (dc *datesChip) GetHeight(p core.Provider, cell *entity.Cell) float64 {
	_, _, _, h := dc.place(p, cell)
	return h
}

func (dc *datesChip) Render(p core.Provider, cell *entity.Cell) {
	datesY, datesRight, chipY, _ := dc.place(p, cell)
	if dc.chip.label != "" {
		cw, ch := dc.chip.size(p)
		x, y := cell.X+cell.Width-cw, cell.Y+chipY
		fillRect(p, x, y, cw, ch, dc.chip.bg)
		strokeRect(p, x, y, cw, ch, hair, dc.chip.fg)
		cp := dc.chip.prop()
		cp.Top = chipPadY
		p.AddText(dc.chip.label, &entity.Cell{X: x, Y: y, Width: cw, Height: ch}, &cp)
	}
	dp := dc.datesProp()
	dp.Top, dp.Right = datesY, datesRight
	p.AddText(dc.dates, cell, &dp)
}

func (dc *datesChip) SetConfig(*entity.Config) {}
func (dc *datesChip) GetStructure() *node.Node[core.Structure] {
	return node.New(core.Structure{Type: "datesChip", Value: dc.dates})
}

// continuationStrip tops every page after the first: a compact red strip,
// the business name left, the document title and number right.
func continuationStrip(d Document) core.Row {
	const top, bottom = 4, 4
	name := style(fontDisplay, true, 10, colWhite)
	number := style(fontDisplay, false, 10, colWhite)
	number.Align = align.Right
	title := style(fontBody, true, 7.5, cGold)
	title.Align, title.Right, title.Top = align.Right, textWidth(number, d.Ref)+3, 0.9
	return band(&props.Cell{BackgroundColor: cRed},
		col.New(contentW/2).Add(newStack(top, bottom, block{lines: []string{d.FootName}, prop: name})),
		col.New(contentW-contentW/2).Add(
			newStack(top, bottom, block{lines: []string{d.Ref}, prop: number}),
			newStack(top, bottom, block{lines: []string{d.DocTitle}, prop: title}),
		),
	)
}

// --- parties ---------------------------------------------------------------

// partyCol is one column of the parties band: a label, then a name and lines
// of text, then key/value pairs. A column with nothing under its label is
// left out, label and all.
type partyCol struct {
	Label string
	Name  string
	Lines []string
	Pairs []MetaCell
}

func (c partyCol) empty() bool { return c.Name == "" && len(c.Lines) == 0 && len(c.Pairs) == 0 }

// documentParties is an invoice's or quotation's From, Bill to and
// Reference: the currency and the meta cells beyond the number and dates
// (the PO/SO number). A missing party (its name a dash) is left out.
func documentParties(d Document) [3]partyCol {
	name := func(s string) string {
		if s == Dash {
			return ""
		}
		return s
	}
	from := partyCol{Label: d.FromLabel, Name: name(d.FromName), Lines: d.FromLines}
	to := partyCol{Label: d.ToLabel, Name: name(d.ToName), Lines: d.ToLines}
	if d.Simple {
		// The receipt still prints its labels in capitals; its own band
		// replaces this one.
		from.Label, to.Label = upper(from.Label), upper(to.Label)
	}
	ref := partyCol{Label: "Reference", Pairs: []MetaCell{{"Currency", d.Currency}}}
	if len(d.Meta) > 3 {
		ref.Pairs = append(ref.Pairs, d.Meta[3:]...)
	}
	return [3]partyCol{from, to, ref}
}

// partiesBand is the red band under the header, three columns under a faint
// rule.
func partiesBand(cols [3]partyCol) core.Row {
	const colGap = 8
	widths := [3]int{53, 52, 53} // with two gaps, contentW
	var out []core.Col
	for i, c := range cols {
		if i > 0 {
			out = append(out, pad(colGap))
		}
		cc := col.New(widths[i])
		if !c.empty() {
			cc.Add(partyComponent(c))
		}
		out = append(out, cc)
	}
	return band(&props.Cell{BackgroundColor: cRed, BorderType: border.Top, BorderColor: cBandRule, BorderThickness: hair}, out...)
}

func partyComponent(c partyCol) core.Component {
	label := style(fontBody, true, 7, cBandLabel)
	blocks := []block{{lines: []string{c.Label}, prop: label}}
	if c.Name != "" {
		n := style(fontBody, true, 8, colWhite)
		n.Top = 1
		blocks = append(blocks, block{lines: []string{c.Name}, prop: n})
	}
	if len(c.Lines) > 0 {
		blocks = append(blocks, block{lines: c.Lines, prop: style(fontBody, false, 8, cBandText)})
	}
	s := seq{newStack(3, 0, blocks...)}
	if len(c.Pairs) > 0 {
		s = append(s, gap(1), &kv{
			rows: c.Pairs, kp: style(fontBody, false, 8, cBandLabel), vp: style(fontBody, false, 8, colWhite),
			gap: 4, rowGap: 0.3,
		})
	}
	return append(s, gap(7))
}

// --- items -----------------------------------------------------------------

// Column widths of the items table, in mm.
const (
	colNo     = 8
	colQty    = 12
	colRate   = 30
	colAmount = 30
)

func descWidth(d Document) int {
	if d.Simple {
		return contentW - colNo - colAmount
	}
	return contentW - colNo - colQty - colRate - colAmount
}

// tableHeader is the items table's header row; it repeats on every page the
// table runs onto.
func tableHeader(d Document) core.Row {
	rule := &props.Cell{BorderType: border.Bottom, BorderColor: cInk, BorderThickness: hair}
	cell := func(w int, label string, a align.Type) core.Col {
		p := style(fontBody, true, 7, cMutedSoft)
		p.Align = a
		return col.New(w).WithStyle(rule).Add(newStack(0, 2.5, block{lines: []string{upper(label)}, prop: p}))
	}
	cols := []core.Col{cell(colNo, "#", align.Left), cell(descWidth(d), "Description", align.Left)}
	if !d.Simple {
		cols = append(cols, cell(colQty, "Qty", align.Right), cell(colRate, "Rate", align.Right))
	}
	cols = append(cols, cell(colAmount, "Amount · "+d.Currency, align.Right))
	return band(nil, cols...)
}

// itemRow is one row of the items table; it is one Maroto row, so it never
// splits across pages.
func itemRow(d Document, i int, it Item) core.Row {
	rule := &props.Cell{BorderType: border.Bottom, BorderColor: cLine, BorderThickness: hair}
	// The last line's leading already sits under it, so the bottom pad is
	// the mockup's 2.2 mm less that.
	const top, bottom = 2.2, 1.0
	no := style(fontMono, false, 8.5, cMutedSoft)
	no.Top = 0.3
	desc := style(fontBody, true, 9, cInk)
	desc.Right = 4
	sub := style(fontBody, false, 8.5, cMuted)
	sub.Right, sub.Top = 4, 0.3
	fig := style(fontDisplay, false, 9, cInk)
	fig.Align = align.Right

	one := func(w int, s string, p props.Text) core.Col {
		return col.New(w).WithStyle(rule).Add(newStack(top, bottom, block{lines: []string{s}, prop: p}))
	}
	blocks := []block{{lines: splitLines(strings.ReplaceAll(it.Desc, "\r\n", "\n")), prop: desc}}
	if it.Sub != "" {
		blocks = append(blocks, block{lines: []string{it.Sub}, prop: sub})
	}
	cols := []core.Col{
		one(colNo, rowNumber(i+1), no),
		col.New(descWidth(d)).WithStyle(rule).Add(newStack(top, bottom, blocks...)),
	}
	if !d.Simple {
		cols = append(cols, one(colQty, it.Qty, fig), one(colRate, it.Rate, fig))
	}
	cols = append(cols, one(colAmount, it.Amount, fig))
	return band(nil, cols...)
}

// items is the items table; its header row starts every page it runs onto.
func items(p *pager, d Document) {
	head := tableHeader(d)
	if len(d.Items) == 0 {
		p.add(head)
		return
	}
	for i, it := range d.Items {
		r := itemRow(d, i, it)
		if i == 0 {
			p.keep(head, r)
			continue
		}
		if !p.fits(p.height(r)) {
			p.newPage()
			p.add(head)
		}
		p.add(r)
	}
}

// --- summary ---------------------------------------------------------------

const (
	summaryW = 84
	lowerGap = 10
)

// paymentPanel is the payment instructions and the NPWP line, or nil when
// the document has neither.
func paymentPanel(d Document) core.Component {
	if d.PaymentInstructions == "" && d.NPWP == "" {
		return nil
	}
	label := style(fontBody, true, 7.5, cRed)
	blocks := []block{{lines: []string{upper("Payment Instructions")}, prop: label}}
	text := style(fontBody, false, 8.5, cInk)
	text.Top = 1.5
	if d.PaymentInstructions != "" {
		blocks = append(blocks, block{lines: splitLines(d.PaymentInstructions), prop: text})
	}
	if d.NPWP != "" {
		blocks = append(blocks, block{lines: []string{d.NPWP}, prop: text})
	}
	return newStack(0, 0, blocks...)
}

// summaryBox is the lower block: left (the payment panel, or nil) beside the
// boxed summary rows, which end in the red bar with the hero label and
// amount. It is one row, kept on one page.
func summaryBox(d Document, left core.Component) core.Row {
	lc := col.New(contentW - summaryW - lowerGap)
	if left != nil {
		lc.Add(left)
	}
	return band(nil, lc, pad(lowerGap), col.New(summaryW).Add(&sumBox{d: d}))
}

// sumBox draws the summary: label/value rows in a ruled box, then the bar.
type sumBox struct{ d Document }

const sumPadX, sumPadY, sumRowGap, barPadY = 6, 4, 2, 4

func (s *sumBox) rowProps(r SummaryRow) (lp, vp props.Text) {
	lp = style(fontBody, r.Class == "total", 9.5, cMuted)
	lp.Left = sumPadX
	c := summaryColour[r.Class]
	if c == nil {
		c = cInk
	}
	vp = style(fontDisplay, false, 9.5, c)
	vp.Align, vp.Right = align.Right, sumPadX
	return lp, vp
}

func (s *sumBox) barProps() (lp, vp props.Text) {
	lp = style(fontBody, true, 7.5, cBandLabel)
	lp.Left = sumPadX
	vp = style(fontDisplay, false, 15, colWhite)
	vp.Align, vp.Right = align.Right, sumPadX
	return lp, vp
}

func (s *sumBox) walk(p core.Provider, cell *entity.Cell, draw bool) float64 {
	y := 0.0
	if len(s.d.Summary) > 0 {
		y = sumPadY
		for i, r := range s.d.Summary {
			if i > 0 {
				y += sumRowGap
			}
			lp, vp := s.rowProps(r)
			lh, vh := fontHeight(p, &lp), fontHeight(p, &vp)
			h := max(lh, vh)
			if draw {
				lp.Top, vp.Top = y+h-lh, y+h-vh // one baseline
				p.AddText(r.Label, cell, &lp)
				p.AddText(r.Value, cell, &vp)
			}
			y += h
		}
		y += sumPadY
		if draw {
			strokeRect(p, cell.X, cell.Y, cell.Width, y, hair, cLine)
		}
	}
	lp, vp := s.barProps()
	lh, vh := fontHeight(p, &lp), fontHeight(p, &vp)
	barH := vh + 2*barPadY
	if draw {
		fillRect(p, cell.X, cell.Y+y, cell.Width, barH, cRed)
		bar := entity.Cell{X: cell.X, Y: cell.Y + y, Width: cell.Width, Height: barH}
		vp.Top = barPadY
		lp.Top = barPadY + vh - lh // one baseline
		p.AddText(upper(s.d.HeroLabel), &bar, &lp)
		amount := s.d.HeroAmount
		if s.d.Currency != "" {
			amount = s.d.Currency + " " + amount
		}
		p.AddText(amount, &bar, &vp)
	}
	return y + barH
}

func (s *sumBox) GetHeight(p core.Provider, cell *entity.Cell) float64 { return s.walk(p, cell, false) }
func (s *sumBox) Render(p core.Provider, cell *entity.Cell)            { s.walk(p, cell, true) }
func (s *sumBox) SetConfig(*entity.Config)                             {}
func (s *sumBox) GetStructure() *node.Node[core.Structure] {
	return node.New(core.Structure{Type: "summary"})
}

// --- notes -----------------------------------------------------------------

// notes is Notes and Terms, side by side when both. Each paragraph is its
// own row, so long notes run onto the next page instead of under the footer.
func notes(p *pager, d Document) {
	left, right := splitLines(d.Notes), splitLines(d.Terms)
	leftLabel, rightLabel := "Notes", "Terms"
	if len(left) == 0 {
		left, right, leftLabel, rightLabel = right, nil, rightLabel, ""
	}
	if len(left) == 0 {
		return
	}
	p.add(spacer(notesGap))
	const colGap = 10
	half := (contentW - colGap) / 2
	label := style(fontBody, true, 7.5, cRed)
	text := style(fontBody, false, 8.5, cMuted)
	line := func(top float64, l, r []string, prop props.Text) core.Row {
		cols := []core.Col{col.New(half).Add(newStack(top, 0, block{lines: l, prop: prop}))}
		rc := col.New(contentW - colGap - half)
		if len(r) > 0 {
			rc.Add(newStack(top, 0, block{lines: r, prop: prop}))
		}
		return band(nil, append(cols, pad(colGap), rc)...)
	}
	var rightHead []string
	if rightLabel != "" && len(right) > 0 {
		rightHead = []string{upper(rightLabel)}
	}
	head := line(0, []string{upper(leftLabel)}, rightHead, label)
	at := func(ls []string, i int) []string {
		if i < len(ls) {
			return []string{ls[i]}
		}
		return nil
	}
	for i := range max(len(left), len(right)) {
		top := 0.0
		if i == 0 {
			top = 1.5
		}
		r := line(top, at(left, i), at(right, i), text)
		if i == 0 {
			p.keep(head, r)
			continue
		}
		p.add(r)
	}
}

// --- footer ----------------------------------------------------------------

// pageCount is the document's page count, known once the pager has laid out
// every row, and the page the footer is drawing.
type pageCount struct{ current, total int }

// footerBand is the footer on every page: the document's footer text, a
// rule, and under it the business and its contacts left, the document and
// "Page X of Y" right.
func footerBand(d Document, pages *pageCount) core.Row {
	return row.New().Add(col.New(gridSize).Add(&foot{d: d, pages: pages}))
}

type foot struct {
	d     Document
	pages *pageCount
}

const footTextGap, footPadY = 3, 3.5

func (f *foot) text() *stack {
	t := style(fontBody, false, 8.5, cMuted)
	t.Left, t.Right = padX, padX
	if f.d.Footer == "" {
		return newStack(0, 0)
	}
	return newStack(0, footTextGap, block{lines: splitLines(f.d.Footer), prop: t})
}

func (f *foot) lineProps() (l, r props.Text) {
	l = style(fontBody, false, 7.5, cMutedSoft)
	l.Left = padX
	r = l
	r.Left, r.Right, r.Align = 0, padX, align.Right
	return l, r
}

func (f *foot) walk(p core.Provider, cell *entity.Cell, draw bool) float64 {
	t := f.text()
	y := t.GetHeight(p, cell)
	l, r := f.lineProps()
	h := fontHeight(p, &l)
	if draw {
		t.Render(p, cell)
		line(p, cell.X, cell.Y+y, cell.Width, orientation.Horizontal, hair, cLine)
		f.pages.current++
		left := strings.Join(append([]string{f.d.FootName}, f.d.FootMeta...), " · ")
		if f.d.FootName == "" {
			left = strings.Join(f.d.FootMeta, " · ")
		}
		right := strings.TrimSpace(f.d.DocTitle+" "+f.d.Ref) + " · " + fmt.Sprintf("Page %d of %d", f.pages.current, f.pages.total)
		l.Top, r.Top = y+footPadY, y+footPadY
		// Keep the right text clear of the left.
		l.Right = padX + textWidth(r, right) + 6
		p.AddText(left, cell, &l)
		p.AddText(right, cell, &r)
	}
	return y + footPadY + h + footPadY
}

func (f *foot) GetHeight(p core.Provider, cell *entity.Cell) float64 { return f.walk(p, cell, false) }
func (f *foot) Render(p core.Provider, cell *entity.Cell)            { f.walk(p, cell, true) }
func (f *foot) SetConfig(*entity.Config)                             {}
func (f *foot) GetStructure() *node.Node[core.Structure] {
	return node.New(core.Structure{Type: "footer"})
}
