package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// newCapturing builds a logger with the same options as Init, writing to a buffer
// so the emitted JSON can be inspected.
func newCapturing(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{
		Level:       slog.LevelDebug,
		ReplaceAttr: cloudLoggingFields,
	}))
}

func decodeEntry(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v\ngot: %s", err, buf.String())
	}
	return entry
}

// TestCloudLoggingUsesTheFieldNamesGCPReads is the whole reason ReplaceAttr
// exists. slog's defaults are "level", "msg" and "time"; Cloud Logging looks for
// "severity", "message" and "timestamp" and treats anything else as an ordinary
// payload field. With the defaults, every entry shows up at severity "default",
// so filtering by severity in the log viewer returns nothing.
func TestCloudLoggingUsesTheFieldNamesGCPReads(t *testing.T) {
	var buf bytes.Buffer
	newCapturing(&buf).Warn("disk is filling up", slog.String("mount", "/data"))

	entry := decodeEntry(t, &buf)

	for _, want := range []string{"severity", "message", "timestamp"} {
		if _, ok := entry[want]; !ok {
			t.Errorf("no %q field; Cloud Logging will not read this entry", want)
		}
	}
	for _, unwanted := range []string{"level", "msg", "time"} {
		if _, ok := entry[unwanted]; ok {
			t.Errorf("slog's default key %q survived the rewrite", unwanted)
		}
	}

	if entry["message"] != "disk is filling up" {
		t.Errorf("message = %v, want the log message", entry["message"])
	}
	// Ordinary attributes must pass through untouched.
	if entry["mount"] != "/data" {
		t.Errorf("mount = %v, want /data", entry["mount"])
	}
}

func TestSeverityMapping(t *testing.T) {
	cases := []struct {
		level slog.Level
		want  string
	}{
		{slog.LevelDebug, "DEBUG"},
		{slog.LevelInfo, "INFO"},
		// slog spells this WARN; Cloud Logging spells it WARNING, and an
		// unrecognised value is silently downgraded to "default".
		{slog.LevelWarn, "WARNING"},
		{slog.LevelError, "ERROR"},
		{slog.LevelError + 4, "CRITICAL"},
	}

	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			var buf bytes.Buffer
			newCapturing(&buf).Log(context.Background(), tc.level, "x")

			if got := decodeEntry(t, &buf)["severity"]; got != tc.want {
				t.Errorf("severity for %v = %v, want %v", tc.level, got, tc.want)
			}
		})
	}
}

// TestNestedAttrsAreNotRewritten checks the len(groups) guard. An attribute a
// caller happens to name "msg" inside a group belongs to that caller; rewriting it
// would corrupt their data.
func TestNestedAttrsAreNotRewritten(t *testing.T) {
	var buf bytes.Buffer
	newCapturing(&buf).Info("outer", slog.Group("inner",
		slog.String("msg", "keep me"),
		slog.String("level", "keep me too"),
	))

	entry := decodeEntry(t, &buf)
	inner, ok := entry["inner"].(map[string]any)
	if !ok {
		t.Fatalf("inner group missing: %v", entry)
	}
	if inner["msg"] != "keep me" {
		t.Errorf(`inner "msg" = %v, want "keep me"`, inner["msg"])
	}
	if inner["level"] != "keep me too" {
		t.Errorf(`inner "level" = %v, want "keep me too"`, inner["level"])
	}
}

func TestFromContextFallsBackToDefault(t *testing.T) {
	if FromContext(context.Background()) == nil {
		t.Fatal("FromContext returned nil; every call site would need a nil guard")
	}

	var buf bytes.Buffer
	want := newCapturing(&buf)
	ctx := WithLogger(context.Background(), want)
	if FromContext(ctx) != want {
		t.Error("FromContext did not return the logger that was attached")
	}
}

func TestRequestIDRoundTrip(t *testing.T) {
	if got := RequestID(context.Background()); got != "" {
		t.Errorf("RequestID outside a request = %q, want empty", got)
	}

	ctx := WithRequestID(context.Background(), "abc123")
	if got := RequestID(ctx); got != "abc123" {
		t.Errorf("RequestID = %q, want abc123", got)
	}
}

func TestNewRequestIDIsUniqueAndSafe(t *testing.T) {
	const n = 1000
	seen := make(map[string]bool, n)

	for i := 0; i < n; i++ {
		id := NewRequestID()
		if id == "" {
			t.Fatal("empty request id")
		}
		if seen[id] {
			t.Fatalf("duplicate request id %q after %d draws", id, i)
		}
		seen[id] = true

		// Must be safe to drop into a log line and a JSON string unescaped.
		if strings.ContainsAny(id, "\n\r\"\\ ") {
			t.Fatalf("request id %q contains a character that could forge a log entry", id)
		}
	}
}
