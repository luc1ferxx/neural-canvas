package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"socialai/config"
)

// newTestRouter builds the real router with a valid throwaway configuration.
func newTestRouter(t *testing.T) http.Handler {
	t.Helper()

	t.Setenv("ES_URL", "https://es.invalid:9200")
	t.Setenv("ES_USERNAME", "u")
	t.Setenv("ES_PASSWORD", "p")
	t.Setenv("GCS_BUCKET", "bucket")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("JWT_SECRET", "0123456789012345678901234567890123456789")
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:3000")

	if err := config.Load(); err != nil {
		t.Fatalf("config.Load(): %v", err)
	}
	return InitRouter()
}

// TestProtectedRoutesAreRegistered checks each authenticated route answers 401
// rather than 404 when called without a token.
//
// The distinction is the point: 404 would mean the route is not registered at
// all, which is exactly what was wrong with DELETE /post/{id} -- its route was
// a commented-out line, so the frontend's delete button could never succeed.
func TestProtectedRoutesAreRegistered(t *testing.T) {
	router := newTestRouter(t)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/upload"},
		{http.MethodGet, "/search"},
		{http.MethodPost, "/generate"},
		{http.MethodDelete, "/post/some-id"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code == http.StatusNotFound {
				t.Fatalf("%s %s returned 404: route is not registered", tc.method, tc.path)
			}
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s returned %d, want 401 (missing token)", tc.method, tc.path, rec.Code)
			}
		})
	}
}

// TestPublicRoutesDoNotRequireAuth confirms signup and signin are reachable
// without a token. They must not answer 401 or 404; a 400 for an empty body is
// the expected outcome.
func TestPublicRoutesDoNotRequireAuth(t *testing.T) {
	router := newTestRouter(t)

	for _, path := range []string{"/signup", "/signin"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code == http.StatusNotFound {
				t.Fatalf("%s returned 404: route is not registered", path)
			}
			if rec.Code == http.StatusUnauthorized {
				t.Fatalf("%s returned 401: it must be reachable without a token", path)
			}
		})
	}
}

// TestUnknownRouteIs404 guards the test above: it only means something if an
// unregistered path really does produce 404.
func TestUnknownRouteIs404(t *testing.T) {
	router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/definitely-not-a-route", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown route returned %d, want 404", rec.Code)
	}
}

// TestWildcardOriginIsRejected pins the CORS decision: the config layer must
// refuse "*" because every one of these endpoints is authenticated.
func TestWildcardOriginIsRejected(t *testing.T) {
	t.Setenv("ES_URL", "https://es.invalid:9200")
	t.Setenv("ES_USERNAME", "u")
	t.Setenv("ES_PASSWORD", "p")
	t.Setenv("GCS_BUCKET", "bucket")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("JWT_SECRET", "0123456789012345678901234567890123456789")
	t.Setenv("ALLOWED_ORIGINS", "*")

	if err := config.Load(); err == nil {
		t.Fatal(`config.Load() accepted ALLOWED_ORIGINS="*"`)
	}
}
