package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Locker runs a job on one replica at a time, with a PostgreSQL session
// advisory lock. A replica that does not get the lock skips the pass.
type Locker struct {
	pool *pgxpool.Pool
}

func NewLocker(pool *pgxpool.Pool) *Locker { return &Locker{pool: pool} }

// RunExclusive runs job if the lock identified by key is free; ran reports
// whether it did.
func (l *Locker) RunExclusive(ctx context.Context, key int64, job func(context.Context)) (ran bool, err error) {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&acquired); err != nil || !acquired {
		return false, err
	}
	defer func() {
		// Unlock on a fresh context: ctx may be cancelled (shutdown).
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, key)
	}()
	job(ctx)
	return true, nil
}
