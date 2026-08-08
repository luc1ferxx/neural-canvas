package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/luc1ferxx/neural-canvas/backend/metrics"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// countSeries reports how many distinct label combinations exist for a metric,
// and how many of them carry a given label value.
//
// The number of series is the thing worth asserting about a metrics change: a
// counter that answers the same question with ten series instead of ten thousand
// is the difference between a metric and an outage of the monitoring system.
func countSeries(t *testing.T, collector prometheus.Collector) []*dto.Metric {
	t.Helper()

	ch := make(chan prometheus.Metric, 4096)
	collector.Collect(ch)
	close(ch)

	var out []*dto.Metric
	for m := range ch {
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatalf("could not read metric: %v", err)
		}
		out = append(out, &pb)
	}
	return out
}

func labelValue(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

// TestUnmatchedPathsCollapseIntoOneSeries is the cardinality guard.
//
// A scanner walking /admin, /.env, /wp-login.php is the normal case for anything
// on the public internet, and it is unbounded. If the route label were the raw
// path, each of those would allocate a permanent time series in this process and
// in the scraper -- an attacker-controlled memory leak in the monitoring path.
func TestUnmatchedPathsCollapseIntoOneSeries(t *testing.T) {
	router := newTestRouter(t)

	for _, path := range []string{
		"/admin", "/.env", "/wp-login.php", "/api/v1/users", "/post/1/../../etc/passwd",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	}

	var routes []string
	for _, m := range countSeries(t, metrics.RequestsTotal) {
		if route := labelValue(m, "route"); route != "" {
			routes = append(routes, route)
		}
	}

	for _, route := range routes {
		if strings.Contains(route, "admin") || strings.Contains(route, ".env") ||
			strings.Contains(route, "wp-login") || strings.Contains(route, "passwd") {
			t.Errorf("a raw request path leaked into the route label: %q", route)
		}
	}
}

// TestPathParametersDoNotCreateSeriesPerID is the same argument for a route that
// legitimately matches: /post/{id} must be one series, not one per post.
func TestPathParametersDoNotCreateSeriesPerID(t *testing.T) {
	router := newTestRouter(t)

	for _, id := range []string{"aaa", "bbb", "ccc", "ddd"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/post/"+id, nil))
	}

	for _, m := range countSeries(t, metrics.RequestsTotal) {
		route := labelValue(m, "route")
		for _, id := range []string{"aaa", "bbb", "ccc", "ddd"} {
			if strings.Contains(route, id) {
				t.Errorf("route label %q contains a path parameter value", route)
			}
		}
	}
}

// TestUnmatchedPathReturnsTheErrorEnvelope covers what mux does by default, which
// is to answer text/plain "404 page not found".
//
// That left 404 -- one of the two most common errors any HTTP API returns -- as a
// response a JSON client cannot parse, which is exactly the defect the JWT
// middleware's default handler had.
func TestUnmatchedPathReturnsTheErrorEnvelope(t *testing.T) {
	router := newTestRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no-such-endpoint", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("404 body is not the error envelope: %v (body %q)", err, rec.Body.String())
	}
	if body.Error.Code != codeNotFound {
		t.Errorf("code = %q, want %q", body.Error.Code, codeNotFound)
	}
}

// TestWrongMethodReturnsTheErrorEnvelope is the 405 half.
func TestWrongMethodReturnsTheErrorEnvelope(t *testing.T) {
	router := newTestRouter(t)

	rec := httptest.NewRecorder()
	// /signin exists, but only for POST.
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/signin", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}

	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("405 body is not the error envelope: %v (body %q)", err, rec.Body.String())
	}
	if body.Error.Code != codeInvalidRequest {
		t.Errorf("code = %q, want %q", body.Error.Code, codeInvalidRequest)
	}
}

// TestMetricsEndpointIsNotOnTheAPIRouter pins the separation.
//
// If /metrics were reachable on the API port it would publish route names,
// traffic volume, error rates, Go runtime internals and the process command line
// to anyone who can reach the API.
func TestMetricsEndpointIsNotOnTheAPIRouter(t *testing.T) {
	router := newTestRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, metricsPath, nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("GET %s on the API router = %d, want 404: metrics must not be public",
			metricsPath, rec.Code)
	}
}

// TestUnmatchedRequestsAreCountedAtAll is the regression test for the design this
// replaced.
//
// The first version installed the instrumentation with router.Use. mux middleware
// runs only after a route has matched, so every request from a scanner -- the
// traffic most worth watching -- was recorded by neither the duration histogram
// nor the in-flight gauge. A 404 flood looked exactly like silence.
func TestUnmatchedRequestsAreCountedAtAll(t *testing.T) {
	router := newTestRouter(t)

	before := observationCount(t, metrics.RequestDuration)
	router.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/.env", nil))

	if after := observationCount(t, metrics.RequestDuration); after != before+1 {
		t.Errorf("http_request_duration_seconds observations went %d -> %d; "+
			"an unmatched request was not recorded", before, after)
	}
}

// TestEveryRequestIsInBothTheCounterAndTheHistogram pins the consistency that the
// two recording paths used to break.
//
// When unmatched requests were counted by the NotFound handler but timed by the
// middleware, http_requests_total and http_request_duration_seconds_count
// disagreed about how many requests there had been -- so a dashboard reading the
// error rate off one and the latency off the other described two different
// services.
func TestEveryRequestIsInBothTheCounterAndTheHistogram(t *testing.T) {
	router := newTestRouter(t)

	counterBefore := observationCount(t, metrics.RequestsTotal)
	histogramBefore := observationCount(t, metrics.RequestDuration)

	requests := []struct {
		method, path string
	}{
		{http.MethodGet, "/healthz"},     // matched, 200
		{http.MethodGet, "/signin"},      // matched path, wrong method: 405
		{http.MethodGet, "/nope"},        // unmatched: 404
		{http.MethodDelete, "/post/abc"}, // matched, no token: 401
	}
	for _, req := range requests {
		router.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(req.method, req.path, nil))
	}

	counted := observationCount(t, metrics.RequestsTotal) - counterBefore
	timed := observationCount(t, metrics.RequestDuration) - histogramBefore

	if counted != len(requests) {
		t.Errorf("http_requests_total rose by %d, want %d", counted, len(requests))
	}
	if timed != len(requests) {
		t.Errorf("http_request_duration_seconds_count rose by %d, want %d", timed, len(requests))
	}
}

// TestInFlightGaugeReturnsToZero catches the leak that makes the gauge worse than
// useless: an Inc without a matching Dec on some path, which makes the value climb
// forever and read as a permanent overload.
func TestInFlightGaugeReturnsToZero(t *testing.T) {
	router := newTestRouter(t)

	for _, path := range []string{"/healthz", "/nope", "/signin"} {
		router.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, path, nil))
	}

	series := countSeries(t, metrics.RequestsInFlight)
	if len(series) != 1 {
		t.Fatalf("http_requests_in_flight has %d series, want 1", len(series))
	}
	if got := series[0].GetGauge().GetValue(); got != 0 {
		t.Errorf("http_requests_in_flight = %v after all requests finished, want 0", got)
	}
}

// observationCount totals a counter or histogram vector across all its series.
//
// Totalling rather than reading one series is what makes this usable as a
// before/after delta regardless of which routes other tests have exercised.
func observationCount(t *testing.T, collector prometheus.Collector) int {
	t.Helper()

	var total float64
	for _, m := range countSeries(t, collector) {
		switch {
		case m.Histogram != nil:
			total += float64(m.GetHistogram().GetSampleCount())
		case m.Counter != nil:
			total += m.GetCounter().GetValue()
		default:
			t.Fatalf("observationCount: metric is neither a counter nor a histogram")
		}
	}
	return int(total)
}

// TestPreCreatedSeriesExistWithoutTraffic covers metrics' package init.
//
// A labelled Prometheus metric does not exist until something observes it, so
// without pre-creation a freshly started process serves a /metrics containing no
// image_generations_total at all -- and "no image has been generated yet" is then
// indistinguishable from "the metric was renamed" or "that code path was never
// deployed". Both are silent, and both are the kind of thing found out during an
// incident.
//
// The assertion is on exact series counts rather than on substrings in the
// endpoint output, because that is what makes it independent of test order.
// Nothing else in this package generates an image or touches Elasticsearch, so
// these two numbers are the same whether this test runs first or last -- and both
// drop to zero if the init is deleted. An earlier version of this test read
// /metrics for the metric names instead, and passed only because a sibling test
// had happened to run first and produce the series.
func TestPreCreatedSeriesExistWithoutTraffic(t *testing.T) {
	generations := countSeries(t, metrics.GenerationsTotal)
	// 2 providers x 2 outcomes.
	if len(generations) != 4 {
		t.Errorf("image_generations_total has %d series, want 4 pre-created", len(generations))
	}

	seen := map[string]bool{}
	for _, m := range generations {
		seen[labelValue(m, "provider")+"/"+labelValue(m, "result")] = true
	}
	for _, want := range []string{
		metrics.ProviderLabelOpenAI + "/" + metrics.ResultSuccess,
		metrics.ProviderLabelOpenAI + "/" + metrics.ResultError,
		metrics.ProviderLabelStub + "/" + metrics.ResultSuccess,
		metrics.ProviderLabelStub + "/" + metrics.ResultError,
	} {
		if !seen[want] {
			t.Errorf("image_generations_total is missing the %s series", want)
		}
	}

	// One per Op* constant. The count is asserted rather than the names because a
	// new operation added to one list and not the other is the failure this catches.
	if es := countSeries(t, metrics.ElasticsearchDuration); len(es) != 15 {
		t.Errorf("elasticsearch_operation_duration_seconds has %d series, want one per operation (15)",
			len(es))
	}
}

// TestAdminHandlerServesMetrics is the other half of the separation: /metrics has
// to be somewhere.
//
// It serves an API request first, deliberately. http_requests_total is not
// pre-created -- unlike the counters above, its label set includes the status
// code, and enumerating route x method x status would invent series for
// combinations that can never happen. So the HTTP families appear on first
// traffic, and this test makes that traffic itself rather than depending on
// another test to have made it.
func TestAdminHandlerServesMetrics(t *testing.T) {
	router := newTestRouter(t)
	router.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/anything", nil))

	rec := httptest.NewRecorder()
	AdminHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, metricsPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s on the admin handler = %d, want 200", metricsPath, rec.Code)
	}

	body := rec.Body.String()
	for _, want := range []string{
		"http_requests_total",
		"http_request_duration_seconds",
		"http_requests_in_flight",
		"image_generations_total",
		"elasticsearch_operation_duration_seconds",
		"generation_quota_rejections_total",
		// Registered by client_golang's default registry. Worth pinning: goroutine
		// count and heap size are what distinguish "the dependency is slow" from
		// "this process is leaking".
		"go_goroutines",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s is missing from /metrics", want)
		}
	}
}

// TestDurationBucketsCoverTheSlowestEndpoint guards the reason for replacing
// prometheus.DefBuckets, whose top bucket is 10s. /generate routinely takes 10-30s,
// and a request past the last finite bucket lands in +Inf, where a quantile cannot
// be interpolated -- so the p99 of the most expensive endpoint would be unknowable.
func TestDurationBucketsCoverTheSlowestEndpoint(t *testing.T) {
	rec := httptest.NewRecorder()
	AdminHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, metricsPath, nil))

	// generateTimeout in service is 120s; the buckets must at least reach the range
	// a normal generate falls in.
	if !strings.Contains(rec.Body.String(), `le="30"`) {
		t.Error("no 30s bucket: a 10-30s /generate would only be visible in +Inf")
	}
}
