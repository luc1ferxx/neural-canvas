package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"socialai/config"
	"socialai/model"
	"socialai/service"

	"github.com/form3tech-oss/jwt-go"
)

// usernamePattern requires 3-32 characters of lowercase alphanumerics,
// underscore or dash.
//
// The previous pattern was `^[a-z0-9]$`, which matches exactly one character.
// Anchored that way it rejected only single-character usernames and let
// everything else through, including whitespace and control characters.
var usernamePattern = regexp.MustCompile(`^[a-z0-9_-]{3,32}$`)

const (
	minPasswordLen = 8
	maxPasswordLen = 72 // bcrypt silently truncates beyond 72 bytes
	// maxAuthBodyBytes caps the credential payload; nothing legitimate is large.
	maxAuthBodyBytes = 4 << 10
)

var throttle = newLoginThrottle()

func signinHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Received one signin request")

	user, ok := decodeCredentials(w, r)
	if !ok {
		return
	}

	if !throttle.allow(user.Username) {
		http.Error(w, "Too many failed sign-in attempts, try again later", http.StatusTooManyRequests)
		fmt.Printf("Throttled sign-in for %q\n", user.Username)
		return
	}

	success, err := service.CheckUser(user.Username, user.Password)
	if err != nil {
		http.Error(w, "Failed to verify credentials", http.StatusInternalServerError)
		fmt.Printf("Failed to verify credentials %v\n", err)
		return
	}

	if !success {
		throttle.recordFailure(user.Username)
		http.Error(w, "Invalid username or password", http.StatusUnauthorized)
		return
	}

	throttle.recordSuccess(user.Username)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": user.Username,
		"iat":      time.Now().Unix(),
		"exp":      time.Now().Add(time.Hour * 24).Unix(),
	})

	// Signed with the secret from the environment. This used to be the literal
	// []byte("secret"), which let anyone mint a token for any username.
	tokenString, err := token.SignedString(config.C.JWTSecret)
	if err != nil {
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		fmt.Printf("Failed to generate token %v\n", err)
		return
	}

	// Plain text, because the frontend stores the response body verbatim.
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(tokenString))
}

func signupHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Received one signup request")

	user, ok := decodeCredentials(w, r)
	if !ok {
		return
	}

	if !usernamePattern.MatchString(user.Username) {
		http.Error(w,
			"Username must be 3-32 characters of lowercase letters, digits, underscore or dash",
			http.StatusBadRequest)
		return
	}
	if len(user.Password) < minPasswordLen || len(user.Password) > maxPasswordLen {
		http.Error(w,
			fmt.Sprintf("Password must be between %d and %d characters", minPasswordLen, maxPasswordLen),
			http.StatusBadRequest)
		return
	}

	if err := service.AddUser(user); err != nil {
		if errors.Is(err, service.ErrUserExists) {
			http.Error(w, "User already exists", http.StatusBadRequest)
			return
		}
		http.Error(w, "Failed to save user", http.StatusInternalServerError)
		fmt.Printf("Failed to save user %v\n", err)
		return
	}

	fmt.Printf("User added successfully: %s.\n", user.Username)
	writeJSON(w, http.StatusOK, map[string]string{"username": user.Username})
}

// decodeCredentials reads a size-limited JSON body into a User. It writes the
// error response itself and reports whether decoding succeeded.
func decodeCredentials(w http.ResponseWriter, r *http.Request) (*model.User, bool) {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxAuthBodyBytes))
	var user model.User
	if err := decoder.Decode(&user); err != nil {
		http.Error(w, "Cannot decode user data from client", http.StatusBadRequest)
		fmt.Printf("Cannot decode user data from client %v\n", err)
		return nil, false
	}
	if user.Username == "" || user.Password == "" {
		http.Error(w, "Username and password are required", http.StatusBadRequest)
		return nil, false
	}
	return &user, true
}
