package outboxpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Gui-Rondon/hyrule/outbox"
)

const (
	defaultInterval       = 5 * time.Second
	defaultPublishTimeout = 10 * time.Second
	defaultMaxBackoff     = 5 * time.Minute
	defaultStaleAfter     = time.Minute
	// defaultMaxAttempts: 1s/2s/4s/8s/16s/32s/64s/128s/256s backoff
	// (capped at defaultMaxBackoff from attempt 9 on) before an event
	// goes to the DLQ -- about 8.5 minutes of retrying, generous enough
	// for a transient destination outage without retrying forever.
	defaultMaxAttempts = 10
	defaultWorkers     = 1
)

// ErrNotReplayable is returned by Replay when id does not name a dead
// event (already replayed, never existed, or not dead in the first
// place).
var ErrNotReplayable = errors.New("outboxpostgres: id is not a dead event")

// Relay publishes the pending events of an Outbox, oldest first within
// each worker's own pass. Unlike outbox.Relay (Mongo), it takes no
// lease: SELECT ... FOR UPDATE SKIP LOCKED already keeps two
// concurrent callers -- even in different processes -- from claiming
// the same row, so several workers (Workers, or several services'
// Relay.Run calls) drain concurrently with no coordination between
// them. The accepted cost (decision C-1): ordering by Key is only
// kept within one worker's own pass, not across workers.
type Relay struct {
	Outbox    *Outbox
	Publisher outbox.Publisher
	// Interval between scans when there is nothing pending (default 5s).
	Interval time.Duration
	// PublishTimeout bounds each publish (default 10s) -- the claiming
	// transaction (and its row lock) stays open for at most this long.
	PublishTimeout time.Duration
	// MaxBackoff caps the wait between attempts of a failing event: 1s,
	// 2s, 4s... (default 5 minutes).
	MaxBackoff time.Duration
	// StaleAfter logs a warning when the oldest pending event is older
	// (default 1 minute).
	StaleAfter time.Duration
	// MaxAttempts sends an event to the DLQ (dead_at) once attempts
	// reaches it (default 10) -- Mongo's outbox has no DLQ at all
	// (events just retry forever); this is new, Postgres-only (C-2).
	MaxAttempts int
	// Workers is how many goroutines Run drains with concurrently
	// (default 1) -- the throughput SKIP LOCKED buys, self-contained
	// in one Relay.Run call instead of the caller registering several
	// background tasks.
	Workers int

	once sync.Once
}

func (r *Relay) setDefaults() { r.once.Do(r.defaults) }

func (r *Relay) defaults() {
	if r.Interval <= 0 {
		r.Interval = defaultInterval
	}
	if r.PublishTimeout <= 0 {
		r.PublishTimeout = defaultPublishTimeout
	}
	if r.MaxBackoff <= 0 {
		r.MaxBackoff = defaultMaxBackoff
	}
	if r.StaleAfter <= 0 {
		r.StaleAfter = defaultStaleAfter
	}
	if r.MaxAttempts <= 0 {
		r.MaxAttempts = defaultMaxAttempts
	}
	if r.Workers <= 0 {
		r.Workers = defaultWorkers
	}
}

// Run drains with Workers goroutines until ctx is done.
func (r *Relay) Run(ctx context.Context) error {
	r.setDefaults()
	var wg sync.WaitGroup
	for range r.Workers {
		wg.Go(func() { r.runWorker(ctx) })
	}
	wg.Wait()
	return nil
}

func (r *Relay) runWorker(ctx context.Context) {
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		if _, err := r.Drain(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("outboxpostgres: relay pass failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Drain claims and publishes every eligible event once, oldest first,
// until none remain.
func (r *Relay) Drain(ctx context.Context) (published int, err error) {
	r.setDefaults()
	for {
		found, ok, err := r.claimOne(ctx)
		if err != nil {
			return published, err
		}
		if !found {
			r.warnIfStale(ctx)
			return published, nil
		}
		if ok {
			published++
		}
	}
}

// claimOne claims one eligible row (SELECT ... FOR UPDATE SKIP LOCKED),
// publishes it, and records the outcome (published_at, or attempts/
// last_error/next_attempt_at, or dead_at past MaxAttempts) -- all in
// the one transaction that holds the row's lock, so no other worker
// can see it until this call commits. found reports whether a row was
// claimed at all; published reports whether its publish succeeded.
//
// The NOT EXISTS clause is what keeps a key's events in order (within
// this call, across calls of the same worker, and -- modulo the
// concurrent-claim timing C-1 already accepts as best effort -- mostly
// across workers too): a keyed row is only eligible once every earlier
// (lower id) row of the same key is actually published or dead, so a
// backed-off or still-locked earlier event keeps blocking the later
// ones without needing an in-memory "blocked" set the way the Mongo
// relay's single-pass scan does.
func (r *Relay) claimOne(ctx context.Context) (found, published bool, err error) {
	tx, err := r.Outbox.Pool.Begin(ctx)
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	table := pgx.Identifier{r.Outbox.table()}.Sanitize()
	q := fmt.Sprintf(`SELECT o.id, o.topic, o.type, o.key, o.data, o.attributes::text, o.occurred_at, o.attempts
		FROM %[1]s o
		WHERE o.published_at IS NULL AND o.dead_at IS NULL
		  AND (o.next_attempt_at IS NULL OR o.next_attempt_at <= now())
		  AND (o.key = '' OR NOT EXISTS (
		        SELECT 1 FROM %[1]s o2
		        WHERE o2.key = o.key AND o2.id < o.id AND o2.published_at IS NULL AND o2.dead_at IS NULL
		      ))
		ORDER BY o.id
		FOR UPDATE OF o SKIP LOCKED
		LIMIT 1`, table)
	var id int64
	var topic, typ, key string
	var data []byte
	var attrsText *string
	var occurredAt time.Time
	var attempts int
	err = tx.QueryRow(ctx, q).Scan(&id, &topic, &typ, &key, &data, &attrsText, &occurredAt, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}

	attrs := map[string]string{}
	if attrsText != nil {
		json.Unmarshal([]byte(*attrsText), &attrs)
	}
	idStr := strconv.FormatInt(id, 10)
	attrs[outbox.AttrEventID] = idStr
	attrs[outbox.AttrOccurredAt] = occurredAt.Format(time.RFC3339Nano)
	if typ != "" {
		attrs[outbox.AttrEventType] = typ
	}
	msg := outbox.Message{ID: idStr, Topic: topic, Data: data, Attributes: attrs, OrderingKey: key}

	pctx, cancel := context.WithTimeout(ctx, r.PublishTimeout)
	pubErr := r.Publisher.Publish(pctx, msg)
	cancel()

	if pubErr != nil {
		attempts++
		wait := backoff(attempts, r.MaxBackoff)
		slog.Warn("outboxpostgres: publish failed", "topic", topic, "event_id", idStr,
			"key", key, "attempt", attempts, "retry_in", wait.String(), "error", pubErr)
		var uerr error
		if attempts >= r.MaxAttempts {
			_, uerr = tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET attempts=$1, last_error=$2, dead_at=now() WHERE id=$3`, table),
				attempts, pubErr.Error(), id)
		} else {
			_, uerr = tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET attempts=$1, last_error=$2, next_attempt_at=$3 WHERE id=$4`, table),
				attempts, pubErr.Error(), now().Add(wait), id)
		}
		if uerr != nil {
			return false, false, uerr
		}
		if err := tx.Commit(ctx); err != nil {
			return false, false, err
		}
		return true, false, nil
	}

	// a failure here republishes the event later: consumers deduplicate
	if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET published_at=$1 WHERE id=$2`, table), now(), id); err != nil {
		return false, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, false, err
	}
	return true, true, nil
}

// backoff is 1s, 2s, 4s... up to max.
func backoff(attempt int, max time.Duration) time.Duration {
	wait := time.Second
	for i := 1; i < attempt && wait < max; i++ {
		wait *= 2
	}
	return min(wait, max)
}

// Replay clears dead_at (and resets attempts/next_attempt_at) on a
// dead event, so Drain's pending scan picks it up again -- an UPDATE,
// never a direct reprocessing of id itself.
func (r *Relay) Replay(ctx context.Context, id int64) error {
	table := pgx.Identifier{r.Outbox.table()}.Sanitize()
	tag, err := r.Outbox.Pool.Exec(ctx, fmt.Sprintf(`UPDATE %s SET dead_at=NULL, attempts=0, last_error='', next_attempt_at=NULL
		WHERE id=$1 AND dead_at IS NOT NULL`, table), id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotReplayable
	}
	return nil
}

func (r *Relay) warnIfStale(ctx context.Context) {
	s, err := r.Stats(ctx)
	if err == nil && s.Pending > 0 && s.Oldest > r.StaleAfter {
		slog.Warn("outboxpostgres: pending events delayed", "table", r.Outbox.table(), "oldest", s.Oldest.Round(time.Second).String())
	}
}
