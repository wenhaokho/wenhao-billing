package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// The write tools take their arguments as loose JSON and need the typed
// request bodies of the HTTP routes (bills.BillCreate, invoices.InvoiceUpdate
// ...) so they run the same services. The bodies are not plain structs:
// their fields are platform.Optional, NotNull, Dec4 and Date values whose
// refusals (a decimal with five places, a malformed date, an enum, a length)
// are reported by Huma's own validation and resolvers, and a refused decimal
// that nothing resolved panics on use. So the arguments are decoded the way a
// request is: through a private Huma API built by platform.NewServer, whose
// one operation per body type only hands the decoded body back. Every
// validation the routes apply applies here, and a refusal is the sentence
// Huma's 422 carries.

// decoder is that private API. It is built once, before the first call, and
// its operations are registered then (the router is not safe to change while
// it serves).
type decoder struct {
	handler http.Handler
	api     huma.API
}

func newDecoder(cfg platform.Config) *decoder {
	h, api := platform.NewServer(cfg, platform.Deps{})
	return &decoder{handler: h, api: api}
}

// slotKey carries the pointer the operation's handler stores the body in.
type slotKey struct{}

type bodyIn[T any] struct{ Body T }

// decodeInto registers the operation that decodes T under name and returns
// the function that does it: the arguments are marshalled as the request
// body, and the result is the typed body or a refusal carrying the 422's
// sentence ("field: message", several joined by "; ").
func decodeInto[T any](d *decoder, name string) func(ctx context.Context, args map[string]any) (T, error) {
	path := "/decode/" + name
	huma.Register(d.api, huma.Operation{
		OperationID: "mcp-decode-" + name, Method: http.MethodPost, Path: path,
		Tags: []string{platform.PublicTag},
	}, func(ctx context.Context, in *bodyIn[T]) (*struct{}, error) {
		slot, _ := ctx.Value(slotKey{}).(*T)
		if slot == nil {
			return nil, fmt.Errorf("mcp decode %s: no slot in the request context", name)
		}
		*slot = in.Body
		return nil, nil
	})
	return func(ctx context.Context, args map[string]any) (T, error) {
		var out, zero T
		raw, err := json.Marshal(args)
		if err != nil {
			return zero, Refuse("the arguments are not JSON: %v", err)
		}
		req, err := http.NewRequestWithContext(context.WithValue(ctx, slotKey{}, &out), http.MethodPost, path, bytes.NewReader(raw))
		if err != nil {
			return zero, fmt.Errorf("mcp decode %s: %w", name, err)
		}
		req.Header.Set("Content-Type", "application/json")
		w := &memWriter{header: http.Header{}, status: http.StatusOK}
		d.handler.ServeHTTP(w, req)
		switch {
		case w.status >= 200 && w.status < 300:
			return out, nil
		case w.status >= 400 && w.status < 500:
			return zero, Refuse("%s", problemSentence(w.body.Bytes()))
		default:
			return zero, fmt.Errorf("mcp decode %s: status %d: %s", name, w.status, w.body.String())
		}
	}
}

// problemSentence is the sentence of a problem+json error body: each field
// error as "field: message", joined by "; ", or the detail when there are
// none. A field is named as the 422 names it, without the "body." of a path.
func problemSentence(body []byte) string {
	var p struct {
		Detail string `json:"detail"`
		Errors []struct {
			Location string `json:"location"`
			Message  string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return "the arguments were refused"
	}
	parts := make([]string, 0, len(p.Errors))
	for _, e := range p.Errors {
		loc := strings.TrimPrefix(strings.TrimPrefix(e.Location, "body."), "body")
		if loc == "" {
			parts = append(parts, e.Message)
			continue
		}
		parts = append(parts, loc+": "+e.Message)
	}
	if len(parts) == 0 {
		if p.Detail == "" {
			return "the arguments were refused"
		}
		return p.Detail
	}
	return strings.Join(parts, "; ")
}

// memWriter is a response writer that keeps the response in memory (the
// decoder's requests never leave the process).
type memWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *memWriter) Header() http.Header         { return w.header }
func (w *memWriter) WriteHeader(status int)      { w.status = status }
func (w *memWriter) Write(b []byte) (int, error) { return w.body.Write(b) }
