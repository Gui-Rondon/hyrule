package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestTransactions(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE items (name TEXT)"); err != nil {
		t.Fatal(err)
	}
	tx := Transactor{db}
	sentinel := errors.New("abort")
	insert := func(ctx context.Context) error {
		_, err := tx.Querier(ctx).ExecContext(ctx, "INSERT INTO items VALUES ('a')")
		return err
	}
	if err := tx.WithTransaction(ctx, func(ctx context.Context) error {
		if err := tx.WithTransaction(ctx, insert); err != nil {
			t.Fatal(err)
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM items").Scan(&n); err != nil || n != 0 {
		t.Fatalf("rollback: %d %v", n, err)
	}
	if err := tx.WithTransaction(ctx, insert); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected panic")
			}
		}()
		_ = tx.WithTransaction(ctx, func(ctx context.Context) error { _ = insert(ctx); panic("abort") })
	}()
	if err := db.QueryRow("SELECT count(*) FROM items").Scan(&n); err != nil || n != 1 {
		t.Fatalf("panic rollback: %d %v", n, err)
	}
	other, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := tx.WithTransaction(ctx, func(ctx context.Context) error { return (Transactor{other}).WithTransaction(ctx, insert) }); err == nil {
		t.Fatal("cross database transaction accepted")
	}
	if err := (Transactor{}).WithTransaction(ctx, insert); err == nil {
		t.Fatal("nil database accepted")
	}
	if _, err := Open(ctx, ""); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, err := Open(ctx, filepath.Join(t.TempDir(), "missing", "data.db")); err == nil {
		t.Fatal("missing directory accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := tx.WithTransaction(canceled, insert); err == nil {
		t.Fatal("canceled transaction accepted")
	}
}
