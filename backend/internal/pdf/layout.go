package pdf

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // logo sizes
	_ "image/png"  // logo sizes
	"strconv"
	"strings"

	"github.com/johnfercher/go-tree/node"
	"github.com/johnfercher/maroto/v2/pkg/components/col"
	mimage "github.com/johnfercher/maroto/v2/pkg/components/image"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/border"
	"github.com/johnfercher/maroto/v2/pkg/consts/extension"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/core/entity"
	"github.com/johnfercher/maroto/v2/pkg/props"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// The page is A4 with no margins, as the template's @page (TPL:104); the
// grid has one unit per millimetre, so column sizes are widths in mm.
const (
	gridSize   = 210
	padX       = 11 // the template's 40px side padding
	contentW   = gridSize - 2*padX
	sectionGap = 4.5
)

// mm is CSS px at 96 dpi in millimetres; pt is CSS px in points.
func mm(v float64) float64 { return v * 25.4 / 96 }
func pt(v float64) float64 { return v * 0.75 }

func hexColor(s string) *props.Color {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		panic(fmt.Sprintf("pdf: bad colour %q", s))
	}
	return &props.Color{Red: int(v >> 16 & 0xff), Green: int(v >> 8 & 0xff), Blue: int(v & 0xff)}
}

// The brand palette and the few literal colours around it.
var (
	cRed       = hexColor("a51c30") // band, total, PPh
	cGold      = hexColor("d8bf84")
	cInk       = hexColor("501921")
	cLine      = hexColor("e1d7c8")
	cMuted     = hexColor("75665f")
	cMutedSoft = hexColor("9d8a6e") //nolint:unused // used by the redrawn layout (Task 2)
	cSurface2  = hexColor("f6f2ea")
	cPaidFg    = hexColor("2f6b4f") // paid pill
	cPaidBg    = hexColor("e4efe7")
	colWhite   = hexColor("ffffff")
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

// font is a text style: family, bold (the family's heavier weight), size in
// CSS px, colour.
func font(family string, bold bool, sizePx float64, c *props.Color) props.Text {
	st := fontstyle.Normal
	if bold {
		st = fontstyle.Bold
	}
	return props.Text{Family: family, Style: st, Size: pt(sizePx), Color: c}
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

// pair is one summary line: a label on the left, a value on the right.
type pair struct {
	label, value string
	lp, vp       props.Text
}

// pairs prints label/value lines; it is the summary box.
type pairs struct {
	rows             []pair
	top, bottom, gap float64
}

func (s *pairs) walk(p core.Provider, cell *entity.Cell, draw bool) float64 {
	y := s.top
	for i, r := range s.rows {
		if i > 0 {
			y += s.gap
		}
		lh, vh := fontHeight(p, &r.lp), fontHeight(p, &r.vp)
		h := max(lh, vh)
		if draw {
			lp, vp := r.lp, r.vp
			lp.Top, vp.Top = y+h-lh, y+h-vh // one baseline
			p.AddText(r.label, cell, &lp)
			p.AddText(r.value, cell, &vp)
		}
		y += h
	}
	return y + s.bottom
}

func (s *pairs) GetHeight(p core.Provider, cell *entity.Cell) float64 { return s.walk(p, cell, false) }
func (s *pairs) Render(p core.Provider, cell *entity.Cell)            { s.walk(p, cell, true) }

func (s *pairs) SetConfig(cfg *entity.Config) {
	for i := range s.rows {
		s.rows[i].lp.MakeValid(cfg.DefaultFont)
		s.rows[i].vp.MakeValid(cfg.DefaultFont)
	}
}

func (s *pairs) GetStructure() *node.Node[core.Structure] {
	return node.New(core.Structure{Type: "pairs"})
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
// that does not fit to a new page whole (rows never split), and repeats the
// registered header and footer on every page. The pager mirrors that sum so
// it knows where a page breaks: to repeat the items table's header row on a
// new page, and to keep a section's rows together.
type pager struct {
	m      core.Maroto
	cfg    *entity.Config
	probe  *probe
	root   entity.Cell
	header []core.Row
	footH  float64
	used   float64
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
		if p.fits(h) {
			p.used += h
		} else {
			p.used = p.headerHeight() + h
		}
		p.m.AddRows(r)
	}
}

// newPage fills what is left of the page, so the next row starts a new one.
func (p *pager) newPage() {
	space := max(p.root.Height-p.used-p.footH-1e-6, 0)
	p.add(row.New(space).Add(col.New()))
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
func band(height float64, style *props.Cell, cols ...core.Col) core.Row {
	var r core.Row
	if height > 0 {
		r = row.New(height)
	} else {
		r = row.New()
	}
	r.Add(pad(padX))
	r.Add(cols...)
	r.Add(pad(padX))
	if style != nil {
		r.WithStyle(style)
	}
	return r
}

func spacer(h float64) core.Row { return row.New(h).Add(col.New()) }

func upper(s string) string { return platform.PyUpper(s) }

// layout builds the document: header and footer registered (on every page),
// then sections 2-6 of inventory §2.5.
func layout(m core.Maroto, cfg *entity.Config, d Document, logo []byte) error {
	pr := &probe{}
	footer := []core.Row{row.New().Add(col.New().Add(pr)), footerBand(d)}
	if err := m.RegisterFooter(footer...); err != nil {
		return err
	}
	header := headerRows(d, logo)
	if err := m.RegisterHeader(header...); err != nil {
		return err
	}
	p := &pager{
		m: m, cfg: cfg, probe: pr, header: header,
		root: entity.NewRootCell(cfg.Dimensions.Width, cfg.Dimensions.Height, *cfg.Margins),
	}
	for _, r := range footer {
		p.footH += p.height(r)
	}
	p.used = p.headerHeight()

	p.add(metaStrip(d), spacer(sectionGap))
	p.add(parties(d), spacer(sectionGap))
	items(p, d)
	p.add(spacer(sectionGap))
	p.keep(lower(d)...)
	notes(p, d)
	return nil
}

// headerRows is section 1: the gradient strip, the navy band with the logo,
// title, reference and status, and the 16px gap under it.
func headerRows(d Document, logo []byte) []core.Row {
	strip := row.New(mm(4)).Add(col.New().WithStyle(&props.Cell{BackgroundColor: cRed}))

	bandH := mm(20 + 76 + 20)
	const logoCol = 70
	logoCell := col.New(logoCol)
	if c := logoComponent(logo, logoCol, bandH); c != nil {
		logoCell.Add(c)
	}
	title := font(fontDisplay, true, 32, colWhite)
	title.Align, title.Top = align.Right, mm(22)
	ref := font(fontMono, false, 12.5, cGold)
	ref.Align, ref.Top = align.Right, mm(68)
	// The chip pairs are for light surfaces; on the red band the status is gold.
	status := font(fontDisplay, false, 11, cGold)
	status.Align, status.Top = align.Right, mm(88)
	right := col.New(contentW-logoCol).Add(
		newStack(0, 0,
			block{lines: []string{d.DocTitle}, prop: title},
		),
		newStack(0, 0, block{lines: []string{d.Ref}, prop: ref}),
		newStack(0, 0, block{lines: []string{"• " + upper(d.StatusLabel)}, prop: status}),
	)
	navy := &props.Cell{BackgroundColor: cRed}
	return []core.Row{strip, band(bandH, navy, logoCell, right), spacer(mm(16))}
}

// logoComponent fits the logo in a box 76px tall and at most 60 mm wide,
// centred on the band's height.
func logoComponent(logo []byte, colW, bandH float64) core.Component {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(logo))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		return nil
	}
	ratio := float64(cfg.Height) / float64(cfg.Width)
	h := mm(76)
	w := h / ratio
	if w > 60 {
		w, h = 60, 60*ratio
	}
	// Maroto sizes an image by a percentage of the cell along the side that
	// binds; mirror that choice (math.Resize).
	pct := w / colW * 100
	if ratio > bandH/colW {
		pct = h / bandH * 100
	}
	ext := extension.Png
	if logoIsJPEG(logo) {
		ext = extension.Jpg
	}
	return mimage.NewFromBytes(logo, ext, props.Rect{Top: (bandH - h) / 2, Percent: pct})
}

// footerBand is section 7: navy, 50px, the business name and contacts.
func footerBand(d Document) core.Row {
	name := font(fontDisplay, false, 12, colWhite)
	name.Top = mm(17)
	meta := font(fontMono, false, 11.5, cGold)
	meta.Align, meta.Top = align.Right, mm(18)
	const nameW = 90
	return band(mm(50), &props.Cell{BackgroundColor: cRed},
		col.New(nameW).Add(newStack(0, 0, block{lines: []string{d.FootName}, prop: name})),
		col.New(contentW-nameW).Add(newStack(0, 0, block{lines: []string{strings.Join(d.FootMeta, "    ")}, prop: meta})),
	)
}

// metaStrip is section 2: equal cells, key over value (three, or four with
// the PO/SO number).
func metaStrip(d Document) core.Row {
	n := max(len(d.Meta), 1)
	key := font(fontBody, true, 10.5, cMuted)
	key.Left, key.Right = mm(14), mm(14)
	val := font(fontMono, false, 14, cInk)
	val.Left, val.Right, val.Top = mm(14), mm(14), mm(6)
	box := &props.Cell{BorderType: border.Full, BorderColor: cLine, BorderThickness: mm(1)}
	var cols []core.Col
	used := 0
	for i, c := range d.Meta {
		w := contentW / n
		if i == len(d.Meta)-1 {
			w = contentW - used
		}
		used += w
		cols = append(cols, col.New(w).WithStyle(box).Add(newStack(mm(12), mm(12),
			block{lines: []string{upper(c.Key)}, prop: key},
			block{lines: []string{c.Value}, prop: val},
		)))
	}
	return band(0, nil, cols...)
}

// party is one box of section 3.
func party(label, name string, lines []string) *stack {
	l := font(fontDisplay, false, 11, cMuted)
	n := font(fontDisplay, false, 16, cInk)
	n.Top = mm(6)
	t := font(fontBody, false, 13, cMuted)
	t.Top = mm(4)
	for _, p := range []*props.Text{&l, &n, &t} {
		p.Left, p.Right = mm(18), mm(18)
	}
	return newStack(mm(16), mm(16),
		block{lines: []string{upper(label)}, prop: l},
		block{lines: []string{name}, prop: n},
		block{lines: lines, prop: t},
	)
}

// parties is section 3: from and to, side by side.
func parties(d Document) core.Row {
	const gap = 4
	w := (contentW - gap) / 2
	box := &props.Cell{BorderType: border.Full, BorderColor: cLine, BorderThickness: mm(1)}
	toBox := &props.Cell{BorderType: border.Full, BorderColor: cLine, BorderThickness: mm(1), BackgroundColor: cSurface2}
	return band(0, nil,
		col.New(w).WithStyle(box).Add(party(d.FromLabel, d.FromName, d.FromLines)),
		pad(gap),
		col.New(contentW-gap-w).WithStyle(toBox).Add(party(d.ToLabel, d.ToName, d.ToLines)),
	)
}

// Column widths of the items table, in mm.
const (
	colNo     = 9
	colQty    = 18
	colRate   = 32
	colAmount = 36
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
	h := font(fontDisplay, false, 10.5, cGold)
	h.Top = mm(11)
	cell := func(w int, label string, a align.Type) core.Col {
		p := h
		p.Align, p.Left, p.Right = a, mm(10), mm(10)
		return col.New(w).Add(newStack(0, mm(11), block{lines: []string{upper(label)}, prop: p}))
	}
	cols := []core.Col{cell(colNo, "#", align.Left), cell(descWidth(d), "Description", align.Left)}
	if !d.Simple {
		cols = append(cols, cell(colQty, "Qty", align.Right), cell(colRate, "Rate", align.Right))
	}
	cols = append(cols, cell(colAmount, "Amount · "+d.Currency, align.Right))
	return band(0, &props.Cell{BackgroundColor: cRed}, cols...)
}

// itemRow is one row of the items table; it is one Maroto row, so it never
// splits across pages.
func itemRow(d Document, i int, it Item) core.Row {
	style := &props.Cell{BorderType: border.Bottom, BorderColor: cLine, BorderThickness: mm(1)}
	if i%2 == 1 {
		style.BackgroundColor = cSurface2
	}
	const top, bottom = 3.4, 3.4
	no := font(fontMono, false, 12, cMuted)
	no.Left = mm(10)
	desc := font(fontBody, true, 13.5, cInk)
	desc.Left, desc.Right = mm(10), mm(10)
	sub := font(fontBody, false, 12, cMuted)
	sub.Left, sub.Right, sub.Top = mm(10), mm(10), 0.6
	fig := font(fontMono, false, 13, cInk)
	fig.Align, fig.Right = align.Right, mm(10)
	amt := font(fontMono, true, 13, cInk)
	amt.Align, amt.Right = align.Right, mm(10)

	one := func(w int, s string, p props.Text) core.Col {
		return col.New(w).WithStyle(style).Add(newStack(top, bottom, block{lines: []string{s}, prop: p}))
	}
	blocks := []block{{lines: splitLines(strings.ReplaceAll(it.Desc, "\r\n", "\n")), prop: desc}}
	if it.Sub != "" {
		blocks = append(blocks, block{lines: []string{it.Sub}, prop: sub})
	}
	cols := []core.Col{
		one(colNo, fmt.Sprintf("%02d", i+1), no),
		col.New(descWidth(d)).WithStyle(style).Add(newStack(top, bottom, blocks...)),
	}
	if !d.Simple {
		cols = append(cols, one(colQty, it.Qty, fig), one(colRate, it.Rate, fig))
	}
	cols = append(cols, one(colAmount, it.Amount, amt))
	return band(0, nil, cols...)
}

// items is section 4.
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

// lower is section 5: the payment panel (when there is one) beside the
// summary box, and the hero band under the summary. Its rows stay on one
// page.
func lower(d Document) []core.Row {
	const sumW, gap = 90, 4
	leftW := contentW - sumW - gap
	hasPay := d.PaymentInstructions != "" || d.NPWP != ""
	payTop := &props.Cell{BackgroundColor: cSurface2, BorderColor: cLine, BorderThickness: mm(1), BorderType: border.Left | border.Top | border.Right}
	payBottom := &props.Cell{BackgroundColor: cSurface2, BorderColor: cLine, BorderThickness: mm(1), BorderType: border.Left | border.Bottom | border.Right}

	left := col.New(leftW)
	leftHero := col.New(leftW)
	if hasPay {
		label := font(fontDisplay, false, 11, cMuted)
		text := font(fontBody, false, 12.5, cMuted)
		text.Top = mm(6)
		npwp := font(fontBody, true, 12.5, cMuted)
		npwp.Top = mm(10)
		for _, t := range []*props.Text{&label, &text, &npwp} {
			t.Left, t.Right = mm(16), mm(16)
		}
		blocks := []block{{lines: []string{upper("Payment Instructions")}, prop: label}}
		if d.PaymentInstructions != "" {
			blocks = append(blocks, block{lines: splitLines(d.PaymentInstructions), prop: text})
		}
		if d.NPWP != "" {
			blocks = append(blocks, block{lines: []string{d.NPWP}, prop: npwp})
		}
		left = left.WithStyle(payTop).Add(newStack(mm(14), mm(6), blocks...))
		leftHero = leftHero.WithStyle(payBottom)
	}

	var rows []pair
	for _, s := range d.Summary {
		lp := font(fontBody, false, 13, cMuted)
		lp.Left = mm(16)
		vp := font(fontMono, s.Class == "total", 13, summaryColour[s.Class])
		vp.Align, vp.Right = align.Right, mm(16)
		if s.Class == "total" {
			lp.Color, lp.Style = cInk, fontstyle.Bold
		}
		rows = append(rows, pair{label: s.Label, value: s.Value, lp: lp, vp: vp})
	}
	summary := col.New(sumW).WithStyle(&props.Cell{BorderType: border.Left | border.Top | border.Right, BorderColor: cLine, BorderThickness: mm(1)}).
		Add(&pairs{rows: rows, top: mm(14), bottom: mm(12), gap: mm(9)})

	heroH := mm(64)
	hl := font(fontDisplay, false, 11, colWhite)
	hl.Left, hl.Top = mm(16), mm(26)
	ha := font(fontMono, true, 22, colWhite)
	const codeW = 11
	ha.Align, ha.Right, ha.Top = align.Right, codeW+mm(16)-1, mm(18)
	hc := font(fontDisplay, false, 12, colWhite)
	hc.Align, hc.Right, hc.Top = align.Right, mm(16), mm(25)
	hero := col.New(sumW).WithStyle(&props.Cell{BackgroundColor: cRed}).Add(
		newStack(0, 0, block{lines: []string{upper(d.HeroLabel)}, prop: hl}),
		newStack(0, 0, block{lines: []string{d.HeroAmount}, prop: ha}),
		newStack(0, 0, block{lines: []string{d.Currency}, prop: hc}),
	)
	return []core.Row{
		band(0, nil, left, pad(gap), summary),
		band(heroH, nil, leftHero, pad(gap), hero),
	}
}

// notes is section 6 (Notes and Terms, side by side when both) and the
// document's footer text under them (Go change 10). Each paragraph is its
// own row, so long notes run onto the next page instead of under the footer.
func notes(p *pager, d Document) {
	left, right := splitLines(d.Notes), splitLines(d.Terms)
	leftLabel, rightLabel := "Notes", "Terms"
	if len(left) == 0 {
		left, right, leftLabel, rightLabel = right, nil, rightLabel, ""
	}
	if len(left) > 0 {
		p.add(spacer(sectionGap))
		widths := []int{contentW}
		if len(right) > 0 {
			widths = []int{(contentW - 4) / 2, 4, contentW - 4 - (contentW-4)/2}
		}
		label := font(fontDisplay, false, 11, cMuted)
		label.Left, label.Right = mm(16), mm(16)
		text := font(fontBody, false, 12.5, cMuted)
		text.Left, text.Right = mm(16), mm(16)
		panel := &props.Cell{BackgroundColor: cSurface2}
		line := func(top, bottom float64, l, r []string, prop props.Text) core.Row {
			cols := []core.Col{col.New(widths[0]).WithStyle(panel).Add(newStack(top, bottom, block{lines: l, prop: prop}))}
			if len(widths) == 3 {
				rc := col.New(widths[2])
				if len(r) > 0 || len(right) > 0 {
					rc.WithStyle(panel).Add(newStack(top, bottom, block{lines: r, prop: prop}))
				}
				cols = append(cols, pad(widths[1]), rc)
			}
			return band(0, nil, cols...)
		}
		var rightHead []string
		if rightLabel != "" && len(right) > 0 {
			rightHead = []string{upper(rightLabel)}
		}
		head := line(mm(14), mm(4), []string{upper(leftLabel)}, rightHead, label)
		n := max(len(left), len(right))
		at := func(ls []string, i int) []string {
			if i < len(ls) {
				return []string{ls[i]}
			}
			return nil
		}
		for i := range n {
			bottom := 0.0
			if i == n-1 {
				bottom = mm(14)
			}
			r := line(0, bottom, at(left, i), at(right, i), text)
			if i == 0 {
				p.keep(head, r)
				continue
			}
			p.add(r)
		}
	}
	if d.Footer != "" {
		p.add(spacer(sectionGap))
		f := font(fontBody, false, 11.5, cMuted)
		for _, l := range splitLines(d.Footer) {
			p.add(band(0, nil, col.New(contentW).Add(newStack(0, 0.4, block{lines: []string{l}, prop: f}))))
		}
	}
}
