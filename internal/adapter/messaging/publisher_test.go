package messaging

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goodfood/order-service/internal/domain"
)

func TestBuildOrderReadyMessage(t *testing.T) {
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	body, err := buildOrderReadyMessage(domain.OrderReadyEvent{
		EventID: "evt-1", OrderID: "o-1", CustomerID: "c-1",
		RestaurantID: "r-1", DeliveryAddress: "12 rue de Paris", OccurredAt: at,
	})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, "evt-1", got["eventId"])
	assert.Equal(t, "order.ready_for_pickup", got["type"])
	assert.Equal(t, "2026-10-10T12:00:00Z", got["occurredAt"])
	data := got["data"].(map[string]any)
	assert.Equal(t, "o-1", data["orderId"])
	assert.Equal(t, "c-1", data["customerId"])
	assert.Equal(t, "r-1", data["restaurantId"])
	assert.Equal(t, "12 rue de Paris", data["deliveryAddress"])
}

func TestPublishFailsFastWhenBrokerUnreachable(t *testing.T) {
	pub := NewAMQPPublisher("amqp://guest:guest@127.0.0.1:1/")
	defer pub.Close()

	err := pub.PublishOrderReady(t.Context(), domain.OrderReadyEvent{OrderID: "o-1"})

	assert.Error(t, err)
}
