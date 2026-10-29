package httpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"goodfood/order-service/internal/platform/requestid"
)

func get(t *testing.T, c *http.Client, url string, ctx context.Context) (*http.Response, error) {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := c.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	return resp, err
}

func TestPropagatesTheRequestID(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header.Get(requestid.Header) }))
	defer srv.Close()

	_, err := get(t, New("svc", Settings{}), srv.URL, requestid.With(context.Background(), "req-42"))

	if err != nil || got != "req-42" {
		t.Fatalf("header %q, err %v", got, err)
	}
}

func TestBreakerOpensAfterConsecutiveFailuresThenRecovers(t *testing.T) {
	var calls, failing atomic.Int32
	failing.Store(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if failing.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	c := New("flaky", Settings{ConsecutiveFailures: 3, OpenFor: 100 * time.Millisecond})

	for i := 0; i < 3; i++ {
		resp, err := get(t, c, srv.URL, context.Background())
		if err != nil || resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("call %d: the 5xx itself must reach the caller (%v)", i, err)
		}
	}
	if _, err := get(t, c, srv.URL, context.Background()); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected the breaker to be open, got %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("an open breaker must not call the target (calls=%d)", calls.Load())
	}

	failing.Store(0)
	time.Sleep(150 * time.Millisecond)
	if resp, err := get(t, c, srv.URL, context.Background()); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("trial call after the open period must go through: %v", err)
	}
}
