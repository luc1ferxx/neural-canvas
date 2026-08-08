package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"socialai/service"

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
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		username, ok := claims["username"].(string)
		if !ok || username == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		issuedAt, ok := issuedAtFromClaims(claims)
		if !ok {
			// Tokens minted before the iat claim existed cannot be checked
			// against a revocation cutoff, so they are not trusted.
			http.Error(w, "Session is no longer valid, please sign in again",
				http.StatusUnauthorized)
			return
		}

		validAfter, err := service.TokensValidAfter(username)
		if err != nil {
			http.Error(w, "Failed to verify session", http.StatusInternalServerError)
			fmt.Printf("Failed to verify session for %q: %v\n", username, err)
			return
		}

		if issuedAt < validAfter {
			http.Error(w, "Session is no longer valid, please sign in again",
				http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// signoutHandler revokes every token issued to the caller so far.
func signoutHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Received one signout request")

	username, ok := usernameFromContext(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := service.RevokeTokens(username); err != nil {
		http.Error(w, "Failed to sign out", http.StatusInternalServerError)
		fmt.Printf("Failed to revoke tokens for %q: %v\n", username, err)
		return
	}

	fmt.Printf("Signed out %s\n", username)
	writeJSON(w, http.StatusOK, map[string]string{"username": username})
}
