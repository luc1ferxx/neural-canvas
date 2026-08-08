package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/luc1ferxx/neural-canvas/backend/logging"
	"github.com/luc1ferxx/neural-canvas/backend/service"

	jwt "github.com/form3tech-oss/jwt-go"
)

// issuedAtFromClaims reads the "iat" claim.
//
// JSON numbers decode into float64 in a jwt.MapClaims, so the value has to be
// converted rather than asserted straight to int64.
func issuedAtFromClaims(claims jwt.MapClaims) (int64, bool) {
	raw, ok := claims["iat"]
	if !ok {
		return 0, false
	}
	switch v := raw.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

// requireLiveSession rejects a token that is validly signed but has since been
// revoked by a sign-out.
//
// A JWT is self-contained, so signing out cannot invalidate one on its own. The
// user document carries a tokensValidAfter timestamp and any token issued before
// it is refused. The lookup is a get by id, which is realtime in Elasticsearch,
// so a sign-out takes effect on the very next request -- including on other
// instances, since the state lives in Elasticsearch rather than in memory.
//
// The cost is one Elasticsearch get per authenticated request. At this scale that
// is a sub-millisecond lookup; a short-lived cache would trade immediacy for
// throughput if that ever stops being true.
func requireLiveSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := r.Context().Value("user").(*jwt.Token)
		if !ok || token == nil {
			writeError(w, r, http.StatusUnauthorized, codeUnauthorized, "Unauthorized")
			return
		}
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			writeError(w, r, http.StatusUnauthorized, codeUnauthorized, "Unauthorized")
			return
		}
		username, ok := claims["username"].(string)
		if !ok || username == "" {
			writeError(w, r, http.StatusUnauthorized, codeUnauthorized, "Unauthorized")
			return
		}

		issuedAt, ok := issuedAtFromClaims(claims)
		if !ok {
			// Tokens minted before the iat claim existed cannot be checked
			// against a revocation cutoff, so they are not trusted.
			writeError(w, r, http.StatusUnauthorized, codeSessionRevoked,
				"Session is no longer valid, please sign in again")
			return
		}

		validAfter, err := service.TokensValidAfter(r.Context(), username)
		if err != nil {
			logging.FromContext(r.Context()).Error("could not verify session",
				slog.String("username", username), slog.String("cause", err.Error()))
			writeError(w, r, http.StatusInternalServerError, codeInternal,
				"Failed to verify session")
			return
		}

		if issuedAt < validAfter {
			writeError(w, r, http.StatusUnauthorized, codeSessionRevoked,
				"Session is no longer valid, please sign in again")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// signoutHandler revokes every token issued to the caller so far.
func signoutHandler(w http.ResponseWriter, r *http.Request) {
	log := logging.FromContext(r.Context())

	username, ok := usernameFromContext(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, codeUnauthorized, "Unauthorized")
		return
	}

	if err := service.RevokeTokens(r.Context(), username); err != nil {
		log.Error("could not revoke tokens",
			slog.String("username", username), slog.String("cause", err.Error()))
		writeError(w, r, http.StatusInternalServerError, codeInternal, "Failed to sign out")
		return
	}

	log.Info("signed out", slog.String("username", username))
	writeJSON(w, http.StatusOK, map[string]string{"username": username})
}
