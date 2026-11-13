package postgres

import (
	"context"
	"errors"
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
