package service

import (
	"context"
	"time"

	"github.com/luc1ferxx/neural-canvas/backend/constants"
)

const (
	// MaxLoginFailures within LoginWindow before an account is locked out.
	MaxLoginFailures = 5
	// LoginWindow is how long the failure count is remembered.
	LoginWindow = 15 * time.Minute
)

// loginAttempt is an alias of the shared counter type. Kept as a name because the
// stored document is the same shape and the integration test asserts on it.
type loginAttempt = counter

// LoginAllowed reports whether username may attempt a sign-in.
//
// Counters live in Elasticsearch rather than in process memory, so the limit is
// shared across instances. The previous in-memory version allowed
// MaxLoginFailures per instance, which App Engine multiplies by the instance
// count.
//
// Keying on the username rather than the client IP is deliberate: behind App
// Engine the only available IP is the load balancer's or a client-supplied
// X-Forwarded-For, and the latter is trivially varied to defeat an IP-keyed
// limiter.
func LoginAllowed(ctx context.Context, username string) (bool, error) {
	failures, err := countWithin(ctx, constants.LOGIN_ATTEMPT_INDEX, username, LoginWindow)
	if err != nil {
		return false, err
	}
	return failures < MaxLoginFailures, nil
}

// RecordLoginFailure counts one failed sign-in.
func RecordLoginFailure(ctx context.Context, username string) error {
	return recordEvent(ctx, constants.LOGIN_ATTEMPT_INDEX, username, LoginWindow)
}

// ClearLoginFailures resets the counter after a successful sign-in.
func ClearLoginFailures(ctx context.Context, username string) error {
	return resetCounter(ctx, constants.LOGIN_ATTEMPT_INDEX, username)
}
