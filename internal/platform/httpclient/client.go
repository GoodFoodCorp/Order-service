// Package httpclient builds the HTTP clients used to call other services:
// bounded timeouts, a pooled transport, request-id propagation and a circuit
// breaker, so a slow or failing dependency is cut off quickly instead of
// tying up this service's goroutines and connections.
package httpclient

import (
	"errors"
	"net/http"
	"time"

	"github.com/sony/gobreaker/v2"

	"goodfood/order-service/internal/platform/metrics"
	"goodfood/order-service/internal/platform/requestid"
)

// ErrCircuitOpen is returned without calling the target while its breaker is
// open.
var ErrCircuitOpen = errors.New("circuit open: dependency temporarily disabled")

// Settings tune a client; zero values take the defaults.
type Settings struct {
	Timeout time.Duration
	// ConsecutiveFailures opens the breaker (default 5).
	ConsecutiveFailures uint32
	// OpenFor is how long the breaker stays open before a trial call (default 15s).
	OpenFor time.Duration
}

// New returns a client for the named target (used in metrics).
func New(target string, s Settings) *http.Client {
	if s.Timeout == 0 {
		s.Timeout = 5 * time.Second
	}
	if s.ConsecutiveFailures == 0 {
		s.ConsecutiveFailures = 5
	}
	if s.OpenFor == 0 {
		s.OpenFor = 15 * time.Second
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.MaxIdleConnsPerHost = 100
	base.IdleConnTimeout = 90 * time.Second

	breaker := gobreaker.NewCircuitBreaker[*http.Response](gobreaker.Settings{
		Name:        target,
		MaxRequests: 1,
		Timeout:     s.OpenFor,
		ReadyToTrip: func(c gobreaker.Counts) bool { return c.ConsecutiveFailures >= s.ConsecutiveFailures },
		OnStateChange: func(_ string, _ gobreaker.State, to gobreaker.State) {
			metrics.BreakerState.WithLabelValues(target).Set(float64(to))
		},
	})
	return &http.Client{
		Timeout:   s.Timeout,
		Transport: &transport{target: target, next: base, breaker: breaker},
	}
}

type transport struct {
	target  string
	next    http.RoundTripper
	breaker *gobreaker.CircuitBreaker[*http.Response]
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if id := requestid.From(req.Context()); id != "" && req.Header.Get(requestid.Header) == "" {
		req = req.Clone(req.Context())
		req.Header.Set(requestid.Header, id)
	}
	resp, err := t.breaker.Execute(func() (*http.Response, error) {
		resp, err := t.next.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 500 {
			// Counted as a failure for the breaker, but still handed to the
			// caller, which reads the dependency's error message.
			return resp, errServerError
		}
		return resp, nil
	})
	switch {
	case errors.Is(err, gobreaker.ErrOpenState), errors.Is(err, gobreaker.ErrTooManyRequests):
		metrics.OutboundRequests.WithLabelValues(t.target, "rejected").Inc()
		return nil, ErrCircuitOpen
	case errors.Is(err, errServerError):
		metrics.OutboundRequests.WithLabelValues(t.target, "error").Inc()
		return resp, nil
	case err != nil:
		metrics.OutboundRequests.WithLabelValues(t.target, "error").Inc()
		return nil, err
	}
	metrics.OutboundRequests.WithLabelValues(t.target, "ok").Inc()
	return resp, nil
}

var errServerError = errors.New("server error")
