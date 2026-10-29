package messaging

import "testing"

func TestParsePaymentSucceeded(t *testing.T) {
	id, err := parsePaymentSucceeded([]byte(`{"eventId":"e1","type":"payment.succeeded","data":{"orderId":"o-1","amountCents":100}}`))
	if err != nil || id != "o-1" {
		t.Fatalf("got %q %v", id, err)
	}

	id, err = parsePaymentSucceeded([]byte(`{"type":"payment.refunded","data":{"orderId":"o-1"}}`))
	if err != nil || id != "" {
		t.Fatalf("other payment events are ignored, got %q %v", id, err)
	}

	for _, body := range []string{`not json`, `{"type":"payment.succeeded","data":{}}`} {
		if _, err := parsePaymentSucceeded([]byte(body)); err == nil {
			t.Fatalf("%s must be rejected", body)
		}
	}
}
