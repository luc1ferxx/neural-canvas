package handler

import (
	"log/slog"
	"net/http"

	"github.com/luc1ferxx/neural-canvas/backend/logging"
	"github.com/luc1ferxx/neural-canvas/backend/store"
)

const (
	healthzPath = "/healthz"
	readyzPath  = "/readyz"
)

// isProbe reports whether a path is one of the probes, which the access log
// skips.
func isProbe(path string) bool {
	return path == healthzPath || path == readyzPath
}

// healthzHandler answers whether this process is alive. It deliberately checks
// nothing else.
//
// The distinction from readiness is the whole reason there are two endpoints. A
// liveness probe that failed when Elasticsearch was unreachable would cause the
// orchestrator to kill and restart every instance -- which does not repair
// Elasticsearch, and turns a recoverable dependency outage into a restart loop
// that also destroys any instance that was serving cached traffic fine. Liveness
// asks "is this process wedged"; only a restart can fix that, and only this
// process can answer it.
func healthzHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyzHandler answers whether this process can serve traffic, which means
// checking the dependency it cannot work without.
//
// Failing here takes the instance out of the load balancer's rotation without
// killing it, so it rejoins by itself once Elasticsearch answers again. 503 is
// the correct status: it tells a load balancer to look elsewhere, whereas a 500
// invites a retry against the same instance.
func readyzHandler(w http.ResponseWriter, r *http.Request) {
	if store.ESBackend == nil {
		writeError(w, r, http.StatusServiceUnavailable, codeUnavailable,
			"Not ready: storage is not initialised")
		return
	}

	if err := store.ESBackend.Ping(r.Context()); err != nil {
		// Logged at warn, not error: an unready instance during a dependency blip
		// is the system working as designed, and paging on it trains people to
		// ignore the alert.
		logging.FromContext(r.Context()).Warn("readiness check failed",
			slog.String("cause", err.Error()))
		writeError(w, r, http.StatusServiceUnavailable, codeUnavailable,
			"Not ready: storage is unreachable")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
