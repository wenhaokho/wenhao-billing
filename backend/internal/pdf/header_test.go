package pdf

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"slices"
	"strings"
	"testing"
)

// widePNG is a PNG about 4.3 times as wide as it is tall, the shape of the
// wenhao.id wordmark, so the logo box takes its widest size.
func widePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 430, 100))
	for x := range 430 {
		for y := range 100 {
			img.Set(x, y, color.RGBA{0x50, 0x19, 0x21, 0xff})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Beside a wide logo the business name shrinks until each word fits its
// column, so no word is split across lines ("Developmen" / "t"). The check
// is on the wrapped lines: PDF text extraction joins a split word back up.
func TestHeaderNameKeepsWholeWordsBesideAWideLogo(t *testing.T) {
	t.Parallel()
	logo := widePNG(t)
	for _, name := range []string{"Wenhao Development", "Wenhao Development Pte Ltd", "PT Wenhao Teknologi Indonesia"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b := &brand{logo: logo, name: name}
			w, _, ok := b.box()
			if !ok {
				t.Fatal("wide logo does not decode")
			}
			eq(t, "logo box takes its widest size", w, float64(logoMaxW))
			width := b.textCell(&area{Width: headLeftW}).Width
			lines := wrapText(style(fontDisplay, true, b.nameSize(width), colWhite), name, width)
			words := strings.Fields(name)
			for _, line := range lines {
				for _, w := range strings.Fields(line) {
					if !slices.Contains(words, w) {
						t.Errorf("line %q splits a word of %q (lines %q)", line, name, lines)
					}
				}
			}
		})
	}
}

// The shrink stops at the floor size: a single word too wide even then still
// breaks between characters rather than running past its column.
func TestHeaderNameSizeHasAFloor(t *testing.T) {
	t.Parallel()
	b := &brand{name: "Wenhao Development"}
	eq(t, "wide column keeps the full size", b.nameSize(80), nameMaxSize)
	if s := b.nameSize(33); s >= nameMaxSize || s < nameMinSize {
		t.Errorf("33mm column: size %v, want below %v and at least %v", s, nameMaxSize, nameMinSize)
	}
	eq(t, "impossible column stops at the floor", b.nameSize(2), nameMinSize)
}

// The chip's and the pill's capitals sit in the middle of their box: as much
// room above the capitals as under the baseline.
func TestChipAndPillLabelsAreCentred(t *testing.T) {
	t.Parallel()
	for _, f := range fontFiles {
		tp := style(f.family, f.style == "B", 10, cInk)
		if r := capHeight(tp) / fontHeight(tp); r < 0.6 || r > 0.8 {
			t.Errorf("%s %q: cap height %.2f of the font size, want a real cap height", f.family, f.style, r)
		}
	}
	c := chip{label: "PARTIALLY PAID", fg: cInk}
	_, ch := c.size()
	pill := pillProp()
	for name, b := range map[string]struct {
		t textProp
		h float64
	}{"chip": {c.prop(), ch}, "pill": {pill, fontHeight(pill) + 2*pillPadY}} {
		baseline := centredTop(b.t, b.h) + fontHeight(b.t)
		above, below := baseline-capHeight(b.t), b.h-baseline
		if d := above - below; d > 1e-9 || d < -1e-9 {
			t.Errorf("%s: %.3f mm above the capitals, %.3f mm under them", name, above, below)
		}
	}
}
