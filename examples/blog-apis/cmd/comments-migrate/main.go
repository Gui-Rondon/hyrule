// Command comments-migrate performs explicit comments-api schema operations.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/examples/blog-apis/internal/commentsschema"
	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
)

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("comments-migrate", flag.ContinueOnError)
	action := flags.String("action", "version", "version, up, prepare, backfill, or force (explicit dirty-state repair)")
	target := flags.Uint("to", commentsschema.Latest, "target schema version for up or force")
	schema := flags.String("schema", "public", "existing schema; never created by this command")
	batch := flags.Int("batch-size", 100, "backfill rows per transaction")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *action != "version" && *action != "up" && *action != "backfill" && *action != "force" && *action != "prepare" {
		return fmt.Errorf("unknown action %q", *action)
	}
	if *target < 1 || *target > commentsschema.Latest {
		return fmt.Errorf("target must be 1..%d", commentsschema.Latest)
	}
	explicitTarget := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "to" {
			explicitTarget = true
		}
	})
	if explicitTarget && *action != "up" && *action != "force" {
		return fmt.Errorf("-to is only supported by up and force")
	}
	if *action == "force" && !explicitTarget {
		return fmt.Errorf("force requires an explicit -to version after physical schema inspection")
	}
	uri := os.Getenv("POSTGRES_CONNECTION_STRING")
	if uri == "" {
		return fmt.Errorf("POSTGRES_CONNECTION_STRING is required")
	}
	// Resolve pool/timeouts using the same platform path, then scope every
	// connection to the explicitly selected schema before using the pool.
	pool, err := postgres.Connect(ctx, config.Config{"postgres": map[string]any{"uri": uri, "max_conns": 4, "connect_timeout": "2s", "statement_timeout": "10s", "lock_timeout": "3s"}}, "comments")
	if err != nil {
		return err
	}
	poolCfg := pool.Config()
	pool.Close()
	poolCfg.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{*schema}.Sanitize()
	pool, err = pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	var current string
	if err := pool.QueryRow(ctx, "SELECT current_schema()").Scan(&current); err != nil || current != *schema {
		return fmt.Errorf("schema %q must already exist: %v", *schema, err)
	}
	if *action == "version" {
		var exists bool
		name := pgx.Identifier{*schema, commentsschema.VersionTable}.Sanitize()
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", name).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			fmt.Println("version=none dirty=false")
			return nil
		}
		var v int
		var dirty bool
		err := pool.QueryRow(ctx, "SELECT version,dirty FROM "+name).Scan(&v, &dirty)
		if errors.Is(err, pgx.ErrNoRows) {
			fmt.Println("version=none dirty=false")
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Printf("version=%d dirty=%v\n", v, dirty)
		return nil
	}
	if *action == "backfill" {
		n, err := commentsschema.Backfill(ctx, pool, *batch)
		if err != nil {
			return err
		}
		fmt.Printf("backfilled %d rows\n", n)
		return nil
	}
	m, err := commentsschema.Open(ctx, pool)
	if err != nil {
		return err
	}
	defer m.Close()
	switch *action {
	case "prepare":
		v, dirty, err := m.Version()
		if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
			return err
		}
		if dirty || v > commentsschema.Latest {
			return fmt.Errorf("inspect schema version=%d dirty=%v before prepare", v, dirty)
		}
		if v < 1 {
			if err := commentsschema.To(m, 1); err != nil {
				return err
			}
			v = 1
		}
		if v < 2 {
			if err := commentsschema.To(m, 2); err != nil {
				return err
			}
			v = 2
		}
		if v == 2 {
			if _, err := commentsschema.Backfill(ctx, pool, *batch); err != nil {
				return err
			}
		}
		if err := commentsschema.To(m, 3); err != nil {
			return err
		}
	case "up":
		if err := commentsschema.To(m, *target); err != nil {
			return err
		}
	case "force":
		_, dirty, err := m.Version()
		if err != nil {
			return err
		}
		if !dirty {
			return fmt.Errorf("force is only allowed for a dirty schema after manual inspection")
		}
		if err := m.Force(int(*target)); err != nil {
			return err
		}
	}
	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		fmt.Println("version=none dirty=false")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("version=%d dirty=%v\n", v, dirty)
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
