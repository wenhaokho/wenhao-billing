package pdf

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // logo sizes
	_ "image/png"  // logo sizes
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/go-pdf/fpdf"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// The page is A4 with no margins, in millimetres; column widths are in mm.
// Sizes below are the mockup's (mm and pt).
const (
	gridSize = 210 // the page's width
	pageH    = 297 // the page's height
	padX     = 18  // the mockup's side padding
	contentW = gridSize - 2*padX
	hair     = 0.26 // a CSS 1px rule, in mm
)

// rgb is a colour.
type rgb struct{ r, g, b int }

func hexColor(s string) *rgb {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		panic(fmt.Sprintf("pdf: bad colour %q", s))
	}
	return &rgb{r: int(v >> 16 & 0xff), g: int(v >> 8 & 0xff), b: int(v & 0xff)}
}

// The brand palette and the few literal colours around it.
var (
	cRed       = hexColor("a51c30") // band, total bar, PPh
	cGold      = hexColor("d8bf84") // location line, titles on the band
	cInk       = hexColor("501921")
	cLine      = hexColor("e1d7c8")
	cMuted     = hexColor("75665f")
	cMutedSoft = hexColor("9d8a6e")
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
func chipColours(class string) (fg, bg *rgb) {
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
var summaryColour = map[string]*rgb{
	"discount": cPaidFg,
	"grossup":  cPaidFg,
	"wht":      cRed,
	"paid":     cPaidFg,
	"total":    cInk,
	"subtotal": cInk,
}

type alignment int

const (
	alignLeft alignment = iota
	alignRight
)

// textProp is a text style and where it sits in its cell: Top is the space
// above the first line, Left and Right inset it.
type textProp struct {
	Family           string
	Style            string // "" or "B": the family's lighter or heavier weight
	Size             float64
	Color            *rgb
	Align            alignment
	Top, Left, Right float64
}

// style is a text style: family, bold (the family's heavier weight), size in
// pt, colour.
func style(family string, bold bool, size float64, c *rgb) textProp {
	st := ""
	if bold {
		st = "B"
	}
	return textProp{Family: family, Style: st, Size: size, Color: c}
}

func upper(s string) string { return platform.PyUpper(s) }

// --- drawing ---------------------------------------------------------------

// area is a box on the page, in mm from the top left.
type area struct{ X, Y, Width, Height float64 }

// canvas draws on one fpdf document.
type canvas struct {
	pdf    *fpdf.Fpdf
	images int // names the registered images
}

// fontHeight is t's font size in mm: the height of a line before leading.
func fontHeight(t textProp) float64 { return t.Size / (72.0 / 25.4) }

func (p *canvas) line(x1, y1, x2, y2, thick float64, c *rgb) {
	p.pdf.SetDrawColor(c.r, c.g, c.b)
	p.pdf.SetLineWidth(thick)
	p.pdf.Line(x1, y1, x2, y2)
}

func (p *canvas) hline(x, y, length, thick float64, c *rgb) { p.line(x, y, x+length, y, thick, c) }

func (p *canvas) fillRect(x, y, w, h float64, c *rgb) {
	p.pdf.SetFillColor(c.r, c.g, c.b)
	p.pdf.Rect(x, y, w, h, "F")
}

// strokeRect outlines a rectangle; the stroke is centred on its edges.
func (p *canvas) strokeRect(x, y, w, h, thick float64, c *rgb) {
	p.pdf.SetDrawColor(c.r, c.g, c.b)
	p.pdf.SetLineWidth(thick)
	p.pdf.Rect(x, y, w, h, "D")
}

// roundedRect paints a rectangle with round corners of radius r, filled
// with fill and outlined with stroke (either may be nil).
func (p *canvas) roundedRect(x, y, w, h, r, thick float64, fill, stroke *rgb) {
	op := ""
	if fill != nil {
		p.pdf.SetFillColor(fill.r, fill.g, fill.b)
		op += "F"
	}
	if stroke != nil {
		p.pdf.SetDrawColor(stroke.r, stroke.g, stroke.b)
		p.pdf.SetLineWidth(thick)
		op += "D"
	}
	p.pdf.RoundedRect(x, y, w, h, r, "1234", op)
}

func (p *canvas) circle(x, y, r float64, c *rgb) {
	p.pdf.SetFillColor(c.r, c.g, c.b)
	p.pdf.Circle(x, y, r, "F")
}

// cellStyle is a row's or column's background and the rule on one of its
// edges.
type cellStyle struct {
	bg          *rgb
	border      byte // 'T' for a rule on the top edge, 'B' on the bottom, 0 none
	borderColor *rgb
	thick       float64
}

func (p *canvas) box(x, y, w, h float64, s *cellStyle) {
	if s == nil {
		return
	}
	if s.bg != nil {
		p.fillRect(x, y, w, h, s.bg)
	}
	switch s.border {
	case 'T':
		p.hline(x, y, w, s.thick, s.borderColor)
	case 'B':
		p.hline(x, y+h, w, s.thick, s.borderColor)
	}
}

// text prints s in c: its first baseline one font height under c's top
// plus t.Top, aligned in c's width less t's insets. A line too wide for that
// breaks at spaces, a font height apart; the components wrap their text
// themselves first (wrapText), so this is only a backstop.
func (p *canvas) text(s string, c *area, t textProp) {
	fh := fontHeight(t)
	top, left, right := min(t.Top, c.Height), min(t.Left, c.Width), min(t.Right, c.Width)
	width := max(c.Width-left-right, 0)
	x, y := c.X+left, c.Y+top+fh
	p.pdf.SetFont(t.Family, t.Style, t.Size)
	if t.Color != nil {
		p.pdf.SetTextColor(t.Color.r, t.Color.g, t.Color.b)
	}
	lines := []string{s}
	if p.pdf.GetStringWidth(s) > width {
		lines = p.breakAtSpaces(s, width)
	}
	for i, l := range lines {
		dx := 0.0
		if t.Align == alignRight {
			dx = width - p.pdf.GetStringWidth(l)
		}
		p.pdf.Text(x+dx, y+float64(i)*fh, l)
	}
}

// breakAtSpaces splits s into lines of at most width at its spaces; a word
// wider than width is a line of its own.
func (p *canvas) breakAtSpaces(s string, width float64) []string {
	var lines []string
	size := 0.0
	for _, w := range strings.Split(s, " ") {
		if w == "" {
			continue
		}
		piece, sep := " "+w, " "
		if len(lines) == 0 || lines[len(lines)-1] == "" {
			piece, sep = w, ""
		}
		pw := p.pdf.GetStringWidth(piece)
		if size+pw <= width {
			if len(lines) == 0 {
				lines = append(lines, "")
			}
			lines[len(lines)-1] += sep + w
			size += pw
			continue
		}
		lines = append(lines, w)
		size = p.pdf.GetStringWidth(w)
	}
	return lines
}

// image prints a PNG or JPEG as large as fits c, centred in it. An image
// fpdf cannot read leaves c empty.
func (p *canvas) image(b []byte, jpeg bool, c *area) {
	typ := "PNG"
	if jpeg {
		typ = "JPG"
	}
	p.images++
	name := "img" + strconv.Itoa(p.images)
	opts := fpdf.ImageOptions{ImageType: typ}
	info := p.pdf.RegisterImageOptionsReader(name, opts, bytes.NewReader(b))
	if info == nil || p.pdf.Err() {
		p.pdf.ClearError()
		return
	}
	iw, ih := info.Width(), info.Height()
	if iw <= 0 || ih <= 0 {
		return
	}
	w := c.Width
	if ih/iw > c.Height/c.Width {
		w = c.Height / (ih / iw)
	}
	h := w * ih / iw
	p.pdf.ImageOptions(name, c.X+(c.Width-w)/2, c.Y+(c.Height-h)/2, w, h, false, opts, 0, "")
}

// measurer is an fpdf document holding the embedded fonts, used only to
// measure text: the status chip and the strip's title are sized by their
// text, and text wraps in its column.
var measurer = sync.OnceValues(func() (*fpdf.Fpdf, error) {
	f := fpdf.New("P", "mm", "A4", "")
	if err := addFonts(f); err != nil {
		return nil, err
	}
	return f, f.Error()
})

var measureMu sync.Mutex

// textWidth is the printed width of s in mm, in t's family, style and size.
func textWidth(t textProp, s string) float64 {
	f, err := measurer()
	if err != nil {
		// Unreachable: render loads the same embedded files first and
		// returns their error, so no document reaches layout (nor this)
		// with fonts that fail to load. Panicking beats measuring every
		// string as 0 wide.
		panic(fmt.Sprintf("pdf: fonts: %v", err))
	}
	measureMu.Lock()
	defer measureMu.Unlock()
	f.SetFont(t.Family, t.Style, t.Size)
	return f.GetStringWidth(s)
}

// capHeight is the height of a capital letter in t's font, in mm.
func capHeight(t textProp) float64 {
	f, err := measurer()
	if err != nil {
		panic(fmt.Sprintf("pdf: fonts: %v", err)) // unreachable, as in textWidth
	}
	measureMu.Lock()
	defer measureMu.Unlock()
	return float64(f.GetFontDesc(t.Family, t.Style).CapHeight) / 1000 * fontHeight(t)
}

// centredTop is the Top that centres t's capitals in a box h tall: the
// labels in the chip and the pill are all capitals, so the font's full
// height (with room for accents above them) would sit them low.
func centredTop(t textProp, h float64) float64 {
	return (h+capHeight(t))/2 - fontHeight(t)
}

// wrapText is one paragraph's printed lines in width: it breaks at spaces,
// and breaks a word wider than the whole line (a long email or reference)
// between characters, so nothing runs past its column. A blank paragraph
// is one empty line.
func wrapText(t textProp, s string, width float64) []string {
	if strings.TrimSpace(s) == "" || width <= 0 || textWidth(t, s) <= width {
		return []string{s}
	}
	var lines []string
	cur := ""
	for _, w := range strings.Split(s, " ") {
		if w == "" {
			continue
		}
		if cur != "" && textWidth(t, cur+" "+w) <= width {
			cur += " " + w
			continue
		}
		if cur != "" {
			lines = append(lines, cur)
		}
		for textWidth(t, w) > width {
			n := fitPrefix(t, w, width)
			lines = append(lines, w[:n])
			w = w[n:]
		}
		cur = w
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// fitPrefix is the byte length of w's longest leading run of characters
// that fits width: at least one character, so a column narrower than a
// letter still moves on.
func fitPrefix(t textProp, w string, width float64) int {
	n := 0
	for i, r := range w {
		end := i + utf8.RuneLen(r)
		if n > 0 && textWidth(t, w[:end]) > width {
			break
		}
		n = end
	}
	return n
}

// --- rows, columns, components ---------------------------------------------

// component is something printed in a column's cell. height is what it
// needs in c's width; render draws it in c.
type component interface {
	height(c *area) float64
	render(p *canvas, c *area)
}

// column is a slice of a row, w mm wide; its components print over each
// other in its cell.
type column struct {
	w     float64
	style *cellStyle
	comps []component
}

func col(w int) *column { return &column{w: float64(w)} }

func (c *column) add(comps ...component) *column {
	c.comps = append(c.comps, comps...)
	return c
}

func (c *column) with(s *cellStyle) *column {
	c.style = s
	return c
}

func (c *column) height() float64 {
	h := 0.0
	for _, comp := range c.comps {
		h = max(h, comp.height(&area{Width: c.w, Height: pageH}))
	}
	return h
}

// row is columns side by side across the page, as tall as its tallest
// column, or fixed tall. A row never splits across pages.
type row struct {
	cols  []*column
	style *cellStyle // when set, it paints the whole row and the columns' own are ignored
	fixed float64    // the height, when above 0
}

func newRow(cols ...*column) *row { return &row{cols: cols} }

func (r *row) height() float64 {
	if r.fixed > 0 {
		return r.fixed
	}
	h := 0.0
	for _, c := range r.cols {
		h = max(h, c.height())
	}
	return h
}

// render draws the row with its top at y.
func (r *row) render(p *canvas, y float64) {
	h := r.height()
	if r.style != nil {
		p.box(0, y, gridSize, h, r.style)
	}
	x := 0.0
	for _, c := range r.cols {
		if r.style == nil {
			p.box(x, y, c.w, h, c.style)
		}
		cl := area{X: x, Y: y, Width: c.w, Height: h}
		for _, comp := range c.comps {
			comp.render(p, &cl)
		}
		x += c.w
	}
}

// leading is the space under each printed line, as a share of the font
// height (CSS line-height about 1.4).
const leading = 0.4

// block is lines of one text style. Each line is a paragraph: it wraps at
// spaces, and a blank line keeps its height (white-space: pre-line).
type block struct {
	lines []string
	prop  textProp // Top is space above the block; Left and Right inset it
	// after is the space under the block.
	after float64
}

// stack prints blocks one under the other in its cell; its height is what
// they need, so a row holding it grows with wrapped text.
type stack struct {
	blocks      []block
	top, bottom float64
}

func newStack(top, bottom float64, blocks ...block) *stack {
	return &stack{blocks: blocks, top: top, bottom: bottom}
}

// printLines prints one paragraph wrapped in the cell's width less t's
// insets, from top, a line every fh*(1+leading); it returns the height the
// lines take. p is nil when only measuring.
func printLines(p *canvas, c *area, t textProp, s string, top float64) float64 {
	step := fontHeight(t) * (1 + leading)
	lines := wrapText(t, s, c.Width-t.Left-t.Right)
	if p != nil {
		for i, l := range lines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			lp := t
			lp.Top = top + float64(i)*step
			p.text(l, c, lp)
		}
	}
	return float64(len(lines)) * step
}

// walk lays s out in c, drawing on p unless p is nil, and returns its
// height. The other components' walks work the same way.
func (s *stack) walk(p *canvas, c *area) float64 {
	y := s.top
	for _, b := range s.blocks {
		y += b.prop.Top
		for _, line := range b.lines {
			y += printLines(p, c, b.prop, line, y)
		}
		y += b.after
	}
	return y + s.bottom
}

func (s *stack) height(c *area) float64    { return s.walk(nil, c) }
func (s *stack) render(p *canvas, c *area) { s.walk(p, c) }

// seq prints components one under the other, each in the cell's width.
type seq []component

func (s seq) walk(p *canvas, c *area) float64 {
	y := 0.0
	for _, comp := range s {
		if comp == nil {
			continue
		}
		sub := *c
		sub.Y += y
		h := comp.height(&sub)
		if p != nil {
			sub.Height = h
			comp.render(p, &sub)
		}
		y += h
	}
	return y
}

func (s seq) height(c *area) float64    { return s.walk(nil, c) }
func (s seq) render(p *canvas, c *area) { s.walk(p, c) }

// gap is empty vertical space inside a seq.
type gap float64

func (g gap) height(*area) float64  { return float64(g) }
func (g gap) render(*canvas, *area) {}

// kv is key/value lines: keys in one column as wide as the widest, values
// beside them, wrapping in what is left.
type kv struct {
	rows   []MetaCell
	kp, vp textProp
	gap    float64 // between the columns
	rowGap float64 // between the lines
	// mono is the key whose value is a reference, printed in the
	// monospace ("" for none).
	mono string
}

func (k *kv) walk(p *canvas, c *area) float64 {
	keyW := 0.0
	for _, r := range k.rows {
		keyW = max(keyW, textWidth(k.kp, r.Key))
	}
	kp := k.kp
	y := 0.0
	for i, r := range k.rows {
		if i > 0 {
			y += k.rowGap
		}
		vp := k.vp
		if k.mono != "" && r.Key == k.mono {
			vp.Family = fontMono
		}
		vp.Left = keyW + k.gap
		if p != nil {
			kp.Top = y
			p.text(r.Key, c, kp)
		}
		y += printLines(p, c, vp, r.Value, y)
	}
	return y
}

func (k *kv) height(c *area) float64    { return k.walk(nil, c) }
func (k *kv) render(p *canvas, c *area) { k.walk(p, c) }

// --- paging ----------------------------------------------------------------

// pager prints rows down the page and breaks pages: a row that does not fit
// above the footer starts a new page (rows never split), which begins with
// the continuation strip (page 1 has the full header band instead); it
// repeats the items table's header row, keeps a section's rows together,
// and counts the pages for "Page X of Y". With no canvas it only counts.
type pager struct {
	p *canvas
	// header is the top of every page after the first.
	header []*row
	footer *row
	used   float64
	pages  int
}

func (pg *pager) footH() float64 { return pg.footer.height() }

func (pg *pager) headerHeight() float64 {
	h := 0.0
	for _, r := range pg.header {
		h += r.height()
	}
	return h
}

func (pg *pager) fits(h float64) bool { return h+pg.used+pg.footH() <= pageH }

func (pg *pager) put(r *row) {
	if pg.p != nil {
		r.render(pg.p, pg.used)
	}
	pg.used += r.height()
}

func (pg *pager) add(rows ...*row) {
	for _, r := range rows {
		if !pg.fits(r.height()) {
			pg.newPage()
		}
		pg.put(r)
	}
}

// endPage prints the footer at the foot of the page.
func (pg *pager) endPage() {
	if pg.p != nil {
		pg.footer.render(pg.p, pageH-pg.footH())
	}
}

// newPage ends the page and starts the next with the continuation header.
func (pg *pager) newPage() {
	pg.endPage()
	if pg.p != nil {
		pg.p.pdf.AddPage()
	}
	pg.pages++
	pg.used = 0
	for _, r := range pg.header {
		pg.put(r)
	}
}

// keep adds rows on one page: if they do not fit what is left but fit a
// fresh page, they start one.
func (pg *pager) keep(rows ...*row) {
	total := 0.0
	for _, r := range rows {
		total += r.height()
	}
	if !pg.fits(total) && total+pg.headerHeight()+pg.footH() <= pageH {
		pg.newPage()
	}
	pg.add(rows...)
}

// --- layout ----------------------------------------------------------------

func pad(w int) *column { return col(w) }

// band is a full-width row: side padding and content between.
func band(s *cellStyle, cols ...*column) *row {
	r := newRow(pad(padX))
	r.cols = append(r.cols, cols...)
	r.cols = append(r.cols, pad(padX))
	r.style = s
	return r
}

func spacer(h float64) *row { return &row{cols: []*column{col(gridSize)}, fixed: h} }

// Vertical rhythm, in mm.
const (
	sectionGap = 5   // between the bands, the table and the summary
	notesGap   = 6.5 // above Notes and Terms
	stripGap   = 5   // under the continuation strip
)

// layout builds the document: on page 1 the header band and the parties
// band, then the items, the summary with the payment panel, and the notes;
// later pages start with the continuation strip, and the footer is on every
// page. It draws on c, already on its first page, and returns the page
// count; with c nil it only counts. total is the page count for "Page X of
// Y" (a first run with c nil finds it).
func layout(c *canvas, d Document, logo []byte, total int) int {
	p := &pager{
		p: c, pages: 1,
		header: []*row{continuationStrip(d), spacer(stripGap)},
		footer: footerBand(d, &pageCount{total: total}),
	}

	p.add(headerBand(d, logo), partiesBand(documentParties(d)))
	if d.Receipt != nil {
		receiptBody(p, d)
	} else {
		p.add(spacer(sectionGap))
		items(p, d)
		p.add(spacer(sectionGap))
		p.keep(summaryBox(d, paymentPanel(d)))
		notes(p, d)
	}
	p.endPage()
	return p.pages
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
func headerBand(d Document, logo []byte) *row {
	title := style(fontBody, true, 7.5, cGold)
	title.Align = alignRight
	number := style(fontDisplay, false, 22, colWhite)
	number.Align, number.Top = alignRight, 1
	right := seq{
		gap(bandTop),
		newStack(0, 0, block{lines: []string{d.DocTitle}, prop: title}, block{lines: []string{d.Ref}, prop: number}),
		&datesChip{dates: headerDates(d), chip: statusChip(d), top: 1.5},
		gap(bandBottom),
	}
	left := seq{gap(bandTop), &brand{logo: logo, name: d.FootName, location: d.Location}, gap(bandBottom)}
	return band(&cellStyle{bg: cRed},
		col(headLeftW).add(left),
		col(contentW-headLeftW).add(right),
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

// box is the logo box's width and whether the logo is a JPEG (else a PNG);
// ok is false when the logo cannot be read.
func (b *brand) box() (w float64, jpeg, ok bool) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b.logo))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		return 0, false, false
	}
	inner := logoBoxH - 2*logoInset
	w = min(max(inner*float64(cfg.Width)/float64(cfg.Height), inner), logoMaxW-2*logoInset) + 2*logoInset
	return w, logoIsJPEG(b.logo), true
}

// The business name prints at nameMaxSize, smaller when a word would not fit
// its column (a wide logo leaves it about 33 mm), down to nameMinSize; only a
// word too wide even then breaks between characters.
const (
	nameMaxSize  = 15.0
	nameMinSize  = 10.0
	nameSizeStep = 0.5
)

// nameSize is the largest size, nameMaxSize down to nameMinSize, at which
// every word of the name fits width.
func (b *brand) nameSize(width float64) float64 {
	words := strings.Fields(b.name)
	for size := nameMaxSize; size > nameMinSize; size -= nameSizeStep {
		t := style(fontDisplay, true, size, colWhite)
		if !slices.ContainsFunc(words, func(w string) bool { return textWidth(t, w) > width }) {
			return size
		}
	}
	return nameMinSize
}

func (b *brand) text(width float64) *stack {
	name := style(fontDisplay, true, b.nameSize(width), colWhite)
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

func (b *brand) textCell(cell *area) area {
	w, _, ok := b.box()
	sub := *cell
	if ok {
		sub.X += w + 5
		sub.Width -= w + 5
	}
	return sub
}

func (b *brand) height(cell *area) float64 {
	sub := b.textCell(cell)
	return max(b.text(sub.Width).height(&sub), logoBoxH)
}

func (b *brand) render(p *canvas, cell *area) {
	if w, jpeg, ok := b.box(); ok {
		p.fillRect(cell.X, cell.Y, w, logoBoxH, colWhite)
		in := area{X: cell.X + logoInset, Y: cell.Y + logoInset, Width: w - 2*logoInset, Height: logoBoxH - 2*logoInset}
		p.image(b.logo, jpeg, &in)
	}
	sub := b.textCell(cell)
	t := b.text(sub.Width)
	if h := t.height(&sub); h < logoBoxH {
		sub.Y += (logoBoxH - h) / 2
	}
	t.render(p, &sub)
}

// chip is an outlined status chip: its label in capitals, in the colours of
// its status class.
type chip struct {
	label  string
	fg, bg *rgb
}

// statusChip is the header's chip for the document's status.
func statusChip(d Document) chip {
	fg, bg := chipColours(d.StatusClass)
	return chip{label: upper(d.StatusLabel), fg: fg, bg: bg}
}

const chipPadX, chipPadY = 2.5, 1

func (c chip) prop() textProp {
	t := style(fontBody, true, 7, c.fg)
	t.Left = chipPadX
	return t
}

func (c chip) size() (w, h float64) {
	t := c.prop()
	return textWidth(t, c.label) + 2*chipPadX, fontHeight(t) + 2*chipPadY
}

// datesChip is the date line with the status chip at its right end.
type datesChip struct {
	dates string
	chip  chip
	top   float64
}

func (dc *datesChip) datesProp() textProp {
	t := style(fontBody, false, 9, cBandText)
	t.Align = alignRight
	return t
}

const chipGap = 3 // between the dates and the chip

// place is where the dates and the chip go: on one line when they fit the
// cell, else the chip on its own line under the dates. y values are from the
// component's top.
func (dc *datesChip) place(cell *area) (datesY, datesRight, chipY, h float64) {
	cw, ch := dc.chip.size()
	dp := dc.datesProp()
	dh := fontHeight(dp)
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

func (dc *datesChip) height(cell *area) float64 {
	_, _, _, h := dc.place(cell)
	return h
}

func (dc *datesChip) render(p *canvas, cell *area) {
	datesY, datesRight, chipY, _ := dc.place(cell)
	if dc.chip.label != "" {
		cw, ch := dc.chip.size()
		x, y := cell.X+cell.Width-cw, cell.Y+chipY
		// A pill, as the mockup's badge (border-radius: 999px).
		p.roundedRect(x, y, cw, ch, ch/2, hair, dc.chip.bg, dc.chip.fg)
		cp := dc.chip.prop()
		cp.Top = centredTop(cp, ch)
		p.text(dc.chip.label, &area{X: x, Y: y, Width: cw, Height: ch}, cp)
	}
	dp := dc.datesProp()
	dp.Top, dp.Right = datesY, datesRight
	p.text(dc.dates, cell, dp)
}

// continuationStrip tops every page after the first: a compact red strip,
// the business name left, the document title and number right.
func continuationStrip(d Document) *row {
	const top, bottom = 4, 4
	name := style(fontDisplay, true, 10, colWhite)
	number := style(fontDisplay, false, 10, colWhite)
	number.Align = alignRight
	title := style(fontBody, true, 7.5, cGold)
	title.Align, title.Right, title.Top = alignRight, textWidth(number, d.Ref)+3, 0.9
	return band(&cellStyle{bg: cRed},
		col(contentW/2).add(newStack(top, bottom, block{lines: []string{d.FootName}, prop: name})),
		col(contentW-contentW/2).add(
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
	if d.Receipt != nil {
		// The receipt's third column is the payment: method, bank
		// reference, date received.
		return [3]partyCol{from, to, {Label: "Payment", Pairs: d.Receipt.Payment}}
	}
	ref := partyCol{Label: "Reference", Pairs: []MetaCell{{"Currency", d.Currency}}}
	if len(d.Meta) > 3 {
		ref.Pairs = append(ref.Pairs, d.Meta[3:]...)
	}
	return [3]partyCol{from, to, ref}
}

// partiesBand is the red band under the header, three columns under a faint
// rule.
func partiesBand(cols [3]partyCol) *row {
	const colGap = 8
	widths := [3]int{53, 52, 53} // with two gaps, contentW
	var out []*column
	for i, c := range cols {
		if i > 0 {
			out = append(out, pad(colGap))
		}
		cc := col(widths[i])
		if !c.empty() {
			cc.add(partyComponent(c))
		}
		out = append(out, cc)
	}
	return band(&cellStyle{bg: cRed, border: 'T', borderColor: cBandRule, thick: hair}, out...)
}

func partyComponent(c partyCol) component {
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
			gap: 4, rowGap: 0.3, mono: bankRefKey,
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

const colDesc = contentW - colNo - colQty - colRate - colAmount

// tableHeader is the items table's header row; it repeats on every page the
// table runs onto.
func tableHeader(d Document) *row {
	rule := &cellStyle{border: 'B', borderColor: cInk, thick: hair}
	cell := func(w int, label string, a alignment) *column {
		p := style(fontBody, true, 7, cMutedSoft)
		p.Align = a
		return col(w).with(rule).add(newStack(0, 2.5, block{lines: []string{upper(label)}, prop: p}))
	}
	return band(nil,
		cell(colNo, "#", alignLeft), cell(colDesc, "Description", alignLeft),
		cell(colQty, "Qty", alignRight), cell(colRate, "Rate", alignRight),
		cell(colAmount, "Amount · "+d.Currency, alignRight),
	)
}

// itemRow is one row of the items table; it is one row, so it never
// splits across pages.
func itemRow(i int, it Item) *row {
	rule := &cellStyle{border: 'B', borderColor: cLine, thick: hair}
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
	fig.Align = alignRight

	one := func(w int, s string, p textProp) *column {
		return col(w).with(rule).add(newStack(top, bottom, block{lines: []string{s}, prop: p}))
	}
	blocks := []block{{lines: splitLines(strings.ReplaceAll(it.Desc, "\r\n", "\n")), prop: desc}}
	if it.Sub != "" {
		blocks = append(blocks, block{lines: []string{it.Sub}, prop: sub})
	}
	return band(nil,
		one(colNo, rowNumber(i+1), no),
		col(colDesc).with(rule).add(newStack(top, bottom, blocks...)),
		one(colQty, it.Qty, fig), one(colRate, it.Rate, fig), one(colAmount, it.Amount, fig),
	)
}

// items is the items table; its header row starts every page it runs onto.
func items(p *pager, d Document) {
	head := tableHeader(d)
	if len(d.Items) == 0 {
		p.add(head)
		return
	}
	for i, it := range d.Items {
		r := itemRow(i, it)
		if i == 0 {
			p.keep(head, r)
			continue
		}
		if !p.fits(r.height()) {
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
func paymentPanel(d Document) component {
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
func summaryBox(d Document, left component) *row {
	lc := col(contentW - summaryW - lowerGap)
	if left != nil {
		lc.add(left)
	}
	return band(nil, lc, pad(lowerGap), col(summaryW).add(newSumBox(d)))
}

// sumBox draws the summary: label/value rows in a ruled box, then the bar.
type sumBox struct {
	rows          []SummaryRow
	label, amount string
}

// newSumBox is d's summary box. Its bar is the hero in capitals with
// "Currency HeroAmount"; on a receipt it is the balance due after the
// payment.
func newSumBox(d Document) *sumBox {
	if d.Receipt != nil {
		return &sumBox{rows: d.Summary, label: upper("Balance due"), amount: d.Receipt.BalanceDue}
	}
	amount := d.HeroAmount
	if d.Currency != "" {
		amount = d.Currency + " " + amount
	}
	return &sumBox{rows: d.Summary, label: upper(d.HeroLabel), amount: amount}
}

const sumPadX, sumPadY, sumRowGap, barPadY = 6, 4, 2, 4

func (s *sumBox) rowProps(r SummaryRow) (lp, vp textProp) {
	lp = style(fontBody, r.Class == "total", 9.5, cMuted)
	if r.Class == "note" {
		lp = style(fontBody, false, 8.5, cMutedSoft)
	}
	lp.Left = sumPadX
	c := summaryColour[r.Class]
	if c == nil {
		c = cInk
	}
	vp = style(fontDisplay, false, 9.5, c)
	vp.Align, vp.Right = alignRight, sumPadX
	return lp, vp
}

func (s *sumBox) barProps() (lp, vp textProp) {
	lp = style(fontBody, true, 7.5, cBandLabel)
	lp.Left = sumPadX
	vp = style(fontDisplay, false, 15, colWhite)
	vp.Align, vp.Right = alignRight, sumPadX
	return lp, vp
}

func (s *sumBox) walk(p *canvas, cell *area) float64 {
	y := 0.0
	if len(s.rows) > 0 {
		y = sumPadY
		for i, r := range s.rows {
			if i > 0 {
				y += sumRowGap
			}
			lp, vp := s.rowProps(r)
			lh, vh := fontHeight(lp), fontHeight(vp)
			h := max(lh, vh)
			if p != nil {
				lp.Top, vp.Top = y+h-lh, y+h-vh // one baseline
				p.text(r.Label, cell, lp)
				p.text(r.Value, cell, vp)
			}
			y += h
		}
		y += sumPadY
		if p != nil {
			p.strokeRect(cell.X, cell.Y, cell.Width, y, hair, cLine)
		}
	}
	lp, vp := s.barProps()
	lh, vh := fontHeight(lp), fontHeight(vp)
	barH := vh + 2*barPadY
	if p != nil {
		p.fillRect(cell.X, cell.Y+y, cell.Width, barH, cRed)
		bar := area{X: cell.X, Y: cell.Y + y, Width: cell.Width, Height: barH}
		vp.Top = barPadY
		lp.Top = barPadY + vh - lh // one baseline
		p.text(s.label, &bar, lp)
		p.text(s.amount, &bar, vp)
	}
	return y + barH
}

func (s *sumBox) height(cell *area) float64    { return s.walk(nil, cell) }
func (s *sumBox) render(p *canvas, cell *area) { s.walk(p, cell) }

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
	line := func(top float64, l, r []string, prop textProp) *row {
		cols := []*column{col(half).add(newStack(top, 0, block{lines: l, prop: prop}))}
		rc := col(contentW - colGap - half)
		if len(r) > 0 {
			rc.add(newStack(top, 0, block{lines: r, prop: prop}))
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

// --- receipt ---------------------------------------------------------------

// receiptBody is the receipt under its bands: the amount received, the
// invoice it is applied to, and the summary box ending in the balance due,
// with the withholding and balance notes beside it.
func receiptBody(p *pager, d Document) {
	p.add(spacer(8), band(nil, col(contentW).add(&received{d: d})), spacer(6))
	p.keep(appliedHeader(d), appliedRow(d))
	p.add(spacer(8))
	p.keep(summaryBox(d, receiptNotes(d)))
}

// received is the receipt's hero: "Amount received", the amount in the
// payment's currency, the amount in words, and at the right the green
// "Paid in full" pill when the invoice's balance after this payment is zero;
// an ink rule under it all.
type received struct{ d Document }

const (
	receivedBottom = 5 // under the words, over the rule
	pillPadX       = 4
	pillPadY       = 2
	pillDot        = 1.8
	pillGap        = 2
	pillSpace      = 8 // between the words and the pill
)

func (rc *received) text() *stack {
	label := style(fontBody, true, 7.5, cRed)
	amount := style(fontDisplay, false, 30, cInk)
	amount.Top = 1.5
	words := style(fontBody, false, 9, cMuted)
	words.Top = 1
	return newStack(0, receivedBottom,
		block{lines: []string{upper(rc.d.HeroLabel)}, prop: label},
		block{lines: []string{strings.TrimSpace(rc.d.Currency + " " + rc.d.HeroAmount)}, prop: amount},
		block{lines: []string{rc.d.Receipt.Words}, prop: words},
	)
}

func pillProp() textProp { return style(fontBody, true, 8, cPaidFg) }

var pillLabel = upper("Paid in full")

// pill is the pill's size, zero when the invoice is not paid in full.
func (rc *received) pill() (w, h float64) {
	if !rc.d.Receipt.PaidInFull {
		return 0, 0
	}
	t := pillProp()
	return pillPadX + pillDot + pillGap + textWidth(t, pillLabel) + pillPadX, fontHeight(t) + 2*pillPadY
}

func (rc *received) walk(p *canvas, cell *area) float64 {
	pw, ph := rc.pill()
	sub := *cell
	if pw > 0 {
		sub.Width -= pw + pillSpace
	}
	t := rc.text()
	h := max(t.height(&sub), ph+receivedBottom)
	if p != nil {
		t.render(p, &sub)
		if pw > 0 {
			// Bottom-aligned with the words.
			x, y := cell.X+cell.Width-pw, cell.Y+h-receivedBottom-ph
			// A pill with a round dot, as the mockup's badge.
			p.roundedRect(x, y, pw, ph, ph/2, 0, cPaidBg, nil)
			p.circle(x+pillPadX+pillDot/2, y+ph/2, pillDot/2, cPaidFg)
			lp := pillProp()
			lp.Left, lp.Top = pillPadX+pillDot+pillGap, centredTop(lp, ph)
			p.text(pillLabel, &area{X: x, Y: y, Width: pw, Height: ph}, lp)
		}
		p.hline(cell.X, cell.Y+h, cell.Width, hair, cInk)
	}
	return h
}

func (rc *received) height(cell *area) float64    { return rc.walk(nil, cell) }
func (rc *received) render(p *canvas, cell *area) { rc.walk(p, cell) }

// Column widths of the "Applied to" table, in mm.
const (
	colIssued   = 30
	colAppTotal = 36
	colAppPay   = 36
	colApplied  = contentW - colIssued - colAppTotal - colAppPay
)

// appliedHeader is the "Applied to" table's header; the two amount columns
// name their currencies, which differ on a cross-currency payment.
func appliedHeader(d Document) *row {
	rule := &cellStyle{border: 'B', borderColor: cLine, thick: hair}
	cell := func(w int, label string, a alignment) *column {
		p := style(fontBody, true, 7.5, cMutedSoft)
		p.Align = a
		return col(w).with(rule).add(newStack(0, 2.5, block{lines: []string{upper(label)}, prop: p}))
	}
	return band(nil,
		cell(colApplied, "Applied to", alignLeft),
		cell(colIssued, "Issued", alignLeft),
		cell(colAppTotal, "Invoice total · "+d.Receipt.InvoiceCurrency, alignRight),
		cell(colAppPay, "This payment · "+d.Currency, alignRight),
	)
}

// appliedRow is the invoice the payment is applied to: its number, issue
// date and total, and this payment (cash plus the withholding it settles).
func appliedRow(d Document) *row {
	rule := &cellStyle{border: 'B', borderColor: cLine, thick: hair}
	const top, bottom = 4, 2.6
	fig := style(fontDisplay, false, 9.5, cInk)
	fig.Align = alignRight
	date := style(fontBody, false, 9.5, cMuted)
	one := func(w int, s string, p textProp) *column {
		return col(w).with(rule).add(newStack(top, bottom, block{lines: []string{s}, prop: p}))
	}
	return band(nil,
		col(colApplied).with(rule).add(&refLine{prefix: "Invoice", ref: d.Receipt.Invoice, top: top, bottom: bottom}),
		one(colIssued, d.Receipt.Issued, date),
		one(colAppTotal, d.Receipt.InvoiceTotal, fig),
		one(colAppPay, d.Receipt.ThisPayment, fig),
	)
}

// refLine is a word and a reference after it in the monospace: "Invoice
// INV-0100". The reference wraps in what the word leaves.
type refLine struct {
	prefix, ref string
	top, bottom float64
}

func (r *refLine) props() (word, ref textProp) {
	word = style(fontBody, true, 9.5, cInk)
	ref = style(fontMono, false, 9, cInk)
	ref.Left = textWidth(word, r.prefix+" ")
	ref.Right = 4
	return word, ref
}

func (r *refLine) walk(p *canvas, cell *area) float64 {
	word, ref := r.props()
	wh, rh := fontHeight(word), fontHeight(ref)
	ref.Top = r.top + wh - rh // one baseline
	if p != nil {
		word.Top = r.top
		p.text(r.prefix, cell, word)
	}
	h := printLines(p, cell, ref, r.ref, ref.Top)
	return max(ref.Top+h, r.top+wh*(1+leading)) + r.bottom
}

func (r *refLine) height(cell *area) float64    { return r.walk(nil, cell) }
func (r *refLine) render(p *canvas, cell *area) { r.walk(p, cell) }

// receiptNotes is the left of the receipt's summary: the withholding note
// asking for the bukti potong (only when the payment carries withholding),
// then the balance note and the thanks.
func receiptNotes(d Document) component {
	label := style(fontBody, true, 7.5, cRed)
	text := style(fontBody, false, 9, cMuted)
	text.Top = 1.5
	var s seq
	if r := d.Receipt; r.WithholdingNote != "" {
		s = append(s, newStack(0, 5,
			block{lines: []string{upper("Withholding tax") + " (" + r.WithholdingLabel + ")"}, prop: label},
			block{lines: []string{r.WithholdingNote}, prop: text},
		))
	}
	if lines := splitLines(d.Notes); len(lines) > 0 {
		s = append(s, newStack(0, 0, block{lines: []string{upper("Note")}, prop: label}, block{lines: lines, prop: text}))
	}
	if len(s) == 0 {
		return nil
	}
	return s
}

// --- footer ----------------------------------------------------------------

// pageCount is the document's page count, known once the pager has laid out
// every row, and the page the footer is drawing.
type pageCount struct{ current, total int }

// footerBand is the footer on every page: the document's footer text, a
// rule, and under it the business and its contacts left, the document and
// "Page X of Y" right.
func footerBand(d Document, pages *pageCount) *row {
	return newRow(col(gridSize).add(&foot{d: d, pages: pages}))
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

func (f *foot) lineProps() (l, r textProp) {
	l = style(fontBody, false, 7.5, cMutedSoft)
	l.Left = padX
	r = l
	r.Left, r.Right, r.Align = 0, padX, alignRight
	return l, r
}

// right is the footer line's right side for page n of total.
func (f *foot) right(n, total int) string {
	return strings.TrimSpace(f.d.DocTitle+" "+f.d.Ref) + " · " + fmt.Sprintf("Page %d of %d", n, total)
}

// walk lays the footer out. The contact line on the left wraps in the room
// the right side leaves at its widest ("Page 999 of 999"), so the footer is
// as tall on every page whatever its page number, and a long business
// name or contact line takes more lines instead of running off the page.
func (f *foot) walk(p *canvas, cell *area) float64 {
	t := f.text()
	y := t.height(cell)
	l, r := f.lineProps()
	l.Right = padX + textWidth(r, f.right(999, 999)) + 6
	left := strings.Join(append([]string{f.d.FootName}, f.d.FootMeta...), " · ")
	if f.d.FootName == "" {
		left = strings.Join(f.d.FootMeta, " · ")
	}
	if p != nil {
		t.render(p, cell)
		p.hline(cell.X, cell.Y+y, cell.Width, hair, cLine)
		f.pages.current++
		r.Top = y + footPadY
		p.text(f.right(f.pages.current, f.pages.total), cell, r)
	}
	// The last line's leading is the bottom padding's business.
	lines := printLines(p, cell, l, left, y+footPadY) - fontHeight(l)*leading
	return y + footPadY + lines + footPadY
}

func (f *foot) height(cell *area) float64    { return f.walk(nil, cell) }
func (f *foot) render(p *canvas, cell *area) { f.walk(p, cell) }
