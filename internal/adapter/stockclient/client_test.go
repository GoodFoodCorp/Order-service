package stockclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"goodfood/order-service/internal/domain"
)

const key = "secret-key"

var lines = []domain.StockLine{{StockItemID: "s1", Quantity: 1.5}, {StockItemID: "s2", Quantity: 2}}

type captured struct {
	method, path, key, body string
}

func server(t *testing.T, status int, resp string, got *captured) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*got = captured{r.Method, r.URL.Path, r.Header.Get("X-Service-Key"), strings.TrimSpace(string(b))}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
}

func TestCheck(t *testing.T) {
	var got captured
	srv := server(t, 200, `{"available":false,"missing":[{"stockItemId":"s1","name":"Tomate","unit":"kg","required":1.5,"onHand":0.5}]}`, &got)
	defer srv.Close()

	av, err := New(srv.URL, key).Check(context.Background(), "r1", lines)
	if err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/internal/stocks/check" || got.key != key {
		t.Errorf("request = %+v", got)
	}
	want := `{"tenantId":"r1","lines":[{"stockItemId":"s1","quantity":1.5},{"stockItemId":"s2","quantity":2}]}`
	if got.body != want {
		t.Errorf("body = %s", got.body)
	}
	if av.Available || len(av.Missing) != 1 {
		t.Fatalf("availability = %+v", av)
	}
	m := av.Missing[0]
	if m.StockItemID != "s1" || m.Name != "Tomate" || m.Unit != "kg" || m.Required != 1.5 || m.OnHand != 0.5 {
		t.Errorf("missing = %+v", m)
	}
}

func TestCheckAvailable(t *testing.T) {
	var got captured
	srv := server(t, 200, `{"available":true,"missing":[]}`, &got)
	defer srv.Close()
	av, err := New(srv.URL, key).Check(context.Background(), "r1", lines)
	if err != nil || !av.Available || len(av.Missing) != 0 {
		t.Errorf("av=%+v err=%v", av, err)
	}
}

func TestConsume(t *testing.T) {
	var got captured
	srv := server(t, 200, `{"status":"APPLIED","shortfalls":[]}`, &got)
	defer srv.Close()
	if err := New(srv.URL, key).Consume(context.Background(), "o1", "r1", lines); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/internal/stocks/consume" || got.key != key {
		t.Errorf("request = %+v", got)
	}
	want := `{"orderId":"o1","tenantId":"r1","lines":[{"stockItemId":"s1","quantity":1.5},{"stockItemId":"s2","quantity":2}]}`
	if got.body != want {
		t.Errorf("body = %s", got.body)
	}
}

func TestRestore(t *testing.T) {
	var got captured
	srv := server(t, 200, `{"status":"RESTORED"}`, &got)
	defer srv.Close()
	if err := New(srv.URL, key).Restore(context.Background(), "o1", "r1"); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/internal/stocks/restore" || got.key != key {
		t.Errorf("request = %+v", got)
	}
	if want := `{"orderId":"o1","tenantId":"r1"}`; got.body != want {
		t.Errorf("body = %s", got.body)
	}
}

func TestErrorStatuses(t *testing.T) {
	c := func(url string) []func() error {
		cl := New(url, key)
		ctx := context.Background()
		return []func() error{
			func() error { _, err := cl.Check(ctx, "r1", lines); return err },
			func() error { return cl.Consume(ctx, "o1", "r1", lines) },
			func() error { return cl.Restore(ctx, "o1", "r1") },
		}
	}
	for _, tc := range []struct {
		status int
		body   string
		want   string
	}{
		{401, `{"error":"invalid service key"}`, "invalid service key"},
		{404, `{"error":"stock item not found"}`, "stock item not found"},
		{409, `{"error":"conflict"}`, "conflict"},
		{500, `oops`, "500"},
	} {
		var got captured
		srv := server(t, tc.status, tc.body, &got)
		for i, call := range c(srv.URL) {
			err := call()
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), string(rune('0'+tc.status/100))) {
				t.Errorf("status %d call %d: err = %v", tc.status, i, err)
			}
			if err != nil && strings.Contains(err.Error(), key) {
				t.Errorf("error leaks key: %v", err)
			}
		}
		srv.Close()
	}
}

func TestUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	cl := New(url, key)
	ctx := context.Background()
	if _, err := cl.Check(ctx, "r1", lines); err == nil {
		t.Error("check: expected error")
	}
	if err := cl.Consume(ctx, "o1", "r1", lines); err == nil {
		t.Error("consume: expected error")
	}
	err := cl.Restore(ctx, "o1", "r1")
	if err == nil || strings.Contains(err.Error(), key) {
		t.Errorf("restore err = %v", err)
	}
}
