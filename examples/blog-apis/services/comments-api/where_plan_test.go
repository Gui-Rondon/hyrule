package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
)

func TestWherePredicatePlans(t *testing.T) {
	if os.Getenv("POSTGRES_WHERE_TEST") != "1" {
		t.Skip("manual predicate regressions: make postgres-ha-where-test")
	}
	runDeclaredIndexExplain(t, true)
}

func requireSelectiveIndexCondition(t *testing.T, raw json.RawMessage, index string) {
	t.Helper()
	type node struct {
		Index string            `json:"Index Name"`
		Cond  string            `json:"Index Cond"`
		Plans []json.RawMessage `json:"Plans"`
	}
	var plan []struct{ Plan json.RawMessage }
	if err := json.Unmarshal(raw, &plan); err != nil || len(plan) != 1 {
		t.Fatalf("invalid plan: %v", err)
	}
	var find func(json.RawMessage) bool
	find = func(raw json.RawMessage) bool {
		var n node
		if err := json.Unmarshal(raw, &n); err != nil {
			t.Fatal(err)
		}
		if n.Index == index && n.Cond != "" {
			return true
		}
		for _, child := range n.Plans {
			if find(child) {
				return true
			}
		}
		return false
	}
	if !find(plan[0].Plan) {
		t.Fatalf("selective predicate did not become an Index Cond on %s: %s", index, raw)
	}
}

func checkDatetimeEvaluations(t *testing.T, ctx context.Context, pool *pgxpool.Pool, rows int) {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		tx.Rollback(cleanup)
		for _, name := range names {
			if err := conn.Conn().Deallocate(cleanup, name); err != nil {
				conn.Conn().Close(cleanup)
				break
			}
		}
	}()
	if _, err := tx.Exec(ctx, "SET LOCAL track_functions='pl'; SET LOCAL plan_cache_mode='force_generic_plan'"); err != nil {
		t.Fatal(err)
	}
	calls := func() int64 {
		var n int64
		if err := tx.QueryRow(ctx, "SELECT COALESCE(sum(calls),0)::bigint FROM pg_stat_xact_user_functions WHERE schemaname=current_schema() AND funcname='hyrule_safe_timestamptz'").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	q := postgres.Query{"published_at": postgres.Query{"$gt": "2020-01-01T05:33:10Z"}}
	schema := postgres.SchemaSpec{Fields: map[string]postgres.Kind{"published_at": postgres.KindDateTime}}
	old, oldArgs, err := postgres.CompileFilter(q, schema, 0)
	if err != nil {
		t.Fatal(err)
	}
	optimized, args, err := postgres.CompileWhere(q, schema, 0)
	if err != nil || !reflect.DeepEqual(args, oldArgs) {
		t.Fatalf("datetime bindings: %v", err)
	}
	for _, test := range []struct{ name, where string }{{"legacy", old}, {"where", optimized}} {
		name := "predicate_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if _, err := conn.Conn().Prepare(ctx, name, "SELECT count(*) FROM explain_fixture WHERE "+test.where); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
		placeholders := make([]string, len(args))
		for i := range placeholders {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
		}
		execute := "EXECUTE " + pgx.Identifier{name}.Sanitize() + "(" + strings.Join(placeholders, ",") + ")"
		before := calls()
		var matched int
		if err := tx.QueryRow(ctx, execute, append([]any{pgx.QueryExecModeSimpleProtocol}, args...)...).Scan(&matched); err != nil {
			t.Fatal(err)
		}
		used := calls() - before
		if matched != 10 || used != int64(rows) {
			t.Fatalf("datetime %s: rows=%d calls=%d expected=%d", test.name, matched, used, rows)
		}
		t.Logf("datetime %s generic: matched=%d helper calls=%d (one per source row)", test.name, matched, used)
	}
}
