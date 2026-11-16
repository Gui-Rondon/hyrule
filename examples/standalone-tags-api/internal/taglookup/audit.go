package taglookup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gui-Rondon/hyrule/platform/sqlite"
)

// SQLiteAudit is an append-only sink owned by the caller. Use a separate file
// from resource storage so canceled/rolled-back queries cannot erase audit.
// File retention, access control, backup and provisioning remain app concerns.
type SQLiteAudit struct{ db *sql.DB }

// OpenSQLiteAudit opens a persistent file, enables WAL and FULL synchronization,
// and creates the pilot's audit table. A successful Write has committed before
// returning. It is not a tamper-proof ledger or an exactly-once delivery system.
func OpenSQLiteAudit(ctx context.Context, path string) (*SQLiteAudit, error) {
	if strings.TrimSpace(path) == "" || path == ":memory:" || strings.ContainsAny(path, "?#") || strings.HasPrefix(path, "file:") {
		return nil, errors.New("taglookup: audit requires a filesystem path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("taglookup: audit path: %w", err)
	}
	// New files are private; existing files retain their deployment-owned mode.
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("taglookup: audit file: %w", err)
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	db, err := sqlite.Open(ctx, abs)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*SQLiteAudit, error) { db.Close(); return nil, err }
	var mode string
	if err = db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return fail(err)
	}
	if mode != "wal" {
		return fail(errors.New("taglookup: audit requires WAL journaling"))
	}
	if _, err = db.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
		return fail(err)
	}
	if _, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS tags_lookup_audit (
 sequence INTEGER PRIMARY KEY, recorded_at TEXT NOT NULL, record TEXT NOT NULL CHECK(json_valid(record)))`); err != nil {
		return fail(err)
	}
	return &SQLiteAudit{db: db}, nil
}

// Write records only the executor's sanitized metadata. ExecContext commits this
// append independently of resource transactions; concurrent calls are serialized
// by this sink's single SQLite connection. It must succeed before output release.
func (a *SQLiteAudit) Write(ctx context.Context, record Record) error {
	if a == nil || a.db == nil {
		return ErrAudit
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = a.db.ExecContext(ctx, `INSERT INTO tags_lookup_audit(recorded_at,record) VALUES (?,?)`, time.Now().UTC().Format(time.RFC3339Nano), string(data))
	return err
}
func (a *SQLiteAudit) Close() error {
	if a == nil || a.db == nil {
		return nil
	}
	return a.db.Close()
}
