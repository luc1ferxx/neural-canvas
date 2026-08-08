package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	jwt "github.com/form3tech-oss/jwt-go"
)

// usernameFromContext pulls the authenticated username out of the JWT that the
// middleware stored on the request context.
//
// Every step is a checked type assertion. The previous code chained three
// unchecked assertions, so any request that reached the handler with an
// unexpected context shape panicked -- an unauthenticated way to kill the
// process.
func usernameFromContext(r *http.Request) (string, bool) {
	token, ok := r.Context().Value("user").(*jwt.Token)
	if !ok || token == nil {
		return "", false
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", false
	}
	username, ok := claims["username"].(string)
	if !ok || username == "" {
		return "", false
	}
	return username, true
}

// writeJSON sends v with the given status.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		fmt.Printf("Failed to encode response %v\n", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
