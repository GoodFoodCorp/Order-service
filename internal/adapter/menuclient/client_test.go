package menuclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const key = "secret-key"

type lookupBody struct {
	RestaurantID string   `json:"restaurant_id"`
	MenuItemIDs  []string `json:"menu_item_ids"`
}

func TestLookupSendsContractAndDecodes(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/recipes/lookup" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Service-Key") != key {
			t.Errorf("X-Service-Key = %q", r.Header.Get("X-Service-Key"))
		}
		b, _ := io.ReadAll(r.Body)
		raw = strings.TrimSpace(string(b))
		_, _ = w.Write([]byte(`{"recipes":{"m1":[{"stock_item_id":"s1","quantity":1.5},{"stock_item_id":"s2","quantity":2}]}}`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, key, nil).Lookup(context.Background(), "r1", []string{"m1", "m2"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"restaurant_id":"r1","menu_item_ids":["m1","m2"]}`; raw != want {
		t.Errorf("body = %s, want %s", raw, want)
	}
	if len(got) != 1 || len(got["m1"]) != 2 || got["m1"][0].StockItemID != "s1" || got["m1"][0].Quantity != 1.5 || got["m1"][1].Quantity != 2 {
		t.Errorf("recipes = %+v", got)
	}
}

func TestLookupDeduplicatesAndSendsNonNullEmpty(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, strings.TrimSpace(string(b)))
		_, _ = w.Write([]byte(`{"recipes":{}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, key, nil)

	if _, err := c.Lookup(context.Background(), "r1", []string{"a", "b", "a", "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Lookup(context.Background(), "r1", nil); err != nil {
		t.Fatal(err)
	}
	if bodies[0] != `{"restaurant_id":"r1","menu_item_ids":["a","b"]}` {
		t.Errorf("dedup body = %s", bodies[0])
	}
	if bodies[1] != `{"restaurant_id":"r1","menu_item_ids":[]}` {
		t.Errorf("empty body = %s", bodies[1])
	}
}

func TestLookupChunksAbove200AndMerges(t *testing.T) {
	var mu sync.Mutex
	var sizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b lookupBody
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
		}
		mu.Lock()
		sizes = append(sizes, len(b.MenuItemIDs))
		mu.Unlock()
		recipes := map[string][]map[string]any{}
		for _, id := range b.MenuItemIDs {
			recipes[id] = []map[string]any{{"stock_item_id": "s-" + id, "quantity": 1}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"recipes": recipes})
	}))
	defer srv.Close()

	ids := make([]string, 450)
	for i := range ids {
		ids[i] = fmt.Sprintf("m%d", i)
	}
	got, err := New(srv.URL, key, nil).Lookup(context.Background(), "r1", ids)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sizes, []int{200, 200, 50}) {
		t.Errorf("chunk sizes = %v", sizes)
	}
	if len(got) != 450 || got["m449"][0].StockItemID != "s-m449" {
		t.Errorf("merged len = %d", len(got))
	}
}

func TestLookupErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   string
	}{
		{401, `{"error":"invalid service key"}`, "invalid service key"},
		{500, `{"error":"internal server error"}`, "500"},
		{502, `bad gateway`, "502"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		_, err := New(srv.URL, key, nil).Lookup(context.Background(), "r1", []string{"m1"})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), fmt.Sprint(tc.status)) {
			t.Errorf("status %d: err = %v", tc.status, err)
		}
		if err != nil && strings.Contains(err.Error(), key) {
			t.Errorf("error leaks key: %v", err)
		}
	}
}

func TestLookupUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, err := New(url, key, nil).Lookup(context.Background(), "r1", []string{"m1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), key) {
		t.Errorf("error leaks key: %v", err)
	}
}
