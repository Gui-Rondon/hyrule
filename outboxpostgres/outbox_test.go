package outboxpostgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/outbox"
	"github.com/Gui-Rondon/hyrule/outboxpostgres"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	uri := os.Getenv("POSTGRES_TEST_URI")
	if uri == "" {
		t.Skip("POSTGRES_TEST_URI not set")
	}
	pool, err := pgxpool.New(context.Background(), uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pool
}

// testTable creates a throwaway outbox table for the test, dropped at
// cleanup.
func testTable(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	table := fmt.Sprintf("outbox_test_%d", time.Now().UnixNano())
	if err := outboxpostgres.EnsureTable(context.Background(), pool, table); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
	return table
}

type row struct {
	Topic         string
	Type          string
	Key           string
	Data          []byte
	Attributes    *string
	OccurredAt    time.Time
	PublishedAt   *time.Time
	Attempts      int
	LastError     string
	NextAttemptAt *time.Time
	DeadAt        *time.Time
}

func selectAll(t *testing.T, pool *pgxpool.Pool, table string) []row {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		"SELECT topic, type, key, data, attributes::text, occurred_at, published_at, attempts, last_error, next_attempt_at, dead_at FROM "+table+" ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.Topic, &r.Type, &r.Key, &r.Data, &r.Attributes, &r.OccurredAt, &r.PublishedAt, &r.Attempts, &r.LastError, &r.NextAttemptAt, &r.DeadAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func countAll(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAddRequiresTransaction(t *testing.T) {
	pool := testPool(t)
	table := testTable(t, pool)
	box := &outboxpostgres.Outbox{Pool: pool, Table: table}
	err := box.Add(context.Background(), outbox.Event{Topic: "t"})
	if !errors.Is(err, outbox.ErrNoTransaction) {
		t.Fatalf("err = %v", err)
	}
	if n := countAll(t, pool, table); n != 0 {
		t.Errorf("stored %d events outside a transaction", n)
	}
}

// The events are stored with the write or not at all.
func TestAddFollowsTheTransaction(t *testing.T) {
	pool := testPool(t)
	table := testTable(t, pool)
	if _, err := pool.Exec(context.Background(), "CREATE TABLE videos_test (title text)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP TABLE IF EXISTS videos_test") })

	box := &outboxpostgres.Outbox{Pool: pool, Table: table}
	tx := postgres.Transactor{Pool: pool}
	ctx := context.Background()

	write := func(fail bool) error {
		return tx.WithTransaction(ctx, func(ctx context.Context) error {
			if _, err := pool.Exec(ctx, "INSERT INTO videos_test (title) VALUES ($1)", "x"); err != nil {
				return err
			}
			if err := box.Add(ctx, outbox.Event{Topic: "videos-to-publish", Type: "video.publish_requested",
				Key: "v1", Body: map[string]any{"_id": "v1"}, Attributes: map[string]string{"origin": "edit"}}); err != nil {
				return err
			}
			if fail {
				return errors.New("write failed after the event")
			}
			return nil
		})
	}

	if err := write(true); err == nil {
		t.Fatal("expected error")
	}
	if n := countAll(t, pool, table); n != 0 {
		t.Fatalf("event kept after rollback: %d", n)
	}

	if err := write(false); err != nil {
		t.Fatal(err)
	}
	rows := selectAll(t, pool, table)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	got := rows[0]
	var attrs map[string]string
	if got.Attributes != nil {
		json.Unmarshal([]byte(*got.Attributes), &attrs)
	}
	if got.Topic != "videos-to-publish" || got.Type != "video.publish_requested" || got.Key != "v1" ||
		string(got.Data) != `{"_id":"v1"}` || attrs["origin"] != "edit" || got.PublishedAt != nil || got.OccurredAt.IsZero() {
		t.Errorf("event = %+v (attrs %v)", got, attrs)
	}
}

func TestAddAllowNoTransaction(t *testing.T) {
	pool := testPool(t)
	table := testTable(t, pool)
	box := &outboxpostgres.Outbox{Pool: pool, Table: table, AllowNoTransaction: true}
	if err := box.Add(context.Background(), outbox.Event{Topic: "t"}); err != nil {
		t.Fatalf("add with AllowNoTransaction = %v", err)
	}
	if n := countAll(t, pool, table); n != 1 {
		t.Errorf("stored %d events, want 1", n)
	}
}

func TestAddEncodesBody(t *testing.T) {
	pool := testPool(t)
	table := testTable(t, pool)
	box := &outboxpostgres.Outbox{Pool: pool, Table: table, AllowNoTransaction: true}
	ctx := context.Background()
	err := box.Add(ctx,
		outbox.Event{Topic: "raw", Body: []byte("bytes")},
		outbox.Event{Topic: "str", Body: "texto"},
		outbox.Event{Topic: "json", Body: struct {
			ID string `json:"id"`
		}{"a"}},
		outbox.Event{Topic: "empty"},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"raw": "bytes", "str": "texto", "json": `{"id":"a"}`, "empty": ""}
	for _, r := range selectAll(t, pool, table) {
		if string(r.Data) != want[r.Topic] {
			t.Errorf("%s = %q", r.Topic, r.Data)
		}
	}
	if err := box.Add(ctx, outbox.Event{}); err == nil {
		t.Error("event without topic must fail")
	}
	if err := box.Add(ctx, outbox.Event{Topic: "x", Body: make(chan int)}); err == nil {
		t.Error("unencodable body must fail")
	}
}

func TestEnsureTableAndOutboxDefaultToTableConstant(t *testing.T) {
	// The default table name is also used by real services. Isolate the
	// default-name test in its own schema instead of touching public.outbox.
	admin := testPool(t)
	schema := fmt.Sprintf("outbox_default_%d", time.Now().UnixNano())
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(context.Background(), "DROP SCHEMA "+ident+" CASCADE") })
	cfg := admin.Config()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := outboxpostgres.EnsureTable(context.Background(), pool, ""); err != nil {
		t.Fatal(err)
	}
	box := &outboxpostgres.Outbox{Pool: pool, AllowNoTransaction: true}
	if err := box.Add(context.Background(), outbox.Event{Topic: "t"}); err != nil {
		t.Fatal(err)
	}
	if n := countAll(t, pool, outboxpostgres.Table); n != 1 {
		t.Errorf("rows in the default table = %d, want 1", n)
	}
}

func TestAddNoAttributesIsSQLNull(t *testing.T) {
	pool := testPool(t)
	table := testTable(t, pool)
	box := &outboxpostgres.Outbox{Pool: pool, Table: table, AllowNoTransaction: true}
	if err := box.Add(context.Background(), outbox.Event{Topic: "t"}); err != nil {
		t.Fatal(err)
	}
	rows := selectAll(t, pool, table)
	if len(rows) != 1 || rows[0].Attributes != nil {
		t.Errorf("attributes = %v, want SQL NULL", rows[0].Attributes)
	}
}
