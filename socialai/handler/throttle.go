package handler

import (
    "sync"
    "time"
)

// loginThrottle rate-limits failed sign-in attempts per username.
//
// Keying on the username rather than the client IP is deliberate. Behind App
// Engine the only IP available is either the load balancer's or a
// client-supplied X-Forwarded-For, and the latter is trivially spoofed -- an
// IP-keyed limiter would be bypassed by varying one header. A username key
// cannot be spoofed away: guessing one account's password requires attempts
// against that account.
//
// Scope: this counter lives in process memory. app.yaml allows up to 2
// instances, so the effective limit is maxFailures per instance. Move this to
// Redis or Firestore if the instance count grows.
type loginThrottle struct {
    mu      sync.Mutex
    entries map[string]*throttleEntry
}

type throttleEntry struct {
    failures int
    first    time.Time
}

const (
    // maxFailures within window before the account is temporarily locked.
    maxFailures = 5
    window      = 15 * time.Minute
    // maxTrackedUsers bounds memory: an attacker can otherwise grow the map by
    // trying a new username every request.
    maxTrackedUsers = 10000
)

func newLoginThrottle() *loginThrottle {
    return &loginThrottle{entries: make(map[string]*throttleEntry)}
}

// allow reports whether username may attempt a sign-in right now.
func (t *loginThrottle) allow(username string) bool {
    t.mu.Lock()
    defer t.mu.Unlock()

    e, ok := t.entries[username]
    if !ok {
        return true
    }
    if time.Since(e.first) > window {
        delete(t.entries, username)
        return true
    }
    return e.failures < maxFailures
}

// recordFailure counts a failed attempt.
func (t *loginThrottle) recordFailure(username string) {
    t.mu.Lock()
    defer t.mu.Unlock()

    e, ok := t.entries[username]
    if !ok || time.Since(e.first) > window {
        if len(t.entries) >= maxTrackedUsers {
            t.pruneLocked()
        }
        t.entries[username] = &throttleEntry{failures: 1, first: time.Now()}
        return
    }
    e.failures++
}

// recordSuccess clears the counter so a legitimate login resets the lockout.
func (t *loginThrottle) recordSuccess(username string) {
    t.mu.Lock()
    defer t.mu.Unlock()
    delete(t.entries, username)
}

// pruneLocked drops expired entries. Caller must hold mu.
func (t *loginThrottle) pruneLocked() {
    for k, e := range t.entries {
        if time.Since(e.first) > window {
            delete(t.entries, k)
        }
    }
    // If everything is still live, drop the map wholesale rather than grow
    // without bound. This briefly forgives in-progress attackers, which is
    // preferable to an unbounded allocation.
    if len(t.entries) >= maxTrackedUsers {
        t.entries = make(map[string]*throttleEntry)
    }
}
