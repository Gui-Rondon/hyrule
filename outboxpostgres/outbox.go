// Package outboxpostgres is the Postgres counterpart to outbox's
// Mongo-backed Outbox/Relay -- the same pattern platform/postgres and
// restpostgres already follow relative to platform/mongodb and rest:
// a separate package per backend, never a type switch inside the
// original one. outbox.Event/Message/Publisher/Dispatcher/Condition
// stay exactly as they are, imported from here -- only the storage
// (Outbox) and the relay (Relay, outboxpostgres/relay.go) are backend-
// specific.
package outboxpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/outbox"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
)

// Table is the default outbox table name.
const Table = "outbox"

// EnsureTable creates table (Table when empty) if it doesn't exist yet,
// with the dead_at column C-2 added on top of the Mongo record's own
// shape (topic/type/key/data/attributes/occurred_at/published_at/
// attempts/last_error/next_attempt_at), plus the partial index Relay's
// pending scan relies on. Never called by Outbox.Add or Relay on their
// own, same precedent as platform/postgres.EnsureFunctions/
// EnsureIndexes: a schema-setup step a service runs explicitly at
// boot, alongside its own resource table's DDL.
func EnsureTable(ctx context.Context, pool *pgxpool.Pool, table string) error {
	if table == "" {
		table = Table
	}
	ident := pgx.Identifier{table}.Sanitize()
	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id              bigint generated always as identity primary key,
		topic           text        not null,
		type            text        not null default '',
		key             text        not null default '',
		data            bytea       not null,
		attributes      jsonb,
		occurred_at     timestamptz not null,
		published_at    timestamptz,
		attempts        int         not null default 0,
		last_error      text        not null default '',
		next_attempt_at timestamptz,
		dead_at         timestamptz
	)`, ident)
	if _, err := pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("outboxpostgres: EnsureTable: %w", err)
	}
	idxName := pgx.Identifier{table + "_pending_idx"}.Sanitize()
	idx := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (id) WHERE published_at IS NULL AND dead_at IS NULL`, idxName, ident)
	if _, err := pool.Exec(ctx, idx); err != nil {
		return fmt.Errorf("outboxpostgres: EnsureTable: %w", err)
	}
	return nil
}

// Outbox stores events in a Postgres table, inside the write
// transaction of ctx -- the Postgres counterpart of outbox.Outbox,
// same Event/Add contract (outbox.ErrNoTransaction is the exact same
// sentinel error, not a new one, so a caller's errors.Is check works
// unchanged regardless of backend).
type Outbox struct {
	Pool *pgxpool.Pool
	// Table defaults to Table.
	Table string
	// AllowNoTransaction lets Add run without a transaction, mirroring
	// outbox.Outbox's own field -- never in production.
	AllowNoTransaction bool
}

func (o *Outbox) table() string {
	if o.Table != "" {
		return o.Table
	}
	return Table
}

var now = func() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

// Add stores events in the transaction of ctx.
func (o *Outbox) Add(ctx context.Context, events ...outbox.Event) error {
	if len(events) == 0 {
		return nil
	}
	if !o.AllowNoTransaction && !postgres.InTransaction(ctx, o.Pool) {
		return outbox.ErrNoTransaction
	}
	// an explicit ::jsonb cast on the attributes parameter, not
	// postgres.Store.InsertMany's generic (cast-less) column list --
	// the same precedent restpostgres/crud.go's own INSERT/UPDATE
	// already set for a jsonb column.
	q := fmt.Sprintf(`INSERT INTO %s (topic, type, key, data, attributes, occurred_at) VALUES ($1, $2, $3, $4, $5::jsonb, $6)`,
		pgx.Identifier{o.table()}.Sanitize())
	ts := now()
	batch := &pgx.Batch{}
	for _, e := range events {
		if e.Topic == "" {
			return errors.New("outboxpostgres: event without topic")
		}
		data, err := encode(e.Body)
		if err != nil {
			return fmt.Errorf("outboxpostgres: encode %s event: %w", e.Topic, err)
		}
		var attrs any
		if len(e.Attributes) > 0 {
			b, err := json.Marshal(e.Attributes)
			if err != nil {
				return fmt.Errorf("outboxpostgres: encode %s event attributes: %w", e.Topic, err)
			}
			attrs = b
		}
		batch.Queue(q, e.Topic, e.Type, e.Key, data, attrs, ts)
	}
	return postgres.NewStore(o.Pool, o.table()).Querier(ctx).SendBatch(ctx, batch).Close()
}

func encode(body any) ([]byte, error) {
	switch b := body.(type) {
	case []byte:
		return b, nil
	case string:
		return []byte(b), nil
	case nil:
		return []byte{}, nil
	default:
		return json.Marshal(b)
	}
}
