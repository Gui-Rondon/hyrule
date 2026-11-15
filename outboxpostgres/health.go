package outboxpostgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Gui-Rondon/hyrule/outbox"
)

// Stats counts the pending events and the age of the oldest -- same
// shape as outbox.Stats (Mongo), so Relay satisfies
// outbox.StatsProvider and outbox.Check/CheckFromConfig work
// unchanged. Dead events (dead_at set) are not pending: they need
// Replay, not a longer wait, so they are excluded here the same way
// the pending scan itself excludes them.
func (r *Relay) Stats(ctx context.Context) (outbox.Stats, error) {
	table := pgx.Identifier{r.Outbox.table()}.Sanitize()
	var s outbox.Stats
	q := fmt.Sprintf(`SELECT count(*), count(*) FILTER (WHERE attempts > 0) FROM %s WHERE published_at IS NULL AND dead_at IS NULL`, table)
	if err := r.Outbox.Pool.QueryRow(ctx, q).Scan(&s.Pending, &s.Retrying); err != nil {
		return s, err
	}
	if s.Pending == 0 {
		return s, nil
	}
	oldest, err := r.oldestPending(ctx, table)
	if err != nil {
		return s, err
	}
	s.Oldest = oldest
	return s, nil
}

func (r *Relay) oldestPending(ctx context.Context, table string) (time.Duration, error) {
	var occurredAt time.Time
	q := fmt.Sprintf(`SELECT occurred_at FROM %s WHERE published_at IS NULL AND dead_at IS NULL ORDER BY id LIMIT 1`, table)
	if err := r.Outbox.Pool.QueryRow(ctx, q).Scan(&occurredAt); err != nil {
		return 0, err
	}
	return time.Since(occurredAt), nil
}
