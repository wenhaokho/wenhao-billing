package platform

import (
	"context"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// RelaxCommon is embedded in a request body, as a feature shares fields
// between its create and update bodies.
type RelaxCommon struct {
	Note Optional[string] `json:"note"`
}

type relaxEntry struct {
	Memo Optional[string] `json:"memo"`
	Size int              `json:"size"`
}

type relaxBody struct {
	RelaxCommon
	Name  string                `json:"name"`
	ByKey map[string]relaxEntry `json:"by_key,omitempty"`
}

type relaxIn struct {
	Body relaxBody
}

func TestRelaxReachesEmbeddedAndMapFields(t *testing.T) {
	t.Parallel()
	h, api := NewServer(Config{Environment: "test"}, Deps{})
	huma.Register(api, huma.Operation{OperationID: "relax", Method: http.MethodPatch, Path: "/relax", Tags: []string{PublicTag}},
		func(_ context.Context, _ *relaxIn) (*struct{}, error) { return nil, nil })
	for _, body := range []string{`{"name":"a"}`, `{"name":"a","by_key":{"k":{"size":1}}}`} {
		if rec := do(t, h, http.MethodPatch, "/relax", body); rec.Code != 204 {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	// Plain fields stay required.
	for _, body := range []string{`{}`, `{"name":"a","by_key":{"k":{}}}`} {
		if rec := do(t, h, http.MethodPatch, "/relax", body); rec.Code != 422 {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
}

type relaxArrayIn struct {
	Body []relaxEntry
}

// A bare JSON array body reaches its items' fields too.
func TestRelaxReachesArrayBodyItems(t *testing.T) {
	t.Parallel()
	h, api := NewServer(Config{Environment: "test"}, Deps{})
	huma.Register(api, huma.Operation{OperationID: "relax-array", Method: http.MethodPut, Path: "/relax-array", Tags: []string{PublicTag}},
		func(_ context.Context, _ *relaxArrayIn) (*struct{}, error) { return nil, nil })
	if rec := do(t, h, http.MethodPut, "/relax-array", `[{"size":1}]`); rec.Code != 204 {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, http.MethodPut, "/relax-array", `[{"memo":"m"}]`); rec.Code != 422 {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

// RelaxShared is embedded by pointer.
type RelaxShared struct {
	Memo Optional[string] `json:"memo"`
}

type relaxPtrIn struct {
	Body struct {
		*RelaxShared
		Name string `json:"name"`
	}
}

func TestRelaxReachesPointerEmbeddedFields(t *testing.T) {
	t.Parallel()
	h, api := NewServer(Config{Environment: "test"}, Deps{})
	huma.Register(api, huma.Operation{OperationID: "relax-ptr", Method: http.MethodPatch, Path: "/relax-ptr", Tags: []string{PublicTag}},
		func(_ context.Context, _ *relaxPtrIn) (*struct{}, error) { return nil, nil })
	if rec := do(t, h, http.MethodPatch, "/relax-ptr", `{"name":"a"}`); rec.Code != 204 {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}
