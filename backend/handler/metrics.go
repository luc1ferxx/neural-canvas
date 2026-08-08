package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/luc1ferxx/neural-canvas/backend/metrics"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const metricsPath = "/metrics"

// routeUnmatched labels requests that matched no route.
//
// Those requests are exactly the ones with an unbounded path space -- a scanner
// probing /wp-login.php, /.env, /admin -- so they must collapse into one series
// rather than each creating their own.
const routeUnmatched = "<unmatched>"

// instrument records the three HTTP metrics for every request.
//
// It takes the router because it is installed *around* the whole handler chain
// rather than with router.Use, and outside the router it has no other way to name
// the route.
//
// router.Use was the first attempt and it is the obvious one, since mux populates
// mux.CurrentRoute before the middleware runs. But mux middleware only runs on
// requests that matched a route, so everything a scanner sends -- the requests
// most worth watching -- was invisible to it. Working around that with a second
// recording path in the NotFound and MethodNotAllowed handlers left three
// inconsistencies: the in-flight gauge never saw unmatched requests, the duration
// histogram's _count did not equal http_requests_total (so a dashboard computing
// error rate from one and latency from the other disagreed about how many requests
// there had been), and two separate places had to be kept in step.
//
// Matching a second time here is the price. It is a linear walk over nine routes
// with compiled regexps, against a handler that then talks to Elasticsearch over
// the network.
func instrument(router *mux.Router) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			metrics.RequestsInFlight.Inc()
			defer metrics.RequestsInFlight.Dec()

			start := time.Now()
			rec := &recordingWriter{ResponseWriter: w}

			next.ServeHTTP(rec, r)

			route := routeTemplate(router, r)
			metrics.RequestDuration.WithLabelValues(r.Method, route).
				Observe(time.Since(start).Seconds())
			metrics.RequestsTotal.WithLabelValues(
				r.Method, route, strconv.Itoa(rec.statusCode()),
			).Inc()
		})
	}
}

// routeTemplate returns the pattern of the route this request would match, or
// routeUnmatched.
//
// Match does not modify the request; it fills in the RouteMatch, which is why
// this can run before the router has served anything.
//
// Two cases come back as routeUnmatched even though the path is real, both
// verified against mux rather than assumed. A known path with the wrong method
// sets MatchErr to ErrMethodMismatch and leaves Route nil, so every 405 is
// attributed to routeUnmatched rather than to the endpoint the client meant. So is
// a CORS preflight, since OPTIONS is not in any route's method list. Neither loses
// anything that matters -- both are bounded, and the status label already
// distinguishes them -- but it does mean a 405 count cannot be broken down by
// endpoint.
func routeTemplate(router *mux.Router, r *http.Request) string {
	var match mux.RouteMatch
	router.Match(r, &match)

	if match.Route == nil {
		return routeUnmatched
	}
	// GetPathTemplate errors only for routes defined without a path, which this
	// router has none of -- but falling back is cheaper than a panic in a
	// middleware that runs on every request.
	template, err := match.Route.GetPathTemplate()
	if err != nil || template == "" {
		return routeUnmatched
	}
	return template
}

// notFoundHandler answers unmatched paths with the API's error envelope.
//
// mux's default is http.NotFound, which sends "404 page not found" as
// text/plain. That left the second-most-common error in any HTTP API as one a
// JSON client cannot parse -- the same defect the JWT middleware's default
// handler had.
func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusNotFound, codeNotFound, "No such endpoint")
}

// methodNotAllowedHandler is the same argument for 405. Sending the wrong method
// to a real path is a client bug worth reporting in a parseable shape.
func methodNotAllowedHandler(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusMethodNotAllowed, codeInvalidRequest,
		"That method is not allowed on this endpoint")
}

// AdminHandler serves /metrics.
//
// It is returned separately from InitRouter so it can be served on its own
// listener. Metrics belong on a port that is not the public one: they expose
// route names, traffic volumes, error rates, Go runtime internals and the
// process command line, none of which should be readable by whoever can reach
// the API. Putting it behind the API's JWT instead would be worse -- Prometheus
// would need a user account, and a scraper that has to authenticate is a scraper
// that stops working when auth breaks, which is when metrics matter most.
func AdminHandler() http.Handler {
	admin := http.NewServeMux()
	admin.Handle(metricsPath, promhttp.Handler())
	return admin
}
