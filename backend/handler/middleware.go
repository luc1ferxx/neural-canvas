package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/luc1ferxx/neural-canvas/backend/logging"
)

// requestIDHeader is both read and written. Reading it means an id assigned
// upstream -- by a load balancer, or by a client correlating its own retries --
// survives instead of being replaced, so one identifier spans the whole path.
const requestIDHeader = "X-Request-Id"

// maxInboundRequestID bounds what is accepted from a client. Without a cap, a
// caller could push a megabyte of text into every log line this request emits.
const maxInboundRequestID = 64

// withRequestID assigns an id to each request, puts a logger carrying it on the
// context, and echoes it back in the response header.
//
// The echo is what makes an error report actionable: the id in the failure
// response the user saw is the same id on every log line the request produced.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeRequestID(r.Header.Get(requestIDHeader))
		if id == "" {
			id = logging.NewRequestID()
		}

		ctx := logging.WithRequestID(r.Context(), id)
		ctx = logging.WithLogger(ctx, slog.Default().With(slog.String("request_id", id)))

		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// sanitizeRequestID accepts an upstream id only if it is plausibly one. Anything
// with a newline in it could forge extra entries in a line-oriented log, and
// anything oversized is dropped rather than truncated.
func sanitizeRequestID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxInboundRequestID {
		return ""
	}
	for _, c := range raw {
		alphanumeric := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !alphanumeric && c != '-' && c != '_' {
			return ""
		}
	}
	return raw
}

// recordingWriter captures the status and size for the access log.
//
// http.ResponseWriter reports neither after the fact, and a handler that never
// calls WriteHeader still sends 200, so the zero value has to be interpreted
// rather than logged as a literal zero.
type recordingWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *recordingWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *recordingWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// statusCode reports what was actually sent, resolving the implicit 200.
func (w *recordingWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// accessLog emits one entry per request.
//
// Server errors log at error level and client errors at warn, so a dashboard can
// separate "this service is broken" from "someone sent a bad request" -- without
// that split, a spike of 401s from a stale frontend looks identical to an outage.
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Probes run every few seconds forever. Logging them buries everything
		// else and costs money per ingested line.
		if isProbe(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		rec := &recordingWriter{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		status := rec.statusCode()
		logger := logging.FromContext(r.Context())
		attrs := []any{
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", status),
			slog.Int("bytes", rec.bytes),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		}

		switch {
		case status >= http.StatusInternalServerError:
			logger.Error("request failed", attrs...)
		case status >= http.StatusBadRequest:
			logger.Warn("request rejected", attrs...)
		default:
			logger.Info("request", attrs...)
		}
	})
}

// recoverPanic turns a panicking handler into a 500 instead of a dropped
// connection.
//
// net/http already recovers a panic to keep the process alive, but it kills the
// connection without a response, so the client sees a transport error rather
// than a status code and the log entry lands outside this request's context with
// no request id on it. Recovering here keeps the failure attributable.
func recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				// http.ErrAbortHandler is a deliberate abort, not a bug; passing it
				// on preserves net/http's documented behaviour. Matched with
				// errors.Is rather than ==, so a wrapped one is still honoured.
				if err, ok := recovered.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(recovered)
				}

				logging.FromContext(r.Context()).Error("handler panicked",
					slog.Any("panic", recovered),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("stack", string(debug.Stack())),
				)

				writeError(w, r, http.StatusInternalServerError, codeInternal,
					"Something went wrong, please try again")
			}
		}()

		next.ServeHTTP(w, r)
	})
}
