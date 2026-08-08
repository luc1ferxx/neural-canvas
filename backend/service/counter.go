package service

import (
	"context"
	"fmt"
	"time"

	"github.com/luc1ferxx/neural-canvas/backend/store"
)

// A windowed counter in Elasticsearch, shared by the login throttle and the
// generation quota.
//
// Both need the same thing: count events per key, forget them after a window,
// and have the limit apply across instances rather than per instance. The
// original login throttle held its counters in process memory, which meant App
// Engine multiplied the effective limit by the instance count -- five attempts
// became five per instance, and an attacker only had to spread requests around.

// incrementScript adds one to a counter, restarting it when the window has
// elapsed.
//
// This runs inside Elasticsearch so the increment is atomic. Doing the same in Go
// would be a read, a decision and a write, and two simultaneous requests would
// each read the same value and one increment would be lost -- which is exactly the
// gap an attacker parallelises.
const incrementScript = `
if (ctx._source.firstAttempt == null ||
    params.now - ctx._source.firstAttempt > params.window) {
  ctx._source.failures = 1;
  ctx._source.firstAttempt = params.now;
} else {
  ctx._source.failures += 1;
}
`

// counter is the stored document. The field names are historical -- this started
// as login-failure tracking -- and renaming them would need a migration of live
// data for no functional gain.
type counter struct {
	Failures     int   `json:"failures"`
	FirstAttempt int64 `json:"firstAttempt"`
}

// countWithin returns how many events are recorded for key inside the window.
// A key with no document, or one whose window has elapsed, counts as zero.
func countWithin(ctx context.Context, index, key string, window time.Duration) (int, error) {
	var c counter
	found, err := store.ESBackend.GetDocument(ctx, index, key, &c)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}

	if time.Now().Unix()-c.FirstAttempt > int64(window.Seconds()) {
		return 0, nil
	}
	return c.Failures, nil
}

// recordEvent adds one to key's counter, creating it if absent.
func recordEvent(ctx context.Context, index, key string, window time.Duration) error {
	now := time.Now().Unix()

	return store.ESBackend.UpdateWithScript(
		ctx,
		index,
		key,
		incrementScript,
		map[string]interface{}{
			"now":    now,
			"window": int64(window.Seconds()),
		},
		// Used when no counter exists yet; the script does not run in that case.
		map[string]interface{}{
			"failures":     1,
			"firstAttempt": now,
		},
	)
}

// resetCounter removes a counter entirely.
func resetCounter(ctx context.Context, index, key string) error {
	if err := store.ESBackend.DeleteDocument(ctx, index, key); err != nil {
		return fmt.Errorf("reset counter %q in %q: %w", key, index, err)
	}
	return nil
}
