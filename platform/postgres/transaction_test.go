package postgres

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestWithTransactionCommits(t *testing.T) {
	pool := testPool(t)
	table := "tx_commit_test"
	setupTable(t, pool, table, `CREATE TABLE `+table+` (name TEXT NOT NULL)`)
	store := NewStore(pool, table)
	tx := Transactor{Pool: pool}

	err := tx.WithTransaction(context.Background(), func(ctx context.Context) error {
		return store.InsertMany(ctx, []map[string]any{{"name": "a"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := store.CountDocuments(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("count after commit = %d, want 1", n)
	}
}

func TestWithTransactionRollsBackOnError(t *testing.T) {
	pool := testPool(t)
	table := "tx_rollback_test"
	setupTable(t, pool, table, `CREATE TABLE `+table+` (name TEXT NOT NULL)`)
	store := NewStore(pool, table)
	tx := Transactor{Pool: pool}

	boom := errors.New("boom")
	err := tx.WithTransaction(context.Background(), func(ctx context.Context) error {
		if err := store.InsertMany(ctx, []map[string]any{{"name": "a"}}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	n, err := store.CountDocuments(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("count after rollback = %d, want 0 (the insert must not have persisted)", n)
	}
}

func TestWithTransactionStoreSeesItsOwnUncommittedWrites(t *testing.T) {
	// Proves Store actually joins the ambient transaction (via
	// querier(ctx)) rather than opening an unrelated connection: a
	// second statement inside the same WithTransaction call must see
	// the first statement's write before it's committed.
	pool := testPool(t)
	table := "tx_visibility_test"
	setupTable(t, pool, table, `CREATE TABLE `+table+` (name TEXT NOT NULL UNIQUE)`)
	store := NewStore(pool, table)
	tx := Transactor{Pool: pool}

	err := tx.WithTransaction(context.Background(), func(ctx context.Context) error {
		if err := store.InsertMany(ctx, []map[string]any{{"name": "a"}}); err != nil {
			return err
		}
		n, err := store.CountDocuments(ctx, Filter{"name": "a"})
		if err != nil {
			return err
		}
		if n != 1 {
			t.Fatalf("count inside the transaction = %d, want 1 (uncommitted write must be visible to a later statement on the same tx)", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWithTransactionCustomRollbackTimeout(t *testing.T) {
	pool := testPool(t)
	tx := Transactor{Pool: pool, RollbackTimeout: 2 * time.Second}
	boom := errors.New("boom")
	err := tx.WithTransaction(context.Background(), func(context.Context) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestWithTransactionNested(t *testing.T) {
	pool := testPool(t)
	tx := Transactor{Pool: pool}
	err := tx.WithTransaction(context.Background(), func(ctx context.Context) error {
		return tx.WithTransaction(ctx, func(context.Context) error { return nil })
	})
	if !errors.Is(err, ErrNestedTransaction) {
		t.Fatalf("err = %v, want ErrNestedTransaction", err)
	}
}

func TestWithTransactionForeign(t *testing.T) {
	pool1 := testPool(t)
	pool2 := testPool(t)
	tx1 := Transactor{Pool: pool1}
	tx2 := Transactor{Pool: pool2}
	err := tx1.WithTransaction(context.Background(), func(ctx context.Context) error {
		return tx2.WithTransaction(ctx, func(context.Context) error { return nil })
	})
	if !errors.Is(err, ErrForeignTransaction) {
		t.Fatalf("err = %v, want ErrForeignTransaction", err)
	}
}

func TestWithTransactionStoreOnAnotherPoolIgnoresForeignTx(t *testing.T) {
	// Store.querier must only pick up ctx's transaction when it was
	// opened on the same *pgxpool.Pool as the Store -- otherwise a
	// Store for a different pool silently (and incorrectly) joining
	// someone else's transaction would be a correctness bug.
	pool1 := testPool(t)
	pool2 := testPool(t)
	table := "tx_foreign_pool_test"
	setupTable(t, pool2, table, `CREATE TABLE `+table+` (name TEXT NOT NULL)`)
	storeOnPool2 := NewStore(pool2, table)
	tx1 := Transactor{Pool: pool1}

	err := tx1.WithTransaction(context.Background(), func(ctx context.Context) error {
		// ctx carries a transaction on pool1; storeOnPool2 must still
		// use pool2 directly (autocommit), not pool1's tx.
		return storeOnPool2.InsertMany(ctx, []map[string]any{{"name": "a"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := storeOnPool2.CountDocuments(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("count = %d, want 1 (the insert ran outside pool1's transaction and must have committed on its own)", n)
	}
}

// TestWithTransactionCommitFailureIsReturnedAndRollbackIsAQuietNoop
// covers section 13's "ambiguous" case -- a failure *during* COMMIT
// itself, never observed against a real Postgres before this test. A
// DEFERRABLE INITIALLY DEFERRED constraint trigger that always raises
// is the one deterministic way to force the failure to surface at
// COMMIT, not at an earlier statement, with a real server rather than
// a mock.
func TestWithTransactionCommitFailureIsReturnedAndRollbackIsAQuietNoop(t *testing.T) {
	pool := testPool(t)
	table := "tx_commit_failure_test"
	setupTable(t, pool, table, `CREATE TABLE `+table+` (name TEXT NOT NULL)`)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION `+table+`_reject() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'forced commit-time failure';
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE CONSTRAINT TRIGGER `+table+`_reject_trigger
		AFTER INSERT ON `+table+`
		DEFERRABLE INITIALLY DEFERRED
		FOR EACH ROW EXECUTE FUNCTION `+table+`_reject()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), "DROP FUNCTION IF EXISTS "+table+"_reject() CASCADE")
	})

	store := NewStore(pool, table)
	tx := Transactor{Pool: pool}

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	err := tx.WithTransaction(ctx, func(ctx context.Context) error {
		return store.InsertMany(ctx, []map[string]any{{"name": "a"}})
	})
	if err == nil {
		t.Fatal("WithTransaction = nil error, want the deferred trigger's commit-time failure")
	}
	if !strings.Contains(err.Error(), "forced commit-time failure") {
		t.Errorf("err = %v, want it to carry the trigger's own message", err)
	}
	// The deferred cleanup Rollback (transaction.go) must receive
	// pgx.ErrTxClosed -- the failed COMMIT already ended the
	// transaction server-side -- and transaction.go already tolerates
	// exactly that error without logging it as a failure.
	if strings.Contains(logBuf.String(), "rollback failed") {
		t.Errorf("log = %q, must not log a rollback failure for the cleanup Rollback after a failed Commit", logBuf.String())
	}

	n, err := store.CountDocuments(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("count after a failed commit = %d, want 0 (the server must have aborted the whole transaction)", n)
	}
}
