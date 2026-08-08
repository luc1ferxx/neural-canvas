// Package metrics holds the Prometheus instruments and the helpers for
// recording to them.
//
// It is a separate package so that store and service can record without
// importing handler, and so the label sets live in one file where their
// cardinality can be reviewed at a glance. Cardinality is the thing that goes
// wrong with metrics: every distinct label combination is a separate time series
// held in memory by both this process and the scraper, so a label that can take
// unbounded values -- a raw URL path, a user id, an error message -- turns a
// cheap counter into an unbounded memory leak. Every label below is bounded by
// something structural: the number of routes, the number of HTTP methods, the
// number of status codes actually returned.
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// durationBuckets deliberately replaces prometheus.DefBuckets, whose largest
// bucket is 10 seconds.
//
// This API has a request that routinely takes 10 to 30 seconds: /generate waits
// on DALL-E and then on a storage upload. With the defaults every one of those
// would fall into the +Inf bucket, which is the one bucket a histogram quantile
// cannot interpolate within -- so the p99 of the single most expensive endpoint
// in the service would be unmeasurable, while everything else looked fine.
//
// The low end is 5ms rather than 1ms because nothing here is faster than a
// network round trip to Elasticsearch.
var durationBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
}

var (
	// RequestsTotal counts responses, not requests: it is incremented after the
	// handler returns, so the status is known.
	//
	// route is the mux path template ("/post/{id}"), never the raw path. With the
	// raw path, a single client walking /post/1, /post/2 ... would create one
	// series per id.
	RequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP responses by method, route template and status code.",
	}, []string{"method", "route", "status"})

	// RequestDuration excludes the status label on purpose. Adding it would
	// multiply the series count by the number of status codes for what is almost
	// always the same question -- how slow is this route -- and errors are usually
	// fast, so mixing them in makes latency look better than it is.
	RequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration by method and route template.",
		Buckets: durationBuckets,
	}, []string{"method", "route"})

	// RequestsInFlight is what distinguishes "slow" from "stuck". A latency
	// histogram only records requests that finished; a request hanging on a
	// dependency forever contributes nothing to it, so an outage where everything
	// blocks looks like an absence of traffic rather than a problem.
	RequestsInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "http_requests_in_flight",
		Help: "HTTP requests currently being served.",
	})

	// GenerationsTotal exists because this is the metric with a currency attached.
	// Every increment with provider="openai" is money, so it is the one counter
	// worth alerting on for cost rather than for correctness.
	GenerationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "image_generations_total",
		Help: "Image generations by provider and outcome.",
	}, []string{"provider", "result"})

	// QuotaRejectionsTotal separates "users are hitting the daily cap" from
	// "generation is broken", which are both 4xx-ish symptoms with completely
	// different responses.
	QuotaRejectionsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "generation_quota_rejections_total",
		Help: "Generation requests refused because the caller's daily quota was spent.",
	})

	// ElasticsearchDuration is here because Elasticsearch is on the path of every
	// authenticated request -- the session-revocation check is one get per request
	// -- so it is the most likely explanation for a latency change that the HTTP
	// metrics alone cannot attribute.
	//
	// The operation label is a fixed string chosen at each call site, not a query
	// or an index name, so it stays bounded.
	ElasticsearchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "elasticsearch_operation_duration_seconds",
		Help:    "Elasticsearch operation duration by operation name.",
		Buckets: durationBuckets,
	}, []string{"operation"})
)

// ObserveElasticsearch records how long an operation took.
//
// It takes the start time rather than a duration so call sites can be a single
// deferred line:
//
//	defer metrics.ObserveElasticsearch(metrics.OpGetDocument, time.Now())
//
// which works because deferred arguments are evaluated where the defer appears,
// not where it runs.
func ObserveElasticsearch(operation string, start time.Time) {
	ElasticsearchDuration.WithLabelValues(operation).Observe(time.Since(start).Seconds())
}

// Elasticsearch operation names, as constants rather than literals at the call
// sites.
//
// The reason is drift. The package init below pre-creates a series per operation,
// and if a call site said "get_doc" while init said "get_document", the result
// would be two series: one permanently at zero and one that no dashboard was built
// against. Nothing would fail; the metric would just quietly be wrong.
const (
	OpPing             = "ping"
	OpIndexExists      = "index_exists"
	OpCount            = "count"
	OpEnsureIndex      = "ensure_index"
	OpDeleteIndex      = "delete_index"
	OpReindex          = "reindex"
	OpSearch           = "search"
	OpSearchPaged      = "search_paged"
	OpDeleteByQuery    = "delete_by_query"
	OpGetDocument      = "get_document"
	OpUpdateFields     = "update_fields"
	OpUpdateWithScript = "update_with_script"
	OpDeleteDocument   = "delete_document"
	OpCreateDocument   = "create_document"
	OpIndexDocument    = "index_document"
)

// init materialises every series this process can produce, at zero.
//
// A labelled Prometheus metric does not exist until something observes it, so a
// freshly started process serves a /metrics with no image_generations_total in it
// at all. That breaks two things. An alert or dashboard panel referring to the
// metric errors rather than reading zero, and -- worse -- "no generations have
// happened yet" becomes indistinguishable from "the metric name is misspelled" or
// "that code path was never deployed". Both are silent.
//
// The cost is the series held for label combinations that may never occur: 4
// counters and 15 histograms. For a Prometheus that is nothing, and it buys a
// /metrics whose shape does not depend on what has happened to have run yet.
//
// This is a package init rather than an exported Init that main calls, which is
// the unusual choice and deliberate. The property being bought is "every process
// that can serve this metric serves it from the first scrape". An exported
// function makes that property depend on each binary remembering to call it, and
// the failure mode of forgetting is invisible -- the metrics endpoint still works,
// it just silently omits whatever has not happened yet. The usual arguments for
// explicit initialisation do not apply here: this reads no configuration, touches
// no network or disk, cannot fail, and does not care what order it runs in.
// promauto has already registered the collectors during variable initialisation,
// which by Go's rules completes before any init in the package body.
func init() {
	for _, provider := range []string{ProviderLabelOpenAI, ProviderLabelStub} {
		for _, result := range []string{ResultSuccess, ResultError} {
			GenerationsTotal.WithLabelValues(provider, result)
		}
	}
	QuotaRejectionsTotal.Add(0)

	for _, op := range []string{
		OpPing, OpIndexExists, OpCount, OpEnsureIndex, OpDeleteIndex, OpReindex,
		OpSearch, OpSearchPaged, OpDeleteByQuery, OpGetDocument, OpUpdateFields,
		OpUpdateWithScript, OpDeleteDocument, OpCreateDocument, OpIndexDocument,
	} {
		ElasticsearchDuration.WithLabelValues(op)
	}
}

// Label values for GenerationsTotal. They mirror config.ProviderOpenAI and
// config.ProviderStub but are declared here so that metrics does not import
// config, which would make the dependency run the wrong way -- config is loaded
// before anything else and must not pull in the rest of the program.
const (
	ProviderLabelOpenAI = "openai"
	ProviderLabelStub   = "stub"

	ResultSuccess = "success"
	ResultError   = "error"
)
