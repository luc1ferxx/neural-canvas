package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/luc1ferxx/neural-canvas/backend/config"
	"github.com/luc1ferxx/neural-canvas/backend/handler"
	"github.com/luc1ferxx/neural-canvas/backend/logging"
	"github.com/luc1ferxx/neural-canvas/backend/store"
)

const (
	// startupTimeout bounds connecting to Elasticsearch and creating the indexes.
	// Without it a cluster that accepts the TCP connection but never answers leaves
	// the process hanging at boot with no output, which reads as a crash.
	startupTimeout = 60 * time.Second

	// shutdownTimeout is how long in-flight requests get to finish after a
	// termination signal. App Engine and Kubernetes both send SIGTERM and then
	// SIGKILL after a grace period, so this has to stay comfortably under theirs
	// or it accomplishes nothing.
	shutdownTimeout = 25 * time.Second
)

func main() {
	// Cancelled on SIGTERM (what App Engine and Kubernetes send to retire an
	// instance) or SIGINT (Ctrl-C locally). Everything downstream derives from
	// this, so a signal during a slow startup aborts it rather than being ignored
	// until the server is already listening.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Load and validate configuration before anything else, so a missing secret
	// is one clear message at startup rather than a panic on the first request.
	// log.Fatalf rather than slog here, deliberately: the log level is itself a
	// config value, so structured logging cannot be configured until this returns.
	if err := config.Load(); err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	logging.Init(config.C.LogLevel)
	slog.Info("starting", slog.String("log_level", config.C.LogLevel.String()))

	startupCtx, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	defer cancelStartup()

	if err := store.InitElasticsearchBackend(startupCtx); err != nil {
		slog.Error("elasticsearch unavailable", slog.String("cause", err.Error()))
		os.Exit(1)
	}

	// Deliberately the signal context and not startupCtx: the GCS client resolves
	// credentials during construction, and google-cloud-go has historically used
	// the context passed here beyond that point. A deadline that expired 60s into
	// the process lifetime would then break uploads long after a successful
	// startup. Cancellation on shutdown is still wanted; an expiry is not.
	if err := store.InitGCSBackend(ctx); err != nil {
		slog.Error("google cloud storage unavailable", slog.String("cause", err.Error()))
		os.Exit(1)
	}

	// App Engine supplies PORT; default 8080 for local runs.
	addr := ":" + config.C.Port

	// Listening before handing off to serve, so "port already in use" is reported
	// here with the address in the message rather than from inside the lifecycle.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("cannot listen", slog.String("addr", addr), slog.String("cause", err.Error()))
		os.Exit(1)
	}

	// An explicit server rather than http.ListenAndServe, which has no timeouts
	// at all: a client can open a connection, dribble out a request header and
	// hold a goroutine indefinitely.
	//
	// ReadTimeout and WriteTimeout are deliberately left unset. A single read
	// deadline would have to cover a 32 MiB upload, which takes minutes on a slow
	// connection, and a write deadline would have to cover /generate, which waits
	// on DALL-E and then on a GCS upload. Any value tight enough to be useful
	// against a slow client would also cut off legitimate requests. ReadHeaderTimeout
	// is the one that addresses the actual attack, because a request header is
	// small and arrives quickly no matter how slow the link.
	srv := &http.Server{
		Handler:           handler.InitRouter(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Stop trapping the signal as soon as the first one arrives, so an impatient
	// second Ctrl-C kills the process instead of being swallowed while requests
	// drain.
	go func() {
		<-ctx.Done()
		stop()
		slog.Info("signal received, draining",
			slog.String("grace", shutdownTimeout.String()))
	}()

	slog.Info("listening", slog.String("addr", ln.Addr().String()))
	if err := serve(ctx, srv, ln, shutdownTimeout); err != nil {
		slog.Error("serve failed", slog.String("cause", err.Error()))
		os.Exit(1)
	}
	slog.Info("shutdown complete")
}

// serve runs srv on ln until ctx is cancelled, then stops accepting new
// connections and gives in-flight handlers up to grace to finish.
//
// This is separated from main so it can be tested. Draining is the kind of
// behaviour that is easy to write plausibly and get wrong -- forgetting that
// ErrServerClosed is the success path, or calling Shutdown with the context that
// was just cancelled, so the grace period is already expired when it starts --
// and neither mistake shows up in a compile or a manual smoke test.
func serve(ctx context.Context, srv *http.Server, ln net.Listener, grace time.Duration) error {
	serveErr := make(chan error, 1)
	go func() {
		// ErrServerClosed is not a failure; it is what Shutdown causes, by design.
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		// A fresh context, not a derivative of ctx: ctx is already cancelled, and
		// deriving from it would give Shutdown an expired deadline and turn the
		// graceful drain into an immediate close.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("in-flight requests did not finish within %s: %w", grace, err)
		}
		return nil
	}
}
