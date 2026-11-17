package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gui-Rondon/hyrule/outbox"
	"github.com/Gui-Rondon/hyrule/outboxpostgres"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func syncFixture(t *testing.T) (*Postgres, string) {
	t.Helper()
	dsn := os.Getenv("KNOWLEDGE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable pgvector database")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err = pool.Exec(t.Context(), `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		t.Fatal(err)
	}
	p := &Postgres{Pool: pool, Model: "sync-test-contract", Dimensions: 3}
	if err = p.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	scope := fmt.Sprintf("sync-%d", time.Now().UnixNano())
	if err = p.InitSync(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool.Exec(ctx, `DELETE FROM hyrule_knowledge_chunks WHERE scope=$1`, scope)
		pool.Exec(ctx, `DELETE FROM hyrule_knowledge_documents WHERE scope=$1`, scope)
		pool.Exec(ctx, `DROP TABLE `+pgx.Identifier{p.SyncOutbox(scope).Table}.Sanitize())
	})
	return p, scope
}
func queue(t *testing.T, p *Postgres, scope string, d Document, deleted bool, expected int64) int64 {
	t.Helper()
	var version int64
	err := (postgres.Transactor{Pool: p.Pool}).WithTransaction(t.Context(), func(ctx context.Context) error {
		var err error
		version, err = p.QueueSource(ctx, scope, d, deleted, expected)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return version
}
func message(scope, id string, version int64) outbox.Message {
	raw, _ := json.Marshal(change{scope, id, version})
	return outbox.Message{Topic: syncTopic, Data: raw}
}
func TestSyncSourceAndEventAtomicity(t *testing.T) {
	p, scope := syncFixture(t)
	d := Document{ID: "doc", Source: "source.md", Revision: "r1", Subjects: []string{"alice"}, Text: "secret document body"}
	if _, err := p.QueueSource(t.Context(), scope, d, false, 0); !errors.Is(err, outbox.ErrNoTransaction) {
		t.Fatal("autocommit allowed", err)
	}
	foreign, err := pgxpool.NewWithConfig(t.Context(), p.Pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	err = (postgres.Transactor{Pool: foreign}).WithTransaction(t.Context(), func(ctx context.Context) error { _, err := p.QueueSource(ctx, scope, d, false, 0); return err })
	if !errors.Is(err, outbox.ErrNoTransaction) {
		t.Fatal("foreign-pool transaction allowed", err)
	}
	abort := errors.New("abort")
	err = (postgres.Transactor{Pool: p.Pool}).WithTransaction(t.Context(), func(ctx context.Context) error {
		if _, err := p.QueueSource(ctx, scope, d, false, 0); err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	if _, err = p.LoadSource(t.Context(), scope, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("source escaped rollback", err)
	}
	var count int
	table := pgx.Identifier{p.SyncOutbox(scope).Table}.Sanitize()
	if err = p.Pool.QueryRow(t.Context(), `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
		t.Fatal("event escaped rollback", count, err)
	}
	if version := queue(t, p, scope, d, false, 0); version != 1 {
		t.Fatal(version)
	}
	if version := queue(t, p, scope, d, false, 0); version != 1 {
		t.Fatal("unchanged replay changed version", version)
	}
	var raw []byte
	if err = p.Pool.QueryRow(t.Context(), `SELECT data FROM `+table).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "alice") || strings.Contains(string(raw), d.Text) {
		t.Fatal("outbox contains ACL or document")
	}
	d.Text = "changed"
	err = (postgres.Transactor{Pool: p.Pool}).WithTransaction(t.Context(), func(ctx context.Context) error { _, err := p.QueueSource(ctx, scope, d, false, 0); return err })
	if !errors.Is(err, ErrVersion) {
		t.Fatal("stale producer accepted", err)
	}
	if err = p.Pool.QueryRow(t.Context(), `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	// A failed event write must also roll back its source update.
	if _, err = p.Pool.Exec(t.Context(), `DROP TABLE `+table); err != nil {
		t.Fatal(err)
	}
	err = (postgres.Transactor{Pool: p.Pool}).WithTransaction(t.Context(), func(ctx context.Context) error { _, err := p.QueueSource(ctx, scope, d, false, 1); return err })
	if err == nil {
		t.Fatal("missing outbox accepted")
	}
	source, err := p.LoadSource(t.Context(), scope, d.ID)
	if err != nil || source.Version != 1 || source.Document.Text != "secret document body" {
		t.Fatal("source committed without event", source, err)
	}
}
func TestSyncRevocationDeletionReplayAndRecreation(t *testing.T) {
	p, scope := syncFixture(t)
	embed := &countingEmbed{}
	worker := &Worker{Store: p, Scope: scope, Embedder: embed}
	relay := &outboxpostgres.Relay{Outbox: p.SyncOutbox(scope), Publisher: worker, PublishTimeout: 5 * time.Second}
	d := Document{ID: "doc", Source: "source.md", Revision: "r1", Subjects: []string{"alice"}, Text: "initial content"}
	queue(t, p, scope, d, false, 0)
	if n, err := relay.Drain(t.Context()); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	hits, err := p.Search(t.Context(), Access{scope, "alice"}, []float32{1, 0, 0}, 5)
	if err != nil || len(hits) != 1 {
		t.Fatal(hits, err)
	}
	oldID := hits[0].ID
	if err = worker.Publish(t.Context(), message(scope, d.ID, 1)); err != nil || embed.calls != 1 {
		t.Fatal("duplicate reembedded", err, embed.calls)
	}
	d.Subjects = []string{"bob"}
	d.Revision = "r2"
	queue(t, p, scope, d, false, 1)
	// Old physical chunks still exist, but both old and new users are denied until
	// the current source version is indexed. Revocation does not wait for relay.
	var physical int
	if err = p.Pool.QueryRow(t.Context(), `SELECT count(*) FROM hyrule_knowledge_chunks WHERE scope=$1`, scope).Scan(&physical); err != nil || physical != 1 {
		t.Fatal(physical, err)
	}
	for _, subject := range []string{"alice", "bob"} {
		h, e := p.Search(t.Context(), Access{scope, subject}, []float32{1, 0, 0}, 5)
		if e != nil || len(h) != 0 {
			t.Fatal("stale version visible", subject, h, e)
		}
		if _, e = p.Fetch(t.Context(), Access{scope, subject}, oldID); !errors.Is(e, ErrNotFound) {
			t.Fatal("stale fetch", e)
		}
	}
	if err = worker.Publish(t.Context(), message(scope, d.ID, 1)); err != nil {
		t.Fatal(err)
	} // reordered old event indexes the latest source
	hits, err = p.Search(t.Context(), Access{scope, "bob"}, []float32{1, 0, 0}, 5)
	if err != nil || len(hits) != 1 || hits[0].Revision != "r2" {
		t.Fatal(hits, err)
	}
	if n, e := relay.Drain(t.Context()); e != nil || n != 1 || embed.calls != 2 {
		t.Fatal("ack replay reembedded", n, e, embed.calls)
	}
	d.Revision = "r3"
	queue(t, p, scope, d, true, 2)
	if _, e := p.Fetch(t.Context(), Access{scope, "bob"}, oldID); !errors.Is(e, ErrNotFound) {
		t.Fatal("deleted content visible before relay", e)
	}
	if err = worker.Publish(t.Context(), message(scope, d.ID, 1)); err != nil || embed.calls != 2 {
		t.Fatal("delete embedded content", err, embed.calls)
	}
	if err = p.Pool.QueryRow(t.Context(), `SELECT count(*) FROM hyrule_knowledge_chunks WHERE scope=$1`, scope).Scan(&physical); err != nil || physical != 0 {
		t.Fatal("delete did not purge", physical, err)
	}
	d.Revision = "r4"
	d.Text = "new content"
	queue(t, p, scope, d, false, 3)
	if err = worker.Publish(t.Context(), message(scope, d.ID, 3)); err != nil {
		t.Fatal("old deletion replay", err)
	}
	hits, err = p.Search(t.Context(), Access{scope, "bob"}, []float32{1, 0, 0}, 5)
	if err != nil || len(hits) != 1 || hits[0].Revision != "r4" {
		t.Fatal("old deletion removed recreation", hits, err)
	}
	d.Revision = "r5"
	d.Subjects = []string{}
	queue(t, p, scope, d, false, 4)
	if err = worker.Publish(t.Context(), message(scope, d.ID, 5)); err != nil || embed.calls != 3 {
		t.Fatal("empty ACL embedded content", err, embed.calls)
	}
	hits, err = p.Search(t.Context(), Access{scope, "bob"}, []float32{1, 0, 0}, 5)
	if err != nil || len(hits) != 0 {
		t.Fatal("empty ACL leak", hits, err)
	}
}

type changingEmbed struct {
	apply func()
	calls int
}

func (e *changingEmbed) Embed(context.Context, string) ([]float32, error) {
	e.calls++
	if e.apply != nil {
		f := e.apply
		e.apply = nil
		f()
	}
	return []float32{1, 0, 0}, nil
}
func TestSyncChangeDuringEmbeddingAndRetry(t *testing.T) {
	p, scope := syncFixture(t)
	d := Document{ID: "doc", Source: "source.md", Revision: "r1", Subjects: []string{"alice"}, Text: "v1"}
	queue(t, p, scope, d, false, 0)
	embed := &changingEmbed{apply: func() { d.Revision = "r2"; d.Text = "v2"; queue(t, p, scope, d, false, 1) }}
	worker := &Worker{Store: p, Scope: scope, Embedder: embed}
	if err := worker.Publish(t.Context(), message(scope, d.ID, 1)); err != nil {
		t.Fatal(err)
	}
	source, err := p.LoadSource(t.Context(), scope, d.ID)
	if err != nil || source.Version != 2 || source.IndexedVersion != 0 {
		t.Fatal("stale embedding overwrote source", source, err)
	}
	worker.Embedder = fixedEmbed{fail: true}
	relay := &outboxpostgres.Relay{Outbox: p.SyncOutbox(scope), Publisher: worker, MaxAttempts: 1}
	if n, e := relay.Drain(t.Context()); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	table := pgx.Identifier{p.SyncOutbox(scope).Table}.Sanitize()
	var dead int
	if err = p.Pool.QueryRow(t.Context(), `SELECT count(*) FROM `+table+` WHERE dead_at IS NOT NULL`).Scan(&dead); err != nil || dead != 2 {
		t.Fatal("DLQ", dead, err)
	}
	source, err = p.LoadSource(t.Context(), scope, d.ID)
	if err != nil || source.IndexedVersion != 0 {
		t.Fatal("failed embedding committed", source, err)
	}
	// Existing relay Replay recovers a failed job after the provider recovers.
	var eventID int64
	if err = p.Pool.QueryRow(t.Context(), `SELECT min(id) FROM `+table).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	worker.Embedder = embed
	if err = relay.Replay(t.Context(), eventID); err != nil {
		t.Fatal(err)
	}
	if n, e := relay.Drain(t.Context()); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	source, err = p.LoadSource(t.Context(), scope, d.ID)
	if err != nil || source.IndexedVersion != 2 {
		t.Fatal("replay did not recover", source, err)
	}
}

func TestSyncOptimisticConcurrentWrites(t *testing.T) {
	p, scope := syncFixture(t)
	d := Document{ID: "doc", Source: "source.md", Revision: "r1", Subjects: []string{"alice"}, Text: "v1"}
	queue(t, p, scope, d, false, 0)
	results := make(chan error, 2)
	for _, revision := range []string{"writer-a", "writer-b"} {
		go func(rev string) {
			next := d
			next.Revision = rev
			results <- (postgres.Transactor{Pool: p.Pool}).WithTransaction(t.Context(), func(ctx context.Context) error { _, err := p.QueueSource(ctx, scope, next, false, 1); return err })
		}(revision)
	}
	succeeded, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrVersion) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicts != 1 {
		t.Fatal(succeeded, conflicts)
	}
	worker := &Worker{Store: p, Scope: scope, Embedder: fixedEmbed{}}
	for range 2 {
		go func() { results <- worker.Publish(t.Context(), message(scope, d.ID, 1)) }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	source, err := p.LoadSource(t.Context(), scope, d.ID)
	if err != nil || source.Version != 2 || source.IndexedVersion != 2 {
		t.Fatal(source, err)
	}
	var count int
	if err = p.Pool.QueryRow(t.Context(), `SELECT count(*) FROM hyrule_knowledge_chunks WHERE scope=$1`, scope).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
func TestSyncLegacyChunksFailClosed(t *testing.T) {
	p, scope := syncFixture(t)
	_, err := p.Pool.Exec(t.Context(), `INSERT INTO hyrule_knowledge_chunks(scope,id,document_id,source,revision,subjects,model,content,embedding) VALUES($1,$2,'legacy','legacy.md','r0',ARRAY['alice'],$3,'old content','[1,0,0]'::vector)`, scope, strings.Repeat("a", 64), p.Model)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := p.Search(t.Context(), Access{scope, "alice"}, []float32{1, 0, 0}, 5)
	if err != nil || len(hits) != 0 {
		t.Fatal("unversioned chunk exposed", hits, err)
	}
	if _, err = p.Fetch(t.Context(), Access{scope, "alice"}, strings.Repeat("a", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	d := Document{ID: "legacy", Source: "legacy.md", Revision: "r1", Subjects: []string{"alice"}, Text: "current content"}
	queue(t, p, scope, d, false, 0)
	if err = (&Worker{Store: p, Scope: scope, Embedder: fixedEmbed{}}).Publish(t.Context(), message(scope, d.ID, 1)); err != nil {
		t.Fatal(err)
	}
	hits, err = p.Search(t.Context(), Access{scope, "alice"}, []float32{1, 0, 0}, 5)
	if err != nil || len(hits) != 1 || hits[0].Revision != "r1" {
		t.Fatal(hits, err)
	}
}

func TestSyncSourceRecreationCannotExposeOrphanedChunks(t *testing.T) {
	p, scope := syncFixture(t)
	d := Document{ID: "doc", Source: "source.md", Revision: "r1", Subjects: []string{"alice"}, Text: "old content"}
	if err := Ingest(t.Context(), p, fixedEmbed{}, scope, d); err != nil {
		t.Fatal(err)
	}
	hits, err := p.Search(t.Context(), Access{scope, "alice"}, []float32{1, 0, 0}, 5)
	if err != nil || len(hits) != 1 {
		t.Fatal(hits, err)
	}
	oldID := hits[0].ID
	// Simulate an operator physically removing a source without its index. The
	// new source's numeric version may restart, but it has not indexed that version.
	if _, err = p.Pool.Exec(t.Context(), `DELETE FROM hyrule_knowledge_documents WHERE scope=$1 AND document_id=$2`, scope, d.ID); err != nil {
		t.Fatal(err)
	}
	d.Text = "new content"
	d.Revision = "new generation"
	d.Subjects = []string{"bob"}
	queue(t, p, scope, d, false, 0)
	hits, err = p.Search(t.Context(), Access{scope, "bob"}, []float32{1, 0, 0}, 5)
	if err != nil || len(hits) != 0 {
		t.Fatal("orphaned content leaked to new source", hits, err)
	}
	if _, err = p.Fetch(t.Context(), Access{scope, "bob"}, oldID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
