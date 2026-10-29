// Package outbox relays the events stored by the transactional outbox to the
// message broker.
package outbox

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

// Message is one stored event, ready to publish.
type Message struct {
	ID         string
	RoutingKey string
	Body       []byte
}

// Store hands batches of pending messages to publish and records the outcome.
type Store interface {
	Relay(ctx context.Context, limit int, publish func(context.Context, Message) error) (int, error)
}

// Publisher sends one message to the broker and waits for its confirmation.
type Publisher interface {
	Publish(ctx context.Context, m Message) error
}

// Relay periodically drains the outbox.
type Relay struct {
	store     Store
	publisher Publisher
	interval  time.Duration
	batch     int
	log       zerolog.Logger
}

func NewRelay(store Store, publisher Publisher, log zerolog.Logger) *Relay {
	return &Relay{store: store, publisher: publisher, interval: 2 * time.Second, batch: 100, log: log}
}

// Run drains the outbox until ctx ends. A full batch is followed immediately
// by the next one, so a backlog empties quickly.
func (r *Relay) Run(ctx context.Context) {
	timer := time.NewTimer(r.interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		n, err := r.store.Relay(ctx, r.batch, r.publisher.Publish)
		if err != nil && ctx.Err() == nil {
			r.log.Warn().Err(err).Msg("outbox relay pass failed")
		}
		if n > 0 {
			r.log.Debug().Int("published", n).Msg("outbox events relayed")
		}
		next := r.interval
		if n == r.batch {
			next = 0
		}
		timer.Reset(next)
	}
}
