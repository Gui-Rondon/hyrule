package taglookup

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPersistentAuditWithProvisionedIdentity(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "audit.db")
	audit, err := OpenSQLiteAudit(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	key := rsaKey(t)
	id, err := LoadJWTIdentity(JWTFileConfig{Issuer: testIssuer, Audience: testAudience, Scope: testScope, KeyID: "key-1", PublicKeyFile: publicKeyFile(t, &key.PublicKey)})
	if err != nil {
		t.Fatal(err)
	}
	r := setup(t, nil)
	r.d.Verify = id.Verify
	r.d.Authorize = id.Authorize
	r.d.Audit = audit.Write
	r.rebuild(t)
	raw := signed(t, key, "key-1", tokenClaims())
	authCtx := WithToken(ctx, raw)
	before := r.snapshot()
	out, err := r.e.Execute(authCtx, strings.NewReader(`{"slug":"go"}`))
	if err != nil || out == nil {
		t.Fatalf("lookup: %s %v", out, err)
	}
	if _, err = r.e.Execute(ctx, strings.NewReader(`{"slug":"private-search"}`)); err != ErrIdentity {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(authCtx)
	cancel()
	if _, err = r.e.Execute(canceled, strings.NewReader(`{"slug":"go"}`)); err != ErrCanceled {
		t.Fatal(err)
	}
	if before != r.snapshot() {
		t.Fatal("lookup mutated source records")
	}
	if err = audit.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLiteAudit(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var synchronous int
	if err = reopened.db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil || synchronous != 2 {
		t.Fatalf("FULL synchronization missing: %d %v", synchronous, err)
	}
	rows, err := reopened.db.Query("SELECT recorded_at,record FROM tags_lookup_audit ORDER BY sequence")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var records []Record
	for rows.Next() {
		var timestamp, data string
		if err = rows.Scan(&timestamp, &data); err != nil {
			t.Fatal(err)
		}
		if timestamp == "" || strings.Contains(data, raw) || strings.Contains(data, "private-search") {
			t.Fatal("missing timestamp or leaked credential/input")
		}
		var rec Record
		if err = json.Unmarshal([]byte(data), &rec); err != nil {
			t.Fatal(err)
		}
		records = append(records, rec)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[0].Subject != "reader" || !records[0].Authorized || records[0].Outcome != "ok" || records[1].Outcome != ErrIdentity.Error() || records[2].Outcome != ErrCanceled.Error() {
		t.Fatalf("unexpected persisted records: %+v", records)
	}
}
func TestPersistentAuditFailureWithholdsOutput(t *testing.T) {
	audit, err := OpenSQLiteAudit(context.Background(), filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	r := setup(t, nil)
	r.d.Audit = audit.Write
	r.rebuild(t)
	if err = audit.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := r.e.Execute(context.Background(), strings.NewReader(`{"slug":"go"}`))
	if out != nil || !errors.Is(err, ErrAudit) {
		t.Fatalf("audit failure released output: %s %v", out, err)
	}
}
func TestPersistentAuditConcurrentAppends(t *testing.T) {
	audit, err := OpenSQLiteAudit(context.Background(), filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	r := setup(t, nil)
	r.d.Service.Resource().Adapter = r.a.Adapter
	r.d.Audit = audit.Write
	r.rebuild(t)
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.e.Execute(context.Background(), strings.NewReader(`{"slug":"go"}`))
			if err != nil {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	var count, ids int
	if err = audit.db.QueryRow(`SELECT count(*),count(DISTINCT json_extract(record,'$.CorrelationID')) FROM tags_lookup_audit`).Scan(&count, &ids); err != nil || count != 12 || ids != 12 {
		t.Fatalf("missing/merged audit records: %d %d %v", count, ids, err)
	}
}
func TestAuditProvisioningFailsClosed(t *testing.T) {
	for _, path := range []string{"", ":memory:", "file:audit.db?mode=memory", filepath.Join(t.TempDir(), "missing", "audit.db")} {
		if audit, err := OpenSQLiteAudit(context.Background(), path); err == nil {
			audit.Close()
			t.Fatalf("invalid audit path accepted: %q", path)
		}
	}
	var audit *SQLiteAudit
	if err := audit.Write(context.Background(), Record{}); err != ErrAudit {
		t.Fatal("nil sink accepted write")
	}
	if err := audit.Close(); err != nil {
		t.Fatal(err)
	}
}
