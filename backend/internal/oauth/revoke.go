package oauth

import (
	"net/http"

	"github.com/wenhaokho/wenhao-billing/backend/internal/oauth/oauthdb"
)

// revoke is POST /oauth/revoke (RFC 7009): always 200 {}. Only refresh
// tokens are revocable (kept from Python); any other token, valid or not, is
// answered the same and changes nothing. token_type_hint and client_id are
// ignored.
func (h handler) revoke(w http.ResponseWriter, r *http.Request) {
	form, ok := readForm(w, r, "token")
	if !ok {
		return
	}
	if _, jti, err := h.tokens.VerifyRefresh(value(form, "token"), h.clock.Now()); err == nil {
		if err := oauthdb.New(h.pool).RevokeRefreshToken(r.Context(), jti); err != nil {
			panic(err)
		}
	}
	writeJSON(w, http.StatusOK, struct{}{})
}
