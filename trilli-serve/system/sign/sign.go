// Package sign is Trilli Sign — DocuSign-style e-signature envelopes, native
// to Trilli (encrypted storage, portals, audit log, mailer).
//
// PRIVATE BETA: while Trilli Sign is being built, every endpoint in this
// package is hard-gated server-side to the beta allowlist. Non-allowlisted
// users get 404 (not 403) so the surface is invisible, matching the
// "coming soon" presentation the rest of the product shows them. Launch =
// flip betaGA to true; the allowlist then no longer matters. The frontend
// mirrors this gate in lib/productivity/access.ts (canUseSign).
package sign

import (
	"net/http"
	"strings"

	"trilli/system/auth"
)

// betaGA releases Trilli Sign to everyone when true.
const betaGA = true

// betaAllowlist holds the accounts that can see/use Trilli Sign during the
// private beta.
var betaAllowlist = map[string]bool{
	"mike@prices.com": true,
}

// Allowed reports whether this identity may see and use Trilli Sign.
func Allowed(id *auth.Identity) bool {
	if betaGA {
		return true
	}
	if id == nil || id.User == nil {
		return false
	}
	return betaAllowlist[strings.ToLower(strings.TrimSpace(id.User.Email))]
}

// RequireBeta wraps a Trilli Sign handler with the beta gate. Runs INSIDE
// RequireAuth, so the identity is already on the context.
func RequireBeta(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.IdentityFromContext(r.Context())
		if !ok || !Allowed(id) {
			http.NotFound(w, r) // invisible, like the feature itself
			return
		}
		next(w, r)
	})
}

type muxLike interface {
	Handle(pattern string, handler http.Handler)
}
