// Package logging configures structured logging and carries a request-scoped
// logger on the context.
//
// It replaces 46 fmt.Print calls. Those were unusable in production for a
// specific reason: with concurrent requests interleaving their output, there was
// no way to tell which lines belonged to the same request, so "this user's upload
// failed" could not be reconstructed from the log at all.
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// Init installs a JSON logger as slog's default.
//
// JSON rather than text because this runs on App Engine, where stdout is
// collected by Cloud Logging: a JSON line becomes an entry with queryable
// fields, while a text line becomes one opaque string you can only grep. The
// renamed keys below are the ones Cloud Logging actually looks for -- anything
// else is kept as an ordinary payload field, which is why slog's defaults would
// produce entries whose severity is always "default".
func Init(level slog.Level) {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: cloudLoggingFields,
	})
	slog.SetDefault(slog.New(handler))
}

// cloudLoggingFields maps slog's key names onto the ones Cloud Logging treats
// specially.
func cloudLoggingFields(groups []string, a slog.Attr) slog.Attr {
	// Only rewrite the top-level built-ins; a nested attribute that happens to be
	// called "msg" belongs to whoever logged it.
	if len(groups) > 0 {
		return a
	}

	switch a.Key {
	case slog.LevelKey:
		a.Key = "severity"
		if level, ok := a.Value.Any().(slog.Level); ok {
			a.Value = slog.StringValue(cloudSeverity(level))
		}
	case slog.MessageKey:
		a.Key = "message"
	case slog.TimeKey:
		a.Key = "timestamp"
	}
	return a
}

// cloudSeverity translates a slog level into one of Cloud Logging's severity
// names. slog's WARN is spelled WARNING there, and anything above ERROR is
// reported as CRITICAL so it stands out from ordinary handler failures.
func cloudSeverity(level slog.Level) string {
	switch {
	case level < slog.LevelInfo:
		return "DEBUG"
	case level < slog.LevelWarn:
		return "INFO"
	case level < slog.LevelError:
		return "WARNING"
	case level < slog.LevelError+4:
		return "ERROR"
	default:
		return "CRITICAL"
	}
}

// Unexported key types, so nothing outside this package can collide with these
// context entries or overwrite them. The pre-existing JWT middleware stores its
// token under the plain string "user", which is exactly the collision this
// avoids.
type (
	loggerKey    struct{}
	requestIDKey struct{}
)

// WithLogger attaches a logger to ctx.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

// FromContext returns the request-scoped logger, or the default one when called
// outside a request. It never returns nil, so a caller does not have to guard
// every log statement.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// WithRequestID attaches a request id to ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request id, or "" outside a request.
func RequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}

// NewRequestID returns a short random identifier.
//
// 8 bytes rather than a UUID: this only has to be unique across the requests in
// flight and in a log retention window, not globally, and a 16-character id is
// short enough to paste into a support conversation.
func NewRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice, but a panic here would take the
		// process down over a log label. A timestamp is not collision-proof; it is
		// good enough to keep one request's lines together, which is the point.
		return "t" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}
