package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/luc1ferxx/neural-canvas/backend/constants"
	"github.com/luc1ferxx/neural-canvas/backend/model"
	"github.com/luc1ferxx/neural-canvas/backend/store"

	"golang.org/x/crypto/bcrypt"
)

// ErrUserExists reports a taken username.
var ErrUserExists = fmt.Errorf("user already exists")

// dummyHash is a valid bcrypt hash of a value nobody can supply, used purely to
// equalize timing on the user-not-found path.
var dummyHash = []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")

// getUser fetches a user by username.
//
// Users are indexed under their username, so this is a get by id rather than a
// search. Get is realtime in Elasticsearch, which means a freshly registered
// account can log in immediately instead of waiting for the next refresh.
func getUser(ctx context.Context, username string) (*model.User, bool, error) {
	var user model.User
	found, err := store.ESBackend.GetDocument(ctx, constants.USER_INDEX, username, &user)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	return &user, true, nil
}

// CheckUser looks the user up by username and compares the supplied password
// against the stored bcrypt hash.
//
// The password is never part of the query. The original implementation searched
// Elasticsearch for username AND password as a term query, which only works if
// the password is stored in plaintext.
//
// Returns (false, nil) for both "no such user" and "wrong password" so the
// caller cannot distinguish them and leak which usernames exist.
func CheckUser(ctx context.Context, username, password string) (bool, error) {
	user, found, err := getUser(ctx, username)
	if err != nil {
		return false, err
	}
	if !found {
		// Hash a throwaway value so a missing user costs roughly the same time
		// as a wrong password; otherwise response latency reveals which
		// usernames are registered.
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return false, nil
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, nil
		}
		// A malformed hash means this row predates hashing. Those rows are not
		// usable for login by design: see the migration note in README.
		return false, fmt.Errorf("stored credential for %q is not a valid bcrypt hash: %w", username, err)
	}

	return true, nil
}

// AddUser hashes the password and stores the user. Returns ErrUserExists if the
// username is taken.
func AddUser(ctx context.Context, user *model.User) error {
	_, found, err := getUser(ctx, user.Username)
	if err != nil {
		return err
	}
	if found {
		return ErrUserExists
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	// Store a copy so the caller's struct never carries the hash back out, and
	// so the plaintext in user.Password is not what gets serialized.
	toStore := *user
	toStore.Password = string(hashed)

	if err := store.ESBackend.SaveToES(ctx, &toStore, constants.USER_INDEX, toStore.Username); err != nil {
		return err
	}

	// No log line here: signupHandler already records the registration with the
	// request id attached, which this could not have done.
	return nil
}

// TokensValidAfter returns the unix timestamp before which this user's tokens
// are no longer accepted. Zero means nothing has been revoked.
func TokensValidAfter(ctx context.Context, username string) (int64, error) {
	user, found, err := getUser(ctx, username)
	if err != nil {
		return 0, err
	}
	if !found {
		// The account is gone; treat every token for it as revoked.
		return time.Now().Unix() + 1, nil
	}
	return user.TokensValidAfter, nil
}

// RevokeTokens invalidates every token issued to this user up to now.
//
// This is what makes signing out mean something on the server. Clearing the
// token in the browser alone left it valid for the remainder of its 24 hours, so
// a copy taken from storage kept working.
func RevokeTokens(ctx context.Context, username string) error {
	_, found, err := getUser(ctx, username)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	// Tokens carry a whole-second iat. Using now+1 ensures a token minted during
	// this same second is also refused, rather than surviving on a tie.
	cutoff := time.Now().Unix() + 1

	return store.ESBackend.UpdateFields(ctx, constants.USER_INDEX, username,
		map[string]interface{}{"tokensValidAfter": cutoff})
}
