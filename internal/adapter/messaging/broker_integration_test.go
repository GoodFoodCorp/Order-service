//go:build integration

package messaging

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"goodfood/order-service/internal/adapter/outbox"
)

// Run with: TEST_AMQP_URL=amqp://... go test -tags integration ./internal/adapter/messaging/
func TestPaymentSucceededReachesTheHandlerWithRetry(t *testing.T) {
	url := os.Getenv("TEST_AMQP_URL")
	if url == "" {
		t.Skip("TEST_AMQP_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var mu sync.Mutex
	calls := map[string]int{}
	handled := make(chan string, 10)
	handler := func(_ context.Context, orderID string) error {
		mu.Lock()
		defer mu.Unlock()
		calls[orderID]++
		if calls[orderID] == 1 {
			return errors.New("payment-service briefly down") // first attempt fails: must be redelivered
		}
		handled <- orderID
		return nil
	}
	consumer := NewPaymentConsumer(url, handler, zerolog.Nop())
	consumer.backoff = 50 * time.Millisecond
	go consumer.Run(ctx)
	time.Sleep(time.Second) // queue declared and bound

	pub := NewAMQPPublisher(url)
	defer pub.Close()
	body := []byte(`{"eventId":"e1","type":"payment.succeeded","data":{"orderId":"order-it-1"}}`)
	if err := pub.Publish(ctx, outbox.Message{ID: "e1", RoutingKey: EventPaymentSucceeded, Body: body}); err != nil {
		t.Fatal(err)
	}

	select {
	case id := <-handled:
		if id != "order-it-1" {
			t.Fatalf("handled %q", id)
		}
	case <-ctx.Done():
		t.Fatal("payment.succeeded never handled")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["order-it-1"] != 2 {
		t.Fatalf("attempts = %d, want 2 (one failure, one success)", calls["order-it-1"])
	}
}
