package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"goodfood/order-service/internal/domain"
)

const (
	exchangeName   = "goodfood.events"
	publishTimeout = 3 * time.Second
)

type envelope struct {
	EventID    string         `json:"eventId"`
	Type       string         `json:"type"`
	OccurredAt time.Time      `json:"occurredAt"`
	Data       orderReadyData `json:"data"`
}

type orderReadyData struct {
	OrderID         string `json:"orderId"`
	CustomerID      string `json:"customerId"`
	RestaurantID    string `json:"restaurantId"`
	DeliveryAddress string `json:"deliveryAddress"`
}

func buildOrderReadyMessage(e domain.OrderReadyEvent) ([]byte, error) {
	return json.Marshal(envelope{
		EventID:    e.EventID,
		Type:       domain.EventOrderReadyForPickup,
		OccurredAt: e.OccurredAt.UTC(),
		Data: orderReadyData{
			OrderID:         e.OrderID,
			CustomerID:      e.CustomerID,
			RestaurantID:    e.RestaurantID,
			DeliveryAddress: e.DeliveryAddress,
		},
	})
}

// AMQPPublisher publishes domain events to RabbitMQ. The connection is opened
// lazily and re-opened after a failure, so a broker restart never requires
// restarting order-service.
type AMQPPublisher struct {
	url  string
	mu   sync.Mutex
	conn *amqp.Connection
	ch   *amqp.Channel
}

func NewAMQPPublisher(url string) *AMQPPublisher {
	return &AMQPPublisher{url: url}
}

func (p *AMQPPublisher) PublishOrderReady(ctx context.Context, e domain.OrderReadyEvent) error {
	body, err := buildOrderReadyMessage(e)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishTimeout)
	defer cancel()

	p.mu.Lock()
	defer p.mu.Unlock()

	err = p.publish(ctx, domain.EventOrderReadyForPickup, e.EventID, body)
	if err != nil {
		p.reset() // stale connection: retry once on a fresh one
		err = p.publish(ctx, domain.EventOrderReadyForPickup, e.EventID, body)
	}
	return err
}

func (p *AMQPPublisher) publish(ctx context.Context, key, messageID string, body []byte) error {
	if err := p.ensureChannel(); err != nil {
		return err
	}
	confirmation, err := p.ch.PublishWithDeferredConfirmWithContext(ctx, exchangeName, key, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		MessageId:    messageID,
		Body:         body,
	})
	if err != nil {
		return err
	}
	acked, err := confirmation.WaitContext(ctx)
	if err != nil {
		return err
	}
	if !acked {
		return errors.New("broker nacked the message")
	}
	return nil
}

func (p *AMQPPublisher) ensureChannel() error {
	if p.ch != nil && !p.ch.IsClosed() {
		return nil
	}
	p.reset()
	conn, err := amqp.DialConfig(p.url, amqp.Config{Dial: amqp.DefaultDial(publishTimeout)})
	if err != nil {
		return err
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return err
	}
	if err := ch.ExchangeDeclare(exchangeName, "topic", true, false, false, false, nil); err != nil {
		_ = conn.Close()
		return err
	}
	if err := ch.Confirm(false); err != nil {
		_ = conn.Close()
		return err
	}
	p.conn, p.ch = conn, ch
	return nil
}

func (p *AMQPPublisher) reset() {
	if p.conn != nil {
		_ = p.conn.Close()
	}
	p.conn, p.ch = nil, nil
}

func (p *AMQPPublisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reset()
}
