package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/luc1ferxx/neural-canvas/backend/config"
	"github.com/luc1ferxx/neural-canvas/backend/logging"
	"github.com/luc1ferxx/neural-canvas/backend/model"
	"github.com/luc1ferxx/neural-canvas/backend/service"

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
	// tokenTTL is how long an issued token stays valid.
	tokenTTL = 24 * time.Hour
)

// signinResponse carries the token as JSON.
//
// This used to be a bare text/plain body. A JSON object costs nothing now and
// means a field can be added later -- an expiry, a refresh token -- without
// every existing client having to be changed on the same day.
type signinResponse struct {
	Token string `json:"token"`
}

func signinHandler(w http.ResponseWriter, r *http.Request) {
	log := logging.FromContext(r.Context())

	user, ok := decodeCredentials(w, r)
	if !ok {
		return
	}

	// The throttle is backed by Elasticsearch, so the limit is shared across
	// instances. If that lookup fails, Elasticsearch is unreachable and the
	// credential check below would fail anyway -- so this logs and continues
	// rather than locking everyone out on a transient error.
	allowed, err := service.LoginAllowed(r.Context(), user.Username)
	if err != nil {
		log.Warn("could not read login throttle",
			slog.String("username", user.Username), slog.String("cause", err.Error()))
	} else if !allowed {
		log.Warn("throttled sign-in", slog.String("username", user.Username))
		writeError(w, r, http.StatusTooManyRequests, codeRateLimited,
			"Too many failed sign-in attempts, try again later")
		return
	}

	success, err := service.CheckUser(r.Context(), user.Username, user.Password)
	if err != nil {
		log.Error("could not verify credentials", slog.String("cause", err.Error()))
		writeError(w, r, http.StatusInternalServerError, codeInternal,
			"Failed to verify credentials")
		return
	}

	if !success {
		if err := service.RecordLoginFailure(r.Context(), user.Username); err != nil {
			log.Warn("could not record login failure",
				slog.String("username", user.Username), slog.String("cause", err.Error()))
		}
		writeError(w, r, http.StatusUnauthorized, codeUnauthorized,
			"Invalid username or password")
		return
	}

	if err := service.ClearLoginFailures(r.Context(), user.Username); err != nil {
		log.Warn("could not clear login failures",
			slog.String("username", user.Username), slog.String("cause", err.Error()))
	}

	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": user.Username,
		"iat":      now.Unix(),
		"exp":      now.Add(tokenTTL).Unix(),
	})

	// Signed with the secret from the environment. This used to be the literal
	// []byte("secret"), which let anyone mint a token for any username.
	tokenString, err := token.SignedString(config.C.JWTSecret)
	if err != nil {
		log.Error("could not sign token", slog.String("cause", err.Error()))
		writeError(w, r, http.StatusInternalServerError, codeInternal,
			"Failed to generate token")
		return
	}

	log.Info("signed in", slog.String("username", user.Username))
	writeJSON(w, http.StatusOK, signinResponse{Token: tokenString})
}

func signupHandler(w http.ResponseWriter, r *http.Request) {
	log := logging.FromContext(r.Context())

	user, ok := decodeCredentials(w, r)
	if !ok {
		return
	}

	if !usernamePattern.MatchString(user.Username) {
		writeError(w, r, http.StatusBadRequest, codeInvalidRequest,
			"Username must be 3-32 characters of lowercase letters, digits, underscore or dash")
		return
	}
	if len(user.Password) < minPasswordLen || len(user.Password) > maxPasswordLen {
		writeError(w, r, http.StatusBadRequest, codeInvalidRequest,
			fmt.Sprintf("Password must be between %d and %d characters", minPasswordLen, maxPasswordLen))
		return
	}

	if err := service.AddUser(r.Context(), user); err != nil {
		if errors.Is(err, service.ErrUserExists) {
			// A distinct code, because "username taken" and "password too short"
			// are both 400 and a client needs to tell them apart to know which
			// field to highlight.
			writeError(w, r, http.StatusConflict, codeUserExists, "User already exists")
			return
		}
		log.Error("could not save user", slog.String("cause", err.Error()))
		writeError(w, r, http.StatusInternalServerError, codeInternal, "Failed to save user")
		return
	}

	log.Info("user registered", slog.String("username", user.Username))
	writeJSON(w, http.StatusOK, map[string]string{"username": user.Username})
}

// decodeCredentials reads a size-limited JSON body into a User. It writes the
// error response itself and reports whether decoding succeeded.
func decodeCredentials(w http.ResponseWriter, r *http.Request) (*model.User, bool) {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxAuthBodyBytes))
	var user model.User
	if err := decoder.Decode(&user); err != nil {
		// Logged at debug: a malformed body is the client's problem, and at info
		// it would let anyone fill the log by posting garbage in a loop.
		logging.FromContext(r.Context()).Debug("could not decode credentials",
			slog.String("cause", err.Error()))
		writeError(w, r, http.StatusBadRequest, codeInvalidRequest,
			"Cannot decode user data from client")
		return nil, false
	}
	if user.Username == "" || user.Password == "" {
		writeError(w, r, http.StatusBadRequest, codeInvalidRequest,
			"Username and password are required")
		return nil, false
	}
	return &user, true
}
