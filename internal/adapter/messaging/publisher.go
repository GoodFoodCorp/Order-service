// Package messaging publishes events to RabbitMQ.
package messaging

import (
	"context"
	"errors"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"goodfood/order-service/internal/adapter/outbox"
)

const (
	// ExchangeName is the topic exchange shared by every GoodFood service.
	ExchangeName   = "goodfood.events"
	publishTimeout = 5 * time.Second
)

// AMQPPublisher publishes persistent messages with publisher confirms. The
// connection is opened lazily and re-opened after a failure, so a broker
// restart never requires restarting the service.
type AMQPPublisher struct {
	url  string
	mu   sync.Mutex
	conn *amqp.Connection
	ch   *amqp.Channel
}

func NewAMQPPublisher(url string) *AMQPPublisher { return &AMQPPublisher{url: url} }

// Publish implements outbox.Publisher.
func (p *AMQPPublisher) Publish(ctx context.Context, m outbox.Message) error {
	ctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()

	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.publish(ctx, m); err != nil {
		p.reset() // stale connection: retry once on a fresh one
		return p.publish(ctx, m)
	}
	return nil
}

func (p *AMQPPublisher) publish(ctx context.Context, m outbox.Message) error {
	if err := p.ensureChannel(); err != nil {
		return err
	}
	confirmation, err := p.ch.PublishWithDeferredConfirmWithContext(ctx, ExchangeName, m.RoutingKey, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		MessageId:    m.ID,
		Body:         m.Body,
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
	if err := ch.ExchangeDeclare(ExchangeName, "topic", true, false, false, false, nil); err != nil {
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

// Close releases the connection.
func (p *AMQPPublisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reset()
}
