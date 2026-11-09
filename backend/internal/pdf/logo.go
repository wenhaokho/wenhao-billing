package pdf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// logoMaxBytes caps a logo, fetched or decoded from a data URI (Go change 5).
	logoMaxBytes = 2 << 20
	// logoTimeout bounds a logo fetch, as Python's httpx timeout did.
	logoTimeout = 5 * time.Second
	// logoTTL is how long a resolved logo is reused for the same logo_url.
	logoTTL = 10 * time.Minute
	// logoCacheMax bounds the cache; one business has one logo, so this is
	// room for edits within the TTL.
	logoCacheMax = 64
	// logoMaxSide and logoMaxPixels bound a logo's dimensions, read from its
	// header before it is decoded: a small file can claim a huge image.
	logoMaxSide   = 4096
	logoMaxPixels = 16 << 20
)

var (
	errLogoTooLarge = errors.New("logo is larger than 2 MB")
	errLogoType     = errors.New("logo is not a PNG or JPEG image")
	errLogoPixels   = errors.New("logo is larger than 4096 x 4096 pixels")
)

// logoClient fetches logos when the resolver has no Client: no redirects
// (Python's httpx.get followed none), the 5 s timeout.
var logoClient = &http.Client{Timeout: logoTimeout, CheckRedirect: noRedirect}

// noRedirect stops at a redirect: the 3xx is the response, and not a logo.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// LogoResolver turns a business's logo_url into PNG or JPEG bytes Maroto can
// embed (Maroto takes png, jpg and jpeg only). A data: URI is decoded; an
// http(s) URL is fetched with a 5 s timeout and a 2 MB cap; the type is
// sniffed from the bytes, never taken from a header. No logo_url, anything
// else, or any failure is the bundled default logo, logged. Resolved logos
// are cached per logo_url for 10 minutes; failures are not cached. Only the
// logo_url is ever fetched: the renderer reads nothing else from outside.
// The zero value is ready to use.
type LogoResolver struct {
	// Client fetches http(s) logos; nil is a plain client. Whatever the
	// client, redirects are not followed and the 5 s timeout applies.
	Client *http.Client
	// Timeout overrides the 5 s fetch timeout (tests).
	Timeout time.Duration
	// Logger receives fallbacks; nil is slog's default.
	Logger *slog.Logger
	// Now is the cache's clock; nil is time.Now.
	Now func() time.Time

	mu    sync.Mutex
	cache map[[sha256.Size]byte]cachedLogo
}

type cachedLogo struct {
	logo    []byte
	expires time.Time
}

// DefaultLogos is the resolver the package's Render functions use.
var DefaultLogos = &LogoResolver{}

func (r *LogoResolver) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return logoTimeout
}

func (r *LogoResolver) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *LogoResolver) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

// Resolve is the logo to print for logoURL (Python resolve_logo).
func (r *LogoResolver) Resolve(ctx context.Context, logoURL *string) []byte {
	if logoURL == nil || *logoURL == "" {
		return DefaultLogo()
	}
	u := *logoURL
	key := sha256.Sum256([]byte(u))
	if logo, ok := r.cached(key); ok {
		return bytes.Clone(logo)
	}
	var raw []byte
	var err error
	switch {
	case hasPrefixFold(u, "data:"):
		raw, err = decodeDataURI(u)
	case hasPrefixFold(u, "http://"), hasPrefixFold(u, "https://"):
		raw, err = r.fetch(ctx, u)
	default:
		return DefaultLogo()
	}
	var logo []byte
	if err == nil {
		logo, err = embeddable(raw)
	}
	if err != nil {
		r.logger().WarnContext(ctx, "failed to resolve logo; using default", "logo_url", redactLogoURL(u), "err", err)
		return DefaultLogo()
	}
	r.store(key, logo)
	return bytes.Clone(logo)
}

func (r *LogoResolver) cached(key [sha256.Size]byte) ([]byte, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cache[key]
	if !ok || !r.now().Before(c.expires) {
		return nil, false
	}
	return c.logo, true
}

func (r *LogoResolver) store(key [sha256.Size]byte, logo []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if r.cache == nil {
		r.cache = map[[sha256.Size]byte]cachedLogo{}
	}
	if len(r.cache) >= logoCacheMax {
		for k, c := range r.cache {
			if !now.Before(c.expires) {
				delete(r.cache, k)
			}
		}
	}
	for k := range r.cache {
		if len(r.cache) < logoCacheMax {
			break
		}
		delete(r.cache, k)
	}
	r.cache[key] = cachedLogo{logo: logo, expires: now.Add(logoTTL)}
}

func (r *LogoResolver) fetch(ctx context.Context, u string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	client := logoClient
	if r.Client != nil {
		c := *r.Client
		c.CheckRedirect = noRedirect
		client = &c
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("logo fetch: status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, logoMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > logoMaxBytes {
		return nil, errLogoTooLarge
	}
	return b, nil
}

// decodeDataURI is the bytes of a data: URI, base64 or percent-encoded. The
// declared media type is ignored: the bytes are sniffed.
func decodeDataURI(u string) ([]byte, error) {
	meta, data, ok := strings.Cut(u[len("data:"):], ",")
	if !ok {
		return nil, errors.New("data URI has no comma")
	}
	if base64.StdEncoding.DecodedLen(len(data)) > logoMaxBytes+3 && strings.HasSuffix(strings.ToLower(meta), ";base64") {
		return nil, errLogoTooLarge
	}
	var b []byte
	var err error
	if strings.HasSuffix(strings.ToLower(meta), ";base64") {
		b, err = base64.StdEncoding.DecodeString(strings.TrimSpace(data))
	} else {
		var s string
		s, err = url.PathUnescape(data)
		b = []byte(s)
	}
	if err != nil {
		return nil, err
	}
	if len(b) > logoMaxBytes {
		return nil, errLogoTooLarge
	}
	return b, nil
}

// embeddable checks raw is a PNG or JPEG by its bytes and returns what the
// PDF embeds: a JPEG as it is, a PNG re-encoded as 8-bit non-interlaced (the
// PDF writer refuses interlaced and 16-bit PNGs).
func embeddable(raw []byte) ([]byte, error) {
	switch http.DetectContentType(raw) {
	case "image/jpeg":
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		if err := checkPixels(cfg); err != nil {
			return nil, err
		}
		return raw, nil
	case "image/png":
		cfg, err := png.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		if err := checkPixels(cfg); err != nil {
			return nil, err
		}
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		out := image.NewNRGBA(img.Bounds())
		draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
		var b bytes.Buffer
		if err := png.Encode(&b, out); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	default:
		return nil, errLogoType
	}
}

// checkPixels refuses dimensions over 4096 on a side or 16 MP in all, before
// anything is decoded.
func checkPixels(cfg image.Config) error {
	if cfg.Width > logoMaxSide || cfg.Height > logoMaxSide || cfg.Width*cfg.Height > logoMaxPixels {
		return fmt.Errorf("%w: %d x %d", errLogoPixels, cfg.Width, cfg.Height)
	}
	return nil
}

// logoIsJPEG tells an embeddable logo's Maroto type: JPEG, else PNG.
func logoIsJPEG(logo []byte) bool { return http.DetectContentType(logo) == "image/jpeg" }

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// redactLogoURL keeps a log line short: a data URI is logged by its header.
func redactLogoURL(u string) string {
	if hasPrefixFold(u, "data:") {
		meta, _, _ := strings.Cut(u, ",")
		return meta + ",…"
	}
	return u
}
