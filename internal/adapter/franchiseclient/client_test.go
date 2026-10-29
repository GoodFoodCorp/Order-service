package franchiseclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"goodfood/order-service/internal/domain"
)

func TestQuoteCallsTheInternalRoute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/internal/restaurants/r1/quote" || r.Header.Get("X-Service-Key") != "key" || body["address"] != "12 rue X" {
			t.Errorf("unexpected call %s %v %v", r.URL.Path, r.Header, body)
		}
		_, _ = w.Write([]byte(`{"canOrder":false,"refusal":"OUTOFZONE","deliveryFeeCents":350,"minimumOrderCents":1000,
			"prepTimeMinutes":20,"estimatedDeliveryMinutes":40,"dropoffLatitude":48.8,"dropoffLongitude":2.3}`))
	}))
	defer srv.Close()

	q, err := New(srv.URL, "key", nil).Quote(context.Background(), "r1", "12 rue X")

	if err != nil {
		t.Fatal(err)
	}
	if q.CanOrder || q.Refusal != "OUTOFZONE" || q.DeliveryFeeCents != 350 || q.EstimatedMinutes != 40 || *q.DropoffLat != 48.8 {
		t.Fatalf("got %+v", q)
	}
}

func TestQuoteMapsUnknownRestaurant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer srv.Close()

	_, err := New(srv.URL, "key", nil).Quote(context.Background(), "r1", "")

	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeNotFound {
		t.Fatalf("got %v", err)
	}
}
