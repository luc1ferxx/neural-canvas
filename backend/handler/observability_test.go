package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// decodeError reads the error envelope, failing the test if the body is not one.
func decodeError(t *testing.T, body []byte) errorDetail {
	t.Helper()

	var resp errorResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response is not a JSON error envelope: %v\nbody: %s", err, body)
	}
	return resp.Error
}

// TestErrorsAreJSONNotPlainText is the regression guard for the old behaviour:
// 35 failure paths answered with text/plain prose, so a JSON client had to guess.
func TestErrorsAreJSONNotPlainText(t *testing.T) {
	router := newTestRouter(t)

	cases := []struct {
		name     string
		method   string
		path     string
		wantCode string
	}{
		{"missing token", http.MethodGet, "/search", codeUnauthorized},
		{"missing token on upload", http.MethodPost, "/upload", codeUnauthorized},
		{"missing token on delete", http.MethodDelete, "/post/abc", codeUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}

			detail := decodeError(t, rec.Body.Bytes())
			if detail.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", detail.Code, tc.wantCode)
			}
			if detail.Message == "" {
				t.Error("message is empty; a client has nothing to show the user")
			}
			if detail.RequestID == "" {
				t.Error("request_id is empty; a user cannot quote anything actionable")
			}
		})
	}
}

// TestSignupValidationErrorsAreDistinguishable is why codes exist at all. Both of
// these are 400, so a client branching on status alone cannot tell which field to
// highlight.
func TestSignupValidationErrorsAreDistinguishable(t *testing.T) {
	router := newTestRouter(t)

	cases := []struct {
		name string
		body string
		want string
	}{
		{"unparseable body", `not json`, codeInvalidRequest},
		{"missing fields", `{}`, codeInvalidRequest},
		{"bad username", `{"username":"A!","password":"longenough1"}`, codeInvalidRequest},
		{"short password", `{"username":"valid-name","password":"short"}`, codeInvalidRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			detail := decodeError(t, rec.Body.Bytes())
			if detail.Code != tc.want {
				t.Errorf("code = %q, want %q", detail.Code, tc.want)
			}
		})
	}
}

// TestRequestIDIsGeneratedAndEchoed checks the correlation handle exists and is
// visible to the client.
func TestRequestIDIsGeneratedAndEchoed(t *testing.T) {
	router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/search", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	header := rec.Header().Get(requestIDHeader)
	if header == "" {
		t.Fatal("no X-Request-Id on the response")
	}

	// The id in the body must be the same one, otherwise quoting it is useless.
	detail := decodeError(t, rec.Body.Bytes())
	if detail.RequestID != header {
		t.Errorf("body request_id = %q but header = %q; they must match",
			detail.RequestID, header)
	}
}

// TestRequestIDFromUpstreamIsPreserved covers the case where a load balancer or a
// client has already assigned one: replacing it would break correlation across
// the hop.
func TestRequestIDFromUpstreamIsPreserved(t *testing.T) {
	router := newTestRouter(t)

	const upstream = "abc123-DEF_456"
	req := httptest.NewRequest(http.MethodGet, "/search", nil)
	req.Header.Set(requestIDHeader, upstream)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if got := rec.Header().Get(requestIDHeader); got != upstream {
		t.Errorf("X-Request-Id = %q, want the upstream value %q", got, upstream)
	}
}

// TestMaliciousRequestIDIsRejected covers log injection. A line-oriented log
// means an id containing a newline can forge entries; an unbounded one can flood
// every line the request emits.
func TestMaliciousRequestIDIsRejected(t *testing.T) {
	router := newTestRouter(t)

	cases := []struct {
		name string
		id   string
	}{
		{"newline", "abc\ndef"},
		{"carriage return", "abc\rdef"},
		{"forged json", `abc","severity":"EMERGENCY`},
		{"too long", strings.Repeat("a", maxInboundRequestID+1)},
		{"space", "abc def"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/search", nil)
			// Set directly on the map: http.Header.Set would not carry a raw
			// newline through, and the point is to test the guard, not net/http.
			req.Header[requestIDHeader] = []string{tc.id}

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			got := rec.Header().Get(requestIDHeader)
			if got == tc.id {
				t.Errorf("the hostile id %q was accepted verbatim", tc.id)
			}
			if got == "" {
				t.Error("no id was substituted; correlation is lost entirely")
			}
		})
	}
}

func TestSanitizeRequestID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"plain123", "plain123"},
		{"with-dash_and_underscore", "with-dash_and_underscore"},
		{"  padded  ", "padded"},
		{"", ""},
		{"has space", ""},
		{"has\nnewline", ""},
		{"semi;colon", ""},
		{`quote"`, ""},
		{strings.Repeat("a", maxInboundRequestID), strings.Repeat("a", maxInboundRequestID)},
		{strings.Repeat("a", maxInboundRequestID+1), ""},
	}

	for _, tc := range cases {
		if got := sanitizeRequestID(tc.in); got != tc.want {
			t.Errorf("sanitizeRequestID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestHealthzIgnoresDependencies is the liveness/readiness distinction, asserted.
//
// store.ESBackend is nil in this test, i.e. Elasticsearch is as unavailable as it
// can get. Liveness must still pass: if it failed here, an orchestrator would
// respond to an Elasticsearch outage by killing every instance, which does not
// fix Elasticsearch and turns a dependency blip into a restart loop.
func TestHealthzIgnoresDependencies(t *testing.T) {
	router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, healthzPath, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: liveness must not depend on Elasticsearch", rec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf(`status = %q, want "ok"`, body["status"])
	}
}

// TestReadyzFailsWithoutStorage is the other half: readiness reports that this
// instance cannot serve, so it leaves the rotation without being killed.
func TestReadyzFailsWithoutStorage(t *testing.T) {
	router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, readyzPath, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 with no Elasticsearch", rec.Code)
	}
	if detail := decodeError(t, rec.Body.Bytes()); detail.Code != codeUnavailable {
		t.Errorf("code = %q, want %q", detail.Code, codeUnavailable)
	}
}

// TestProbesNeedNoToken guards against the probes being put behind the JWT
// middleware, which would make every instance permanently unready: a load
// balancer has no credentials to present.
func TestProbesNeedNoToken(t *testing.T) {
	router := newTestRouter(t)

	for _, path := range []string{healthzPath, readyzPath} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code == http.StatusUnauthorized {
				t.Errorf("%s returned 401: probes cannot authenticate", path)
			}
			if rec.Code == http.StatusNotFound {
				t.Errorf("%s returned 404: probe is not registered", path)
			}
		})
	}
}

// TestPanicBecomesA500 checks that a panicking handler produces an attributable
// error response rather than a dropped connection. net/http on its own recovers
// to keep the process alive but sends nothing, so the client sees a transport
// error with no status and the log line has no request id on it.
func TestPanicBecomesA500(t *testing.T) {
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went badly wrong")
	})

	// The same middleware stack the router applies, minus CORS and routing.
	handler := withRequestID(accessLog(recoverPanic(panicking)))

	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	rec := httptest.NewRecorder()

	// Must not propagate: if it did, this call would end the test binary.
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}

	detail := decodeError(t, rec.Body.Bytes())
	if detail.Code != codeInternal {
		t.Errorf("code = %q, want %q", detail.Code, codeInternal)
	}
	if detail.RequestID == "" {
		t.Error("request_id is empty; the panic is not attributable to a request")
	}
	if strings.Contains(detail.Message, "badly wrong") {
		t.Error("the panic value leaked into the client-facing message")
	}
}

// TestAbortHandlerStillAborts checks the one panic value that must not be
// swallowed: http.ErrAbortHandler is net/http's documented way to abandon a
// response deliberately, and converting it into a 500 would change behaviour.
func TestAbortHandlerStillAborts(t *testing.T) {
	aborting := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	})
	handler := withRequestID(recoverPanic(aborting))

	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	rec := httptest.NewRecorder()

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Error("ErrAbortHandler was swallowed; it must reach net/http")
			return
		}
		err, ok := recovered.(error)
		if !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Errorf("recovered %v, want http.ErrAbortHandler", recovered)
		}
	}()

	handler.ServeHTTP(rec, req)
}

// TestRecordingWriterResolvesImplicitStatus covers the access log's status
// accounting. A handler that writes a body without calling WriteHeader sends 200,
// so logging the zero value would misreport every successful request.
func TestRecordingWriterResolvesImplicitStatus(t *testing.T) {
	t.Run("implicit 200", func(t *testing.T) {
		rec := httptest.NewRecorder()
		w := &recordingWriter{ResponseWriter: rec}
		if _, err := w.Write([]byte("hello")); err != nil {
			t.Fatalf("Write(): %v", err)
		}
		if got := w.statusCode(); got != http.StatusOK {
			t.Errorf("statusCode() = %d, want 200", got)
		}
		if w.bytes != 5 {
			t.Errorf("bytes = %d, want 5", w.bytes)
		}
	})

	t.Run("no write at all", func(t *testing.T) {
		rec := httptest.NewRecorder()
		w := &recordingWriter{ResponseWriter: rec}
		if got := w.statusCode(); got != http.StatusOK {
			t.Errorf("statusCode() = %d, want 200", got)
		}
	})

	t.Run("explicit status wins", func(t *testing.T) {
		rec := httptest.NewRecorder()
		w := &recordingWriter{ResponseWriter: rec}
		w.WriteHeader(http.StatusTeapot)
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatalf("Write(): %v", err)
		}
		if got := w.statusCode(); got != http.StatusTeapot {
			t.Errorf("statusCode() = %d, want 418", got)
		}
	})

	t.Run("first status wins", func(t *testing.T) {
		rec := httptest.NewRecorder()
		w := &recordingWriter{ResponseWriter: rec}
		w.WriteHeader(http.StatusNotFound)
		if got := w.statusCode(); got != http.StatusNotFound {
			t.Errorf("statusCode() = %d, want 404", got)
		}
	})
}
