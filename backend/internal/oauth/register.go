package oauth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/oauth/oauthdb"
)

// maxRegisterBody bounds a registration body; real ones are a few hundred bytes.
const maxRegisterBody = 64 << 10

// registerRequest is RFC 7591's client metadata as far as the server reads
// it. Other members (grant_types, scope, ...) are ignored, as in Python. A
// pointer tells a missing redirect_uris from an empty one.
type registerRequest struct {
	RedirectURIs *[]string `json:"redirect_uris"`
	ClientName   *string   `json:"client_name"`
}

// registerResponse lists its members in Python's order.
type registerResponse struct {
	ClientID                string   `json:"client_id"`
	ClientName              *string  `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

// register is POST /oauth/register (RFC 7591), open to anyone: MCP clients
// register themselves before any user signs in.
func (h handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRegisterBody))
	if err := dec.Decode(&req); err != nil || req.RedirectURIs == nil {
		// Python answered a missing or malformed body with FastAPI's 422
		// (Go change 9): the OAuth shape instead.
		oauthError(w, http.StatusBadRequest, "invalid_request", "request body must be JSON with redirect_uris (a list of strings) and an optional client_name")
		return
	}
	// A control character (NUL above all, which Postgres text refuses) in
	// the self-chosen label is refused here rather than failing the insert.
	if req.ClientName != nil && strings.ContainsFunc(*req.ClientName, unicode.IsControl) {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "client_name must not contain control characters")
		return
	}
	uris := *req.RedirectURIs
	if len(uris) == 0 {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris is required")
		return
	}
	for _, u := range uris {
		if !validRedirectURI(u) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri must be https or loopback http")
			return
		}
	}
	clientID := strings.ReplaceAll(uuid.NewString(), "-", "")
	err := oauthdb.New(h.pool).InsertOAuthClient(r.Context(), oauthdb.InsertOAuthClientParams{
		ClientID: clientID, ClientName: req.ClientName, RedirectUris: strings.Join(uris, " "),
	})
	if err != nil {
		panic(err) // the recoverer logs it, reports it and answers 500
	}
	writeJSON(w, http.StatusCreated, registerResponse{
		ClientID: clientID, ClientName: req.ClientName, RedirectURIs: uris,
		TokenEndpointAuthMethod: "none",
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
	})
}

// validRedirectURI is Go change 5: https, or http to a loopback host (any
// port), with no whitespace or control character and no fragment. Whitespace
// matters because the registered URIs are stored space-joined.
func validRedirectURI(s string) bool {
	if s == "" || strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return false
	}
	if strings.Contains(s, "#") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return true
		}
	}
	return false
}
