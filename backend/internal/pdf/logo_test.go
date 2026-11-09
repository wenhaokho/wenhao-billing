package pdf

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testPNG is a w x h PNG.
func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		img.Set(x, 0, color.NRGBA{R: 200, A: 255})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// padPNG inserts a tEXt chunk of n bytes before IEND: the PNG still decodes
// but weighs n bytes more.
func padPNG(t *testing.T, p []byte, n int) []byte {
	t.Helper()
	iend := p[len(p)-12:]
	data := append([]byte("Comment\x00"), bytes.Repeat([]byte("x"), n)...)
	var chunk bytes.Buffer
	_ = binary.Write(&chunk, binary.BigEndian, uint32(len(data)))
	body := append([]byte("tEXt"), data...)
	chunk.Write(body)
	_ = binary.Write(&chunk, binary.BigEndian, crc32.ChecksumIEEE(body))
	out := append(append(append([]byte(nil), p[:len(p)-12]...), chunk.Bytes()...), iend...)
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("padded PNG does not decode: %v", err)
	}
	return out
}

func size(t *testing.T, b []byte) image.Point {
	t.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("logo does not decode: %v", err)
	}
	return image.Pt(cfg.Width, cfg.Height)
}

func isDefault(b []byte) bool { return bytes.Equal(b, DefaultLogo()) }

// logoServer serves body with content type ct on every path and counts hits.
func logoServer(t *testing.T, ct string, body []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", ct)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestPassthroughDataURI(t *testing.T) {
	t.Parallel()
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNG(t, 7, 3))
	got := (&LogoResolver{}).Resolve(t.Context(), &uri)
	eq(t, "size", size(t, got), image.Pt(7, 3))
}

func TestBadDataURIUsesDefault(t *testing.T) {
	t.Parallel()
	// Python passed "data:image/png;base64,AAAA" through; Go decodes it, and
	// three zero bytes are no image.
	for _, uri := range []string{"data:image/png;base64,AAAA", "data:image/png;base64,***", "data:,plain"} {
		if !isDefault((&LogoResolver{}).Resolve(t.Context(), &uri)) {
			t.Errorf("%q: not the default logo", uri)
		}
	}
}

func TestNoneBusinessUsesDefault(t *testing.T) {
	t.Parallel()
	if !isDefault((&LogoResolver{}).Resolve(t.Context(), nil)) {
		t.Error("nil logo_url: not the default logo")
	}
}

func TestBlankLogoUsesDefault(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "ftp://example.com/logo.png", "/static/logo.png"} {
		if !isDefault((&LogoResolver{}).Resolve(t.Context(), &s)) {
			t.Errorf("%q: not the default logo", s)
		}
	}
}

func TestHTTPFetchSuccess(t *testing.T) {
	t.Parallel()
	srv, hits := logoServer(t, "image/png", testPNG(t, 5, 4))
	u := srv.URL + "/logo.png"
	eq(t, "size", size(t, (&LogoResolver{}).Resolve(t.Context(), &u)), image.Pt(5, 4))
	eq(t, "hits", hits.Load(), int32(1))
}

func TestHTTPFetchJPEG(t *testing.T) {
	t.Parallel()
	// Maroto embeds PNG and JPEG; a JPEG is kept as it is.
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 6, 2)), nil); err != nil {
		t.Fatal(err)
	}
	srv, _ := logoServer(t, "image/jpeg", b.Bytes())
	u := srv.URL + "/logo.jpg"
	if got := (&LogoResolver{}).Resolve(t.Context(), &u); !bytes.Equal(got, b.Bytes()) {
		t.Errorf("JPEG not kept as served (default logo: %v)", isDefault(got))
	}
}

func TestHTTPFetchFailureFallsBackToDefault(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	for _, u := range []string{srv.URL + "/logo.png", down.URL + "/logo.png"} {
		if !isDefault((&LogoResolver{}).Resolve(t.Context(), &u)) {
			t.Errorf("%s: not the default logo", u)
		}
	}
}

func TestHTTPFetchNonImageFallsBackToDefault(t *testing.T) {
	t.Parallel()
	// Python embedded the HTML labelled image/png; Go sniffs the bytes.
	for _, ct := range []string{"text/html", "image/png"} {
		srv, _ := logoServer(t, ct, []byte("<html>not an image</html>"))
		u := srv.URL + "/logo.png"
		if !isDefault((&LogoResolver{}).Resolve(t.Context(), &u)) {
			t.Errorf("HTML served as %s: not the default logo", ct)
		}
	}
}

func TestLogoTypeIsSniffedFromTheBytes(t *testing.T) {
	t.Parallel()
	// A PNG served under a wrong content type is still a PNG.
	srv, _ := logoServer(t, "application/octet-stream", testPNG(t, 9, 2))
	u := srv.URL + "/logo"
	eq(t, "size", size(t, (&LogoResolver{}).Resolve(t.Context(), &u)), image.Pt(9, 2))

	// A GIF is an image Maroto cannot embed.
	var g bytes.Buffer
	if err := gif.Encode(&g, image.NewPaletted(image.Rect(0, 0, 2, 2), []color.Color{color.Black}), nil); err != nil {
		t.Fatal(err)
	}
	gs, _ := logoServer(t, "image/png", g.Bytes())
	gu := gs.URL + "/logo.png"
	if !isDefault((&LogoResolver{}).Resolve(t.Context(), &gu)) {
		t.Error("GIF: not the default logo")
	}
}

func TestLogoSizeCapFallsBack(t *testing.T) {
	t.Parallel()
	big := padPNG(t, testPNG(t, 3, 3), logoMaxBytes)
	srv, _ := logoServer(t, "image/png", big)
	u := srv.URL + "/big.png"
	if !isDefault((&LogoResolver{}).Resolve(t.Context(), &u)) {
		t.Error("a logo over 2 MB: not the default logo")
	}
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(big)
	if !isDefault((&LogoResolver{}).Resolve(t.Context(), &uri)) {
		t.Error("a data URI over 2 MB: not the default logo")
	}
	// Just under the cap is fine.
	ok := padPNG(t, testPNG(t, 3, 3), logoMaxBytes-200)
	srv2, _ := logoServer(t, "image/png", ok)
	u2 := srv2.URL + "/ok.png"
	eq(t, "size", size(t, (&LogoResolver{}).Resolve(t.Context(), &u2)), image.Pt(3, 3))
}

func TestLogoFetchTimesOut(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release); srv.Close() })
	u := srv.URL + "/slow.png"
	r := &LogoResolver{Timeout: 50 * time.Millisecond}
	start := time.Now()
	if !isDefault(r.Resolve(t.Context(), &u)) {
		t.Error("slow logo: not the default logo")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
	if (&LogoResolver{}).timeout() != 5*time.Second {
		t.Error("default timeout is not 5 s")
	}
}

func TestLogoIsCachedForTenMinutes(t *testing.T) {
	t.Parallel()
	srv, hits := logoServer(t, "image/png", testPNG(t, 2, 2))
	u := srv.URL + "/logo.png"
	var mu sync.Mutex
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	r := &LogoResolver{Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now }}
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }

	r.Resolve(t.Context(), &u)
	advance(9*time.Minute + 59*time.Second)
	r.Resolve(t.Context(), &u)
	eq(t, "hits within 10 minutes", hits.Load(), int32(1))
	advance(2 * time.Second)
	r.Resolve(t.Context(), &u)
	eq(t, "hits after 10 minutes", hits.Load(), int32(2))

	// A failure is not cached: the next render tries again.
	bad, badHits := logoServer(t, "text/html", []byte("<html>"))
	bu := bad.URL + "/logo.png"
	r.Resolve(t.Context(), &bu)
	r.Resolve(t.Context(), &bu)
	eq(t, "failed fetches", badHits.Load(), int32(2))
}

func TestLogoCacheIsBounded(t *testing.T) {
	t.Parallel()
	srv, _ := logoServer(t, "image/png", testPNG(t, 2, 2))
	r := &LogoResolver{}
	for i := range logoCacheMax + 10 {
		u := srv.URL + "/logo.png?v=" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		r.Resolve(t.Context(), &u)
	}
	r.mu.Lock()
	n := len(r.cache)
	r.mu.Unlock()
	if n > logoCacheMax {
		t.Errorf("cache holds %d logos, max %d", n, logoCacheMax)
	}
}
