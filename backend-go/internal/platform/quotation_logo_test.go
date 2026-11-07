package platform

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestQuotationLogoValidation(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var pngBytes, jpegBytes bytes.Buffer
	if err := png.Encode(&pngBytes, img); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&jpegBytes, img, nil); err != nil {
		t.Fatal(err)
	}
	data := func(mime string, b []byte) string {
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
	}
	oversized := append([]byte(nil), pngBytes.Bytes()...)
	binary.BigEndian.PutUint32(oversized[16:20], 100000)
	binary.BigEndian.PutUint32(oversized[20:24], 100000)
	binary.BigEndian.PutUint32(oversized[29:33], crc32.ChecksumIEEE(oversized[12:29]))
	validSVG := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20"><rect width="20" height="20" fill="#ff0000"/></svg>`)
	for _, tc := range []struct {
		name, value string
		fallback    bool
	}{
		{"png", data("image/png", pngBytes.Bytes()), false}, {"jpeg", data("image/jpeg", jpegBytes.Bytes()), false},
		{"truncated png", data("image/png", pngBytes.Bytes()[:len(pngBytes.Bytes())-8]), true},
		{"truncated jpeg", data("image/jpeg", jpegBytes.Bytes()[:len(jpegBytes.Bytes())-8]), true},
		{"oversized dimensions", data("image/png", oversized), true},
		{"oversized data", data("image/png", bytes.Repeat([]byte{'x'}, (5<<20)+1)), true},
		{"svg", data("image/svg+xml", validSVG), false},
		{"unsupported SVG text", data("image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><text>logo</text></svg>`)), true},
		{"svg dash attributes", data("image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><path d="M0 0L1 1" stroke-dasharray=".016 .016" stroke-dashoffset="30000000"/></svg>`)), true},
		{"svg dash style", data("image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><path d="M0 0L1 1" style="stroke-dasharray:.016 .016;stroke-dashoffset:30000000"/></svg>`)), true},
		{"svg dash css", data("image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><defs><style>.a{stroke:black;stroke-dasharray:.016 .016;stroke-dashoffset:30000000}</style></defs><path class="a" d="M0 0L1 1"/></svg>`)), true},
		{"recursive svg", data("image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><defs><g id="a"><use href="#a"/></g></defs><use href="#a"/></svg>`)), true},
		{"external svg", data("image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><image href="http://127.0.0.1/no"/></svg>`)), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := quotationLogo(context.Background(), tc.value)
			if bytes.Equal(got, quotationDefaultLogo) != tc.fallback {
				t.Fatalf("fallback=%v", bytes.Equal(got, quotationDefaultLogo))
			}
			if _, _, err := image.Decode(bytes.NewReader(got)); err != nil {
				t.Fatal(err)
			}
		})
	}
	var redirected atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/target", http.StatusFound)
			return
		}
		redirected.Store(true)
		w.Write(pngBytes.Bytes())
	}))
	defer srv.Close()
	got, _ := quotationLogo(context.Background(), srv.URL+"/redirect")
	if !bytes.Equal(got, quotationDefaultLogo) || redirected.Load() {
		t.Fatal("followed redirect")
	}
	got, _ = quotationLogo(context.Background(), srv.URL+"/target")
	if !bytes.Equal(got, pngBytes.Bytes()) {
		t.Fatal("local image not loaded")
	}
}
