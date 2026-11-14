package restpostgres

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
)

func testPool(t *testing.T) *pgxpool.Pool {
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

// setupResourceTable creates the jsonb-document table shape every
// restpostgres resource expects (postgres-rfc.md §4): id uuid primary
// key, doc jsonb, created_at/updated_at timestamptz, version bigint,
// deleted boolean.
func setupResourceTable(t *testing.T, pool *pgxpool.Pool, table string) {
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
		t.Fatal(err)
	}
	ddl := `CREATE TABLE ` + table + ` (
		id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
		doc        jsonb       NOT NULL DEFAULT '{}',
		created_at timestamptz NOT NULL DEFAULT now(),
		updated_at timestamptz NOT NULL DEFAULT now(),
		version    bigint      NOT NULL DEFAULT 1,
		deleted    boolean     NOT NULL DEFAULT false
	)`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
}

// newTestAdapter builds an adapter over a fresh table named table,
// with schema declaring fields' Kind for CompileFilter/sort casts.
func newTestAdapter(t *testing.T, pool *pgxpool.Pool, table string, fields map[string]postgres.Kind) *postgresAdapter {
	setupResourceTable(t, pool, table)
	a, err := NewAdapter(postgres.NewStore(pool, table), postgres.Transactor{Pool: pool}, Options{
		Retry: DefaultRetryOptions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	a.schema = postgres.SchemaSpec{Fields: fields}
	return a
}

func TestNewAdapterValidatesStorePool(t *testing.T) {
	_, err := NewAdapter(postgres.Store{Table: "whatever"}, postgres.Transactor{}, Options{
		Retry: DefaultRetryOptions(),
	})
	if err == nil {
		t.Fatal("expected an error for a Store with a nil Pool")
	}
}

func TestNewAdapterValidatesStoreTable(t *testing.T) {
	pool := testPool(t)
	_, err := NewAdapter(postgres.Store{Pool: pool}, postgres.Transactor{Pool: pool}, Options{
		Retry: DefaultRetryOptions(),
	})
	if err == nil {
		t.Fatal("expected an error for a Store with an empty Table")
	}
}

func TestNewAdapterValidatesRetryOptions(t *testing.T) {
	pool := testPool(t)
	_, err := NewAdapter(postgres.NewStore(pool, "whatever"), postgres.Transactor{Pool: pool}, Options{
		Retry: RetryOptions{}, // zero value is invalid
	})
	if err == nil {
		t.Fatal("expected an error for a zero-value RetryOptions")
	}
}

func TestRetryOptionsValidate(t *testing.T) {
	ok := DefaultRetryOptions()
	cases := []struct {
		name string
		opts RetryOptions
		want bool
	}{
		{"default", ok, true},
		{"negative MaxAttempts", RetryOptions{MaxAttempts: -1, BaseBackoff: ok.BaseBackoff, MaxBackoff: ok.MaxBackoff, MaxDuration: ok.MaxDuration}, false},
		{"zero MaxAttempts is valid", RetryOptions{MaxAttempts: 0, BaseBackoff: ok.BaseBackoff, MaxBackoff: ok.MaxBackoff, MaxDuration: ok.MaxDuration}, true},
		{"zero BaseBackoff", RetryOptions{MaxAttempts: 1, BaseBackoff: 0, MaxBackoff: ok.MaxBackoff, MaxDuration: ok.MaxDuration}, false},
		{"MaxBackoff below BaseBackoff", RetryOptions{MaxAttempts: 1, BaseBackoff: ok.BaseBackoff, MaxBackoff: ok.BaseBackoff / 2, MaxDuration: ok.MaxDuration}, false},
		{"zero MaxDuration", RetryOptions{MaxAttempts: 1, BaseBackoff: ok.BaseBackoff, MaxBackoff: ok.MaxBackoff, MaxDuration: 0}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.opts.validate()
			if (err == nil) != c.want {
				t.Errorf("validate() error = %v, want ok=%v", err, c.want)
			}
		})
	}
}

func TestNewAdapterValidatesSchema(t *testing.T) {
	pool := testPool(t)
	_, err := NewAdapter(postgres.NewStore(pool, "whatever"), postgres.Transactor{Pool: pool}, Options{
		Schema: rest.Schema{"bad": rest.Field{Type: "nonsense"}},
		Retry:  DefaultRetryOptions(),
	})
	if err == nil {
		t.Fatal("expected an error for an unsupported field type")
	}
}
