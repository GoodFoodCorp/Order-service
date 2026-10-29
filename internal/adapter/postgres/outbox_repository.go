package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"goodfood/order-service/internal/adapter/outbox"
	"goodfood/order-service/internal/domain"
)

// envelope is the wire format shared by every GoodFood event.
type envelope struct {
	EventID    string    `json:"eventId"`
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurredAt"`
	Data       any       `json:"data"`
}

// insertEvent writes an event to the outbox inside the caller's transaction.
func insertEvent(ctx context.Context, tx pgx.Tx, e *domain.Event) error {
	payload, err := json.Marshal(envelope{EventID: e.ID, Type: e.Type, OccurredAt: e.OccurredAt.UTC(), Data: e.Data})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO outbox_events (id, routing_key, payload, occurred_at) VALUES ($1, $2, $3, $4)`,
		e.ID, e.Type, payload, e.OccurredAt)
	return err
}

// OutboxRepository is the outbox.Store of this service.
type OutboxRepository struct {
	pool *pgxpool.Pool
}

func NewOutboxRepository(pool *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{pool: pool}
}

// Relay publishes up to limit pending events, oldest first. The rows are
// locked with SKIP LOCKED for the duration of the batch, so several replicas
// can relay concurrently without sending the same event twice at once; an
// event is marked published only once the broker confirmed it.
func (r *OutboxRepository) Relay(ctx context.Context, limit int, publish func(context.Context, outbox.Message) error) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx,
		`SELECT id, routing_key, payload FROM outbox_events
		 WHERE published_at IS NULL ORDER BY occurred_at LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return 0, err
	}
	batch, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (outbox.Message, error) {
		var m outbox.Message
		err := row.Scan(&m.ID, &m.RoutingKey, &m.Body)
		return m, err
	})
	if err != nil {
		return 0, err
	}

	published := 0
	for _, m := range batch {
		if err := publish(ctx, m); err != nil {
			// Stop at the first failure to keep the order; retried next pass.
			if _, uerr := tx.Exec(ctx, `UPDATE outbox_events SET attempts = attempts + 1 WHERE id = $1`, m.ID); uerr != nil {
				return published, uerr
			}
			break
		}
		if _, err := tx.Exec(ctx, `UPDATE outbox_events SET published_at = now() WHERE id = $1`, m.ID); err != nil {
			return published, err
		}
		published++
	}
	return published, tx.Commit(ctx)
}
