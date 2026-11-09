package oauth

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deps are what the OAuth routes need.
type Deps struct {
	Pool *pgxpool.Pool
	// PublicBaseURL is MCP_PUBLIC_BASE_URL, the origin the discovery
	// documents name.
	PublicBaseURL string
}

type handler struct {
	pool *pgxpool.Pool
	base string
}

// Mount adds the discovery documents and the registration route at the root.
// They are plain chi handlers, not Huma operations: Huma answers a bad body
// in problem+json, and OAuth clients need {"error", "error_description"}.
// The caller mounts them only when MCP_ENABLED.
func Mount(r chi.Router, d Deps) {
	h := handler{pool: d.Pool, base: strings.TrimRight(d.PublicBaseURL, "/")}
	r.Get("/.well-known/oauth-authorization-server", h.authorizationServer)
	r.Get("/.well-known/oauth-protected-resource", h.protectedResource)
	r.Post("/oauth/register", h.register)
}

// writeJSON writes v as a JSON body with status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// oauthError writes an RFC 6749 error body, never problem+json.
func oauthError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}{code, description})
}

// authorizationServer is RFC 8414 metadata; members in Python's order.
func (h handler) authorizationServer(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Issuer                            string   `json:"issuer"`
		AuthorizationEndpoint             string   `json:"authorization_endpoint"`
		TokenEndpoint                     string   `json:"token_endpoint"`
		RegistrationEndpoint              string   `json:"registration_endpoint"`
		RevocationEndpoint                string   `json:"revocation_endpoint"`
		ResponseTypesSupported            []string `json:"response_types_supported"`
		GrantTypesSupported               []string `json:"grant_types_supported"`
		CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
		TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	}{
		Issuer:                            h.base,
		AuthorizationEndpoint:             h.base + "/oauth/authorize",
		TokenEndpoint:                     h.base + "/oauth/token",
		RegistrationEndpoint:              h.base + "/oauth/register",
		RevocationEndpoint:                h.base + "/oauth/revoke",
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none"},
	})
}

// protectedResource is RFC 9728 metadata. resource is the origin, not /mcp
// (kept from Python).
func (h handler) protectedResource(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}{h.base, []string{h.base}})
}
