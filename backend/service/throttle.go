package service

import (
	"fmt"
	"time"

	"github.com/luc1ferxx/neural-canvas/backend/constants"
	"github.com/luc1ferxx/neural-canvas/backend/store"
)

const (
	// MaxLoginFailures within LoginWindow before an account is locked out.
	MaxLoginFailures = 5
	// LoginWindow is how long the failure count is remembered.
	LoginWindow = 15 * time.Minute
)

// loginAttempt is the stored counter for one username.
type loginAttempt struct {
	Failures     int   `json:"failures"`
	FirstAttempt int64 `json:"firstAttempt"`
}

// incrementScript adds one to the failure count, restarting the count when the
// window has elapsed.
//
// This runs inside Elasticsearch so the increment is atomic. Doing the same in
// Go would be a read, a decision and a write, and two simultaneous attempts
// would each read the same value and one increment would be lost -- which is
// exactly the gap an attacker parallelises.
const incrementScript = `
if (ctx._source.firstAttempt == null ||
    params.now - ctx._source.firstAttempt > params.window) {
  ctx._source.failures = 1;
  ctx._source.firstAttempt = params.now;
} else {
  ctx._source.failures += 1;
}
`

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
func LoginAllowed(username string) (bool, error) {
	var attempt loginAttempt
	found, err := store.ESBackend.GetDocument(
		constants.LOGIN_ATTEMPT_INDEX, username, &attempt)
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil
	}

	// Expired window: the next recorded failure restarts the count.
	if time.Now().Unix()-attempt.FirstAttempt > int64(LoginWindow.Seconds()) {
		return true, nil
	}

	return attempt.Failures < MaxLoginFailures, nil
}

// RecordLoginFailure counts one failed sign-in.
func RecordLoginFailure(username string) error {
	now := time.Now().Unix()

	return store.ESBackend.UpdateWithScript(
		constants.LOGIN_ATTEMPT_INDEX,
		username,
		incrementScript,
		map[string]interface{}{
			"now":    now,
			"window": int64(LoginWindow.Seconds()),
		},
		// Used when no counter exists yet; the script does not run in that case.
		map[string]interface{}{
			"failures":     1,
			"firstAttempt": now,
		},
	)
}

// ClearLoginFailures resets the counter after a successful sign-in.
func ClearLoginFailures(username string) error {
	if err := store.ESBackend.DeleteDocument(
		constants.LOGIN_ATTEMPT_INDEX, username); err != nil {
		return fmt.Errorf("clear login failures for %q: %w", username, err)
	}
	return nil
}
