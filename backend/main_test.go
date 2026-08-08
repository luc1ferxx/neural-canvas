package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// listenLocal binds an ephemeral port so the tests do not collide with anything
// running on the real one.
func listenLocal(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

// TestServeDrainsInFlightRequest is the point of the whole exercise: a request
// already being handled when the signal arrives must still get its response.
//
// Before this, the server was started with http.ListenAndServe and never shut
// down, so retiring an instance killed a 32 MiB upload mid-transfer and the
// client saw a connection reset with no status code to interpret.
func TestServeDrainsInFlightRequest(t *testing.T) {
	ln := listenLocal(t)
	addr := ln.Addr().String()

	handlerStarted := make(chan struct{})
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(handlerStarted)
			// Stand in for a slow upload that is underway when the signal lands.
			time.Sleep(200 * time.Millisecond)
			_, _ = fmt.Fprint(w, "finished")
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	served := make(chan error, 1)
	go func() { served <- serve(ctx, srv, ln, 5*time.Second, nil) }()

	type result struct {
		body string
		err  error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/")
		if err != nil {
			got <- result{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		got <- result{body: string(b), err: err}
	}()

	// Cancel only once the handler is definitely mid-flight; cancelling earlier
	// would prove nothing, because there would be nothing to drain.
	select {
	case <-handlerStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("the handler never started")
	}
	cancel()

	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("the in-flight request was cut off instead of drained: %v", r.err)
		}
		if r.body != "finished" {
			t.Errorf("body = %q, want %q: the response was truncated", r.body, "finished")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the in-flight request never completed")
	}

	select {
	case err := <-served:
		if err != nil {
			t.Errorf("serve() = %v, want nil: a clean shutdown is not an error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve() did not return after the drain finished")
	}
}

// TestServeRefusesNewRequestsAfterSignal checks the other half of a drain: the
// instance stops taking work it cannot be sure it can finish.
func TestServeRefusesNewRequestsAfterSignal(t *testing.T) {
	ln := listenLocal(t)
	addr := ln.Addr().String()

	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, srv, ln, 5*time.Second, nil) }()

	// Confirm it is actually serving first, otherwise the assertion below would
	// pass just as well against a server that never started.
	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("the server was not serving before shutdown: %v", err)
	}
	_ = resp.Body.Close()

	cancel()
	if err := <-served; err != nil {
		t.Fatalf("serve() = %v, want nil", err)
	}

	// The listener is closed, so a new connection is refused rather than accepted
	// and abandoned.
	client := &http.Client{Timeout: 2 * time.Second}
	if resp, err := client.Get("http://" + addr + "/"); err == nil {
		_ = resp.Body.Close()
		t.Error("a new request succeeded after shutdown; the listener is still open")
	}
}

// TestServeReportsAnUndrainableRequest covers the failure path. A handler that
// outlasts the grace period has to be reported, not silently abandoned: that is
// the signal that shutdownTimeout is set wrong, or that a handler can block
// longer than a deploy will wait.
func TestServeReportsAnUndrainableRequest(t *testing.T) {
	ln := listenLocal(t)
	addr := ln.Addr().String()

	release := make(chan struct{})
	handlerStarted := make(chan struct{})
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(handlerStarted)
			<-release
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, srv, ln, 100*time.Millisecond, nil) }()

	go func() {
		resp, err := http.Get("http://" + addr + "/")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-handlerStarted:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("the handler never started")
	}
	cancel()

	select {
	case err := <-served:
		if err == nil {
			t.Error("serve() = nil, want an error: a handler outlasted the grace period")
		}
	case <-time.After(5 * time.Second):
		t.Error("serve() did not return after the grace period elapsed")
	}

	// Let the handler go so the test does not leak the goroutine.
	close(release)
}

// TestServeTreatsAnExternalCloseAsClean exercises the ErrServerClosed guard.
//
// The drain tests do not reach it: there, Shutdown is what stops the server, and
// serve has already returned through the ctx.Done() branch by the time Serve
// reports ErrServerClosed, so the value is never read. This closes the server
// without cancelling the context, which forces the guard to be the thing that
// decides whether a deliberate close looks like a crash.
func TestServeTreatsAnExternalCloseAsClean(t *testing.T) {
	ln := listenLocal(t)

	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Never cancelled, so the ctx.Done() branch cannot fire.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	served := make(chan error, 1)
	go func() { served <- serve(ctx, srv, ln, 5*time.Second, nil) }()

	// Give Serve a moment to be running before closing it.
	time.Sleep(50 * time.Millisecond)
	_ = srv.Close()

	select {
	case err := <-served:
		if err != nil {
			t.Errorf("serve() = %v, want nil: a deliberate close is not a failure", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve() did not return after the server was closed")
	}
}

// TestServeReportsAListenerFailure checks that a real serving failure surfaces
// instead of looking like a clean exit.
func TestServeReportsAListenerFailure(t *testing.T) {
	ln := listenLocal(t)
	// Closing it under the server makes Serve fail immediately.
	_ = ln.Close()

	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := serve(ctx, srv, ln, time.Second, nil)
	if err == nil {
		t.Error("serve() = nil, want an error: serving on a closed listener is a failure")
	}
}

// TestServeCallsOnSignalBeforeDraining pins the ordering that a goroutine could
// not guarantee.
//
// This was a goroutine waiting on ctx.Done() alongside serve. Both woke on the
// same cancellation, so when Shutdown completed quickly -- which it does whenever
// nothing is in flight -- main returned and the process exited before the
// goroutine ran. An end-to-end run against a real Elasticsearch found it: the
// "signal received" line was missing from the log precisely when shutdown went
// well, which is the worst case for noticing.
func TestServeCallsOnSignalBeforeDraining(t *testing.T) {
	ln := listenLocal(t)

	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	var mu sync.Mutex
	calls := 0
	onSignal := func() {
		mu.Lock()
		defer mu.Unlock()
		calls++
	}

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, srv, ln, 5*time.Second, onSignal) }()

	// Let Serve get going, then signal.
	time.Sleep(50 * time.Millisecond)
	cancel()

	if err := <-served; err != nil {
		t.Fatalf("serve() = %v, want nil", err)
	}

	// By the time serve has returned, onSignal must already have run -- not "may
	// eventually run on another goroutine".
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("onSignal called %d times, want exactly 1", calls)
	}
}

// TestServeToleratesNoOnSignal keeps the hook optional.
func TestServeToleratesNoOnSignal(t *testing.T) {
	ln := listenLocal(t)
	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, srv, ln, 5*time.Second, nil) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	if err := <-served; err != nil {
		t.Errorf("serve() with a nil hook = %v, want nil", err)
	}
}

// TestServeDoesNotCallOnSignalOnAListenerFailure checks the hook is tied to
// cancellation, not to any exit: a crash is not a signal.
func TestServeDoesNotCallOnSignalOnAListenerFailure(t *testing.T) {
	ln := listenLocal(t)
	_ = ln.Close()

	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	called := false
	if err := serve(ctx, srv, ln, time.Second, func() { called = true }); err == nil {
		t.Error("serve() = nil, want an error")
	}
	if called {
		t.Error("onSignal ran on a listener failure; it must only run on cancellation")
	}
}
