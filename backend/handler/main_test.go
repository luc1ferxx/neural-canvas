package handler

import (
	"io"
	"log/slog"
	"os"
	"testing"
)

// TestMain discards log output. The access log emits a line per request, and with
// a few dozen requests across these tests it buries the actual assertion
// failures. Set LOG_TEST_OUTPUT=1 to see it while debugging.
func TestMain(m *testing.M) {
	if os.Getenv("LOG_TEST_OUTPUT") == "" {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	}
	os.Exit(m.Run())
}
