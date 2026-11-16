// Package commentsschema owns comments-api's versioned table schema. Migration
// execution is an explicit operator action, separate from application startup.
package commentsschema

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	pgxdriver "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

//go:embed sql/*.sql
var files embed.FS

const Latest = 3
const VersionTable = "comments_schema_migrations"

// Migrator embeds the standard runner with a forward-only target guard.
// One instance belongs to one sequential operator command.
type Migrator struct {
	*migrate.Migrate
	guard *forwardDriver
}

type forwardDriver struct {
	database.Driver
	target *uint
}

// migrate reads Version after acquiring its database lock. Checking here also
// prevents a downgrade if another migrator advances after To's initial read.
func (d *forwardDriver) Version() (int, bool, error) {
	v, dirty, err := d.Driver.Version()
	if err == nil && d.target != nil && v > int(*d.target) {
		return v, dirty, fmt.Errorf("refusing concurrent downgrade %d -> %d", v, *d.target)
	}
	return v, dirty, err
}

// Open uses the pool's current schema and bounded session settings. The SQL
// wrapper owns its reserved connection; closing it does not close the pool.
func Open(ctx context.Context, pool *pgxpool.Pool) (*Migrator, error) {
	var schema string
	if err := pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		return nil, err
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	drv, err := pgxdriver.WithInstance(sqlDB, &pgxdriver.Config{SchemaName: schema, MigrationsTable: VersionTable, StatementTimeout: 10 * time.Second})
	if err != nil {
		sqlDB.Close()
		return nil, err
	}
	source, err := iofs.New(files, "sql")
	if err != nil {
		drv.Close()
		return nil, err
	}
	guard := &forwardDriver{Driver: drv}
	m, err := migrate.NewWithInstance("iofs", source, "pgx5", guard)
	if err != nil {
		source.Close()
		drv.Close()
		return nil, err
	}
	m.LockTimeout = 10 * time.Second
	return &Migrator{Migrate: m, guard: guard}, nil
}

// To only advances known versions. There is no implicit down or dirty repair.
func To(m *Migrator, target uint) error {
	if target < 1 || target > Latest {
		return fmt.Errorf("target must be 1..%d", Latest)
	}
	v, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return err
	}
	if dirty {
		return fmt.Errorf("comments schema version %d is dirty; inspect and repair explicitly", v)
	}
	if v > target {
		return fmt.Errorf("refusing downgrade %d -> %d", v, target)
	}
	m.guard.target = &target
	defer func() { m.guard.target = nil }()
	err = m.Migrate.Migrate(target)
	if errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	return err
}

// Check never creates a table or changes migration state. Versions 1..3 are
// compatible with this HTTP binary, allowing it to serve throughout expansion.
func Check(ctx context.Context, pool *pgxpool.Pool, table string) error {
	var schema string
	if err := pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		return fmt.Errorf("comments-api: schema unavailable: %w", err)
	}
	var v int
	var dirty bool
	if err := pool.QueryRow(ctx, "SELECT version, dirty FROM "+pgx.Identifier{schema, VersionTable}.Sanitize()).Scan(&v, &dirty); err != nil {
		return fmt.Errorf("comments-api: schema unavailable; run comments-migrate before startup: %w", err)
	}
	if dirty || v < 1 || v > Latest {
		return fmt.Errorf("comments-api: incompatible schema version=%d dirty=%v; inspect migrations", v, dirty)
	}
	rows, err := pool.Query(ctx, "SELECT id, doc, created_at, updated_at, version, deleted FROM "+pgx.Identifier{schema, table}.Sanitize()+" LIMIT 0")
	if err != nil {
		return fmt.Errorf("comments-api: incompatible table: %w", err)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("comments-api: incompatible table: %w", err)
	}
	return nil
}

// Backfill serializes with migrators using their actual advisory lock. Each
// batch is a separate autocommit; API writers only contend with a short batch.
// Metadata (version/timestamps/etag) is preserved. Rerunning resumes safely.
func Backfill(ctx context.Context, pool *pgxpool.Pool, batch int) (int64, error) {
	if batch < 1 || batch > 10000 {
		return 0, fmt.Errorf("batch size must be 1..10000")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Release()
	var db, schema string
	if err := conn.QueryRow(ctx, "SELECT current_database(), current_schema()").Scan(&db, &schema); err != nil {
		return 0, err
	}
	key, err := database.GenerateAdvisoryLockId(db, schema, VersionTable)
	if err != nil {
		return 0, err
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		return 0, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		// Never return a session to the pool with an advisory lock still held.
		if _, err := conn.Exec(cleanup, "SELECT pg_advisory_unlock($1)", key); err != nil {
			conn.Conn().Close(cleanup)
		}
	}()
	var v int
	var dirty bool
	if err := conn.QueryRow(ctx, "SELECT version, dirty FROM "+pgx.Identifier{schema, VersionTable}.Sanitize()).Scan(&v, &dirty); err != nil {
		return 0, err
	}
	if dirty || v != 2 {
		return 0, fmt.Errorf("backfill requires clean version 2; got %d dirty=%v", v, dirty)
	}
	table := pgx.Identifier{schema, "comments"}.Sanitize()
	var invalid int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE (jsonb_typeof(doc->'post_id')='string') IS NOT TRUE").Scan(&invalid); err != nil {
		return 0, err
	}
	if invalid > 0 {
		return 0, fmt.Errorf("%d comments lack a string post_id; repair their actual references before backfill", invalid)
	}
	var total int64
	for {
		tag, err := conn.Exec(ctx, `WITH batch AS (
   SELECT id FROM `+table+` WHERE post_id IS DISTINCT FROM doc->>'post_id' ORDER BY id LIMIT $1 FOR UPDATE
  ) UPDATE `+table+` c SET post_id=c.doc->>'post_id' FROM batch WHERE c.id=batch.id`, batch)
		if err != nil {
			return total, err
		}
		n := tag.RowsAffected()
		total += n
		if n == 0 {
			return total, nil
		}
	}
}
