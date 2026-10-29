// Package metrics exposes the Prometheus metrics of the service.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Inbound HTTP requests by route pattern, method and status.",
	}, []string{"route", "method", "status"})

	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Inbound HTTP request latency by route pattern.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "method"})

	// OutboundRequests counts calls to other services by target and outcome.
	OutboundRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "outbound_requests_total",
		Help: "Calls to other services by target and outcome (ok, error, rejected).",
	}, []string{"target", "outcome"})

	// BreakerState is 0 closed, 1 half-open, 2 open, per target.
	BreakerState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "circuit_breaker_state",
		Help: "Circuit breaker state per target: 0 closed, 1 half-open, 2 open.",
	}, []string{"target"})
)

// Handler serves /metrics.
func Handler() http.Handler { return promhttp.Handler() }

// Middleware records every request against its chi route pattern (not the
// raw path, which would explode the cardinality with ids).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		httpRequests.WithLabelValues(route, r.Method, strconv.Itoa(rec.status)).Inc()
		httpDuration.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
