package knowledgedocs

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/standalone-tags-api/internal/knowledge"
	"example.com/standalone-tags-api/internal/taglookup"
	"github.com/Gui-Rondon/hyrule/outboxpostgres"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type recordingEmbed struct {
	mu    sync.Mutex
	texts []string
}

func (e *recordingEmbed) Embed(_ context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.texts = append(e.texts, text)
	return []float32{1, 0, 0}, nil
}
func TestArticleProducerToMCP(t *testing.T) {
	dsn := os.Getenv("KNOWLEDGE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable pgvector database")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		t.Fatal(err)
	}
	scope := fmt.Sprintf("articles-%d", time.Now().UnixNano())
	source := &knowledge.Postgres{Pool: pool, Model: "producer-test", Dimensions: 3}
	if err = source.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err = source.InitSync(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err = Init(ctx, pool, scope); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		pool.Exec(cleanup, `DROP TABLE `+pgx.Identifier{Table(scope)}.Sanitize())
		pool.Exec(cleanup, `DROP TABLE `+pgx.Identifier{Table(scope + "-other")}.Sanitize())
		pool.Exec(cleanup, `DROP TABLE `+pgx.Identifier{source.SyncOutbox(scope).Table}.Sanitize())
		pool.Exec(cleanup, `DELETE FROM hyrule_knowledge_chunks WHERE scope=$1`, scope)
		pool.Exec(cleanup, `DELETE FROM hyrule_knowledge_documents WHERE scope=$1`, scope)
	}()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	makeIdentity := func(scope string) *taglookup.JWTIdentity {
		i, e := taglookup.NewJWTIdentity(taglookup.JWTConfig{Issuer: "producer", Audience: "knowledge", Scope: scope, Keys: map[string]*rsa.PublicKey{"k": &key.PublicKey}})
		if e != nil {
			t.Fatal(e)
		}
		return i
	}
	sign := func(subject, permission string) string {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "producer", "aud": "knowledge", "sub": subject, "scope": permission, "exp": time.Now().Add(time.Hour).Unix()})
		token.Header["kid"] = "k"
		raw, e := token.SignedString(key)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	identity := makeIdentity(scope)
	producer, err := New(Config{pool, scope, identity})
	if err != nil {
		t.Fatal(err)
	}
	writer := taglookup.WithToken(ctx, sign("curator", "knowledge:write"))
	if _, err = producer.Apply(taglookup.WithToken(ctx, sign("alice", "knowledge:read")), "create", "", "", []byte("not JSON")); !errors.Is(err, taglookup.ErrForbidden) {
		t.Fatal("authorization must precede input processing", err)
	}
	if _, err = producer.Apply(ctx, "create", "", "", nil); !errors.Is(err, taglookup.ErrIdentity) {
		t.Fatal("missing JWT accepted", err)
	}
	apply := func(op, id, etag, raw string) rest.Doc {
		d, e := producer.Apply(writer, op, id, etag, []byte(raw))
		if e != nil {
			t.Fatalf("%s: %v", op, e)
		}
		return d
	}
	doc := apply("create", "", "", `{"title":"Transactions","body":"Source and outbox commit together.","readers":["alice"],"private_notes":"DO_NOT_EMBED_PRIVATE"}`)
	id := fmt.Sprint(doc["_id"])
	etag := doc["_etag"].(string)
	sourceID := "articles/" + id
	original, err := source.LoadSource(ctx, scope, sourceID)
	if err != nil || strings.Contains(original.Document.Text, "PRIVATE") || !strings.Contains(original.Document.Text, "Source and outbox") {
		t.Fatal("projection", original, err)
	}
	embed := &recordingEmbed{}
	relay := &outboxpostgres.Relay{Outbox: source.SyncOutbox(scope), Publisher: &knowledge.Worker{Store: source, Embedder: embed, Scope: scope}}
	drain := func() {
		t.Helper()
		if n, e := relay.Drain(ctx); e != nil || n != 1 {
			t.Fatal(n, e)
		}
	}
	drain()
	var owner atomic.Value
	owner.Store(sign("alice", "knowledge:read"))
	audit, err := taglookup.OpenSQLiteAudit(ctx, t.TempDir()+"/audit.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	server, err := knowledge.NewServer(knowledge.Config{Store: source, Embedder: embed, Identity: identity, Scope: scope, Token: func(context.Context) (string, error) { return owner.Load().(string), nil }, Audit: audit.Write})
	if err != nil {
		t.Fatal(err)
	}
	a, b := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "article-integration", Version: "1"}, nil)
	session, err := client.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	search := func() []knowledge.Chunk {
		t.Helper()
		result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "knowledge.search", Arguments: map[string]any{"query": "transactions"}})
		if e != nil || result.IsError {
			t.Fatal(result, e)
		}
		text := result.Content[0].(*mcp.TextContent).Text
		if strings.Contains(text, "DO_NOT_EMBED") {
			t.Fatal("private data in MCP output")
		}
		var p struct {
			Chunks []knowledge.Chunk `json:"chunks"`
		}
		if json.Unmarshal([]byte(text), &p) != nil {
			t.Fatal(text)
		}
		return p.Chunks
	}
	if hits := search(); len(hits) != 1 {
		t.Fatal(hits)
	}
	doc = apply("patch", id, etag, `{"private_notes":"CHANGED_PRIVATE"}`)
	etag = doc["_etag"].(string)
	after, err := source.LoadSource(ctx, scope, sourceID)
	if err != nil || after.Version != original.Version {
		t.Fatal("unindexed field created new source version", after, err)
	}
	doc = apply("patch", id, etag, `{"readers":["bob"]}`)
	etag = doc["_etag"].(string)
	if hits := search(); len(hits) != 0 {
		t.Fatal("revocation waited for worker", hits)
	}
	owner.Store(sign("bob", "knowledge:read"))
	if hits := search(); len(hits) != 0 {
		t.Fatal("stale content granted before indexing", hits)
	}
	drain()
	if hits := search(); len(hits) != 1 {
		t.Fatal(hits)
	}
	if _, err = producer.Apply(writer, "patch", id, "1", []byte(`{"body":"stale overwrite"}`)); err == nil {
		t.Fatal("stale ETag accepted")
	}
	// Source and event writes done by the producer hook must roll back if a
	// subsequent domain hook aborts the actual article mutation.
	prior, err := source.LoadSource(ctx, scope, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	resource := producer.service.Resource()
	resource.Hooks.OnUpdate = append(resource.Hooks.OnUpdate, func(context.Context, rest.Doc, rest.Doc) error { return rest.Abort(409, "domain rejection") })
	if _, err = producer.Apply(writer, "patch", id, etag, []byte(`{"body":"must roll back"}`)); err == nil {
		t.Fatal("domain failure ignored")
	}
	resource.Hooks.OnUpdate = resource.Hooks.OnUpdate[:1]
	after, err = source.LoadSource(ctx, scope, sourceID)
	if err != nil || after.Version != prior.Version || after.Document.Text != prior.Document.Text {
		t.Fatal("source escaped rollback", after, err)
	}
	if n, e := relay.Drain(ctx); e != nil || n != 0 {
		t.Fatal("event escaped rollback", n, e)
	}
	// A second configured corpus cannot mutate the first corpus's article ID.
	otherScope := scope + "-other"
	if err = Init(ctx, pool, otherScope); err != nil {
		t.Fatal(err)
	}
	other, err := New(Config{pool, otherScope, makeIdentity(otherScope)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.Apply(writer, "patch", id, etag, []byte(`{"readers":["mallory"]}`)); !errors.Is(err, rest.ErrNotFound) {
		t.Fatal("cross-corpus article access", err)
	}
	doc = apply("replace", id, etag, `{"title":"Updated","body":"Replacement body.","readers":["bob"]}`)
	etag = doc["_etag"].(string)
	drain()
	if hits := search(); len(hits) != 1 || !strings.Contains(hits[0].Text, "Replacement body") {
		t.Fatal(hits)
	}
	apply("delete", id, etag, "")
	if hits := search(); len(hits) != 0 {
		t.Fatal("delete waited for worker", hits)
	}
	drain()
	after, err = source.LoadSource(ctx, scope, sourceID)
	if err != nil || !after.Deleted || after.Document.Text != "" {
		t.Fatal(after, err)
	}
	embed.mu.Lock()
	defer embed.mu.Unlock()
	for _, text := range embed.texts {
		if strings.Contains(text, "PRIVATE") {
			t.Fatal("private field sent to embedding provider")
		}
	}
	// An untrusted direct transaction cannot bypass the producer's policy hook.
	if err = (postgres.Transactor{Pool: pool}).WithTransaction(ctx, func(txctx context.Context) error { return producer.project(txctx, doc, false) }); !errors.Is(err, taglookup.ErrIdentity) {
		t.Fatal("hook trusted unverified identity", err)
	}
}
func TestStrictProducerInput(t *testing.T) {
	for _, raw := range []string{`{"title":"a","title":"b"}`, `{"_id":"injected"}`, `{"corpus_scope":"other"}`, `{} {}`, `[]`} {
		if _, err := parse([]byte(raw)); err == nil {
			t.Fatal(raw)
		}
	}
}
