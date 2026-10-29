package paymentclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"goodfood/order-service/internal/domain"
)

func TestCreateIntentUsesInternalRouteAndServiceKey(t *testing.T) {
	var gotKey, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotPath = r.Header.Get("X-Service-Key"), r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"intent_id":"pi_1","client_secret":"cs","status":"PENDING","amount_cents":2598,"currency":"eur"}`))
	}))
	defer srv.Close()

	intent, err := New(srv.URL, "key", nil).CreateIntent(context.Background(), domain.PaymentIntentRequest{
		OrderID: "o1", CustomerID: "c1", AmountCents: 2598, Currency: "eur",
	})

	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "key" || gotPath != "/internal/payments/intents" {
		t.Fatalf("key=%q path=%q", gotKey, gotPath)
	}
	if gotBody["amount_cents"].(float64) != 2598 || gotBody["customer_id"] != "c1" {
		t.Fatalf("body %v", gotBody)
	}
	if intent.ClientSecret != "cs" || intent.AmountCents != 2598 {
		t.Fatalf("intent %+v", intent)
	}
}

func TestGetPaymentMapsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "key", nil).GetPayment(context.Background(), "o1")

	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeNotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}

func TestGetPaymentDecodesTheRecord(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/payments/o1" {
			t.Errorf("path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"order_id":"o1","customer_id":"c1","status":"SUCCEEDED","amount_cents":100,"currency":"eur"}`))
	}))
	defer srv.Close()

	p, err := New(srv.URL, "key", nil).GetPayment(context.Background(), "o1")

	if err != nil || p.Status != domain.PaymentStatusSucceeded || p.AmountCents != 100 || p.CustomerID != "c1" {
		t.Fatalf("got %+v %v", p, err)
	}
}

func TestRefundSurfacesServiceErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"payment provider error"}`))
	}))
	defer srv.Close()

	err := New(srv.URL, "key", nil).Refund(context.Background(), "o1")

	if err == nil || err.Error() != "payment provider error" {
		t.Fatalf("got %v", err)
	}
}
