package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"
)

const (
	// PaymentEventsQueue receives the payment events order-service reacts to.
	PaymentEventsQueue = "order-service.payment-events"
	paymentEventsDLQ   = PaymentEventsQueue + ".dlq"

	// EventPaymentSucceeded is published by payment-service.
	EventPaymentSucceeded = "payment.succeeded"

	reconnectDelay = 5 * time.Second
	// retryDelay spaces the redeliveries of a message whose handling failed
	// for a transient reason (a dependency down).
	retryDelay = 2 * time.Second
	prefetch   = 10
)

// PaymentSucceededHandler confirms the order of a payment. It returns an error
// only for transient failures, which get the message redelivered.
type PaymentSucceededHandler func(ctx context.Context, orderID string) error

type paymentEvent struct {
	Type string `json:"type"`
	Data struct {
		OrderID string `json:"orderId"`
	} `json:"data"`
}

// PaymentConsumer consumes payment.succeeded. It starts in the background so
// the service boots without a broker, reconnects on loss, and dead-letters
// messages that can never be processed (malformed).
type PaymentConsumer struct {
	url     string
	handle  PaymentSucceededHandler
	log     zerolog.Logger
	backoff time.Duration
}

func NewPaymentConsumer(url string, handle PaymentSucceededHandler, log zerolog.Logger) *PaymentConsumer {
	return &PaymentConsumer{url: url, handle: handle, log: log, backoff: retryDelay}
}

// Run consumes until ctx ends, reconnecting after any broker failure.
func (c *PaymentConsumer) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := c.consume(ctx); err != nil && ctx.Err() == nil {
			c.log.Warn().Err(err).Msg("payment events consumer stopped, reconnecting")
		}
		select {
		case <-ctx.Done():
		case <-time.After(reconnectDelay):
		}
	}
}

func (c *PaymentConsumer) consume(ctx context.Context) error {
	conn, err := amqp.DialConfig(c.url, amqp.Config{Dial: amqp.DefaultDial(publishTimeout)})
	if err != nil {
		return err
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	if err := declarePaymentQueue(ch); err != nil {
		return err
	}
	if err := ch.Qos(prefetch, 0, false); err != nil {
		return err
	}
	deliveries, err := ch.ConsumeWithContext(ctx, PaymentEventsQueue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	c.log.Info().Str("queue", PaymentEventsQueue).Msg("consuming payment events")

	closed := conn.NotifyClose(make(chan *amqp.Error, 1))
	for {
		select {
		case <-ctx.Done():
			return nil
		case amqpErr := <-closed:
			return amqpErr
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("delivery channel closed")
			}
			c.dispatch(ctx, d)
		}
	}
}

// dispatch acks a processed message, dead-letters a malformed one, and
// redelivers (after a pause) one whose handling failed transiently.
func (c *PaymentConsumer) dispatch(ctx context.Context, d amqp.Delivery) {
	orderID, err := parsePaymentSucceeded(d.Body)
	if err != nil {
		c.log.Error().Err(err).Str("message_id", d.MessageId).Msg("malformed payment event sent to the DLQ")
		_ = d.Nack(false, false)
		return
	}
	if orderID == "" {
		_ = d.Ack(false) // another payment event type: nothing to do
		return
	}
	if err := c.handle(ctx, orderID); err != nil {
		c.log.Warn().Err(err).Str("order_id", orderID).Msg("payment.succeeded not handled yet, will retry")
		select {
		case <-ctx.Done():
		case <-time.After(c.backoff):
		}
		_ = d.Nack(false, true)
		return
	}
	_ = d.Ack(false)
}

// parsePaymentSucceeded returns the order id of a payment.succeeded message,
// "" for any other payment event, or an error when the message is malformed.
func parsePaymentSucceeded(body []byte) (string, error) {
	var ev paymentEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return "", err
	}
	if ev.Type != EventPaymentSucceeded {
		return "", nil
	}
	if ev.Data.OrderID == "" {
		return "", errors.New("payment.succeeded without orderId")
	}
	return ev.Data.OrderID, nil
}

func declarePaymentQueue(ch *amqp.Channel) error {
	if err := ch.ExchangeDeclare(ExchangeName, "topic", true, false, false, false, nil); err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(paymentEventsDLQ, true, false, false, false, nil); err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(PaymentEventsQueue, true, false, false, false, amqp.Table{
		"x-dead-letter-exchange":    "",
		"x-dead-letter-routing-key": paymentEventsDLQ,
	}); err != nil {
		return err
	}
	return ch.QueueBind(PaymentEventsQueue, EventPaymentSucceeded, ExchangeName, false, nil)
}
