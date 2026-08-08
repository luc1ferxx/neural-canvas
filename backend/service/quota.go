package service

import (
	"context"
	"errors"
	"time"

	"github.com/luc1ferxx/neural-canvas/backend/constants"
)

const (
	// MaxGenerationsPerDay is the per-user ceiling on image generation.
	//
	// Unlike every other endpoint, /generate spends real money on each call: it
	// invokes DALL-E, which is billed per image. Without a ceiling, one
	// authenticated account can drain the entire OpenAI balance in a loop, and
	// the first sign of it is the bill or a hard quota error that takes the
	// feature down for everyone.
	//
	// 20 is chosen to be generous for a person and ruinous for a script.
	MaxGenerationsPerDay = 20

	// GenerationWindow is the period the count covers. A rolling window from the
	// first generation, not a calendar day: a calendar reset lets someone spend
	// two windows back-to-back across midnight.
	GenerationWindow = 24 * time.Hour
)

// ErrQuotaExceeded reports that the caller has used their generation allowance.
var ErrQuotaExceeded = errors.New("generation quota exceeded")

// GenerationsRemaining reports how many generations username has left in the
// current window.
func GenerationsRemaining(ctx context.Context, username string) (int, error) {
	used, err := countWithin(ctx, constants.GENERATION_QUOTA_INDEX, username, GenerationWindow)
	if err != nil {
		return 0, err
	}

	remaining := MaxGenerationsPerDay - used
	if remaining < 0 {
		return 0, nil
	}
	return remaining, nil
}

// ReserveGeneration records an intended generation and reports ErrQuotaExceeded
// when the allowance is spent.
//
// The count is incremented before the work rather than after it, which is
// deliberate and is the opposite of what the login throttle does. A failure that
// happens after OpenAI has been called has still been billed, so counting only
// successes would let a caller with a prompt that reliably fails downstream spend
// without limit. Charging the attempt errs against the user by at most the number
// of genuine internal failures, which is the cheaper mistake.
//
// It is check-then-increment against a shared counter, so a burst of simultaneous
// requests can overshoot by roughly the concurrency. The increment itself is
// atomic, so the overshoot is bounded and small; closing it entirely would need a
// compare-and-set the counter API does not offer. For a spending cap, "20 or
// occasionally 22" is a materially different thing from "unbounded", which is what
// this replaces.
func ReserveGeneration(ctx context.Context, username string) error {
	used, err := countWithin(ctx, constants.GENERATION_QUOTA_INDEX, username, GenerationWindow)
	if err != nil {
		return err
	}
	if used >= MaxGenerationsPerDay {
		return ErrQuotaExceeded
	}

	return recordEvent(ctx, constants.GENERATION_QUOTA_INDEX, username, GenerationWindow)
}
