package knowledge

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"example.com/standalone-tags-api/internal/taglookup"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fixedEmbed struct{ fail bool }

func (e fixedEmbed) Embed(context.Context, string) ([]float32, error) {
	if e.fail {
		return nil, errors.New("secret provider error")
	}
	return []float32{1, 0, 0}, nil
}

type countingEmbed struct{ calls int }

func (e *countingEmbed) Embed(context.Context, string) ([]float32, error) {
	e.calls++
	return []float32{1, 0, 0}, nil
}

type memoryStore struct {
	chunks []EmbeddedChunk
	doc    Document
	access Access
	writes int
}

func (s *memoryStore) Replace(_ context.Context, _ string, d Document, c []EmbeddedChunk) error {
	s.doc = d
	s.chunks = c
	s.writes++
	return nil
}
func (s *memoryStore) Search(_ context.Context, a Access, _ []float32, _ int) ([]Chunk, error) {
	s.access = a
	return []Chunk{{ID: strings.Repeat("a", 64), Text: "excerpt", Source: "ADR.md", Revision: "v1"}}, nil
}
func (s *memoryStore) Fetch(_ context.Context, a Access, id string) (Chunk, error) {
	s.access = a
	return Chunk{}, ErrNotFound
}
func TestIngestAtomicBeforeStoreAndUnicode(t *testing.T) {
	s := &memoryStore{}
	d := Document{ID: "adr", Source: "adr.md", Revision: "v1", Subjects: []string{"alice"}, Text: strings.Repeat("á", 1201)}
	if err := Ingest(t.Context(), s, fixedEmbed{}, "docs", d); err != nil {
		t.Fatal(err)
	}
	if len(s.chunks) != 2 || len([]rune(s.chunks[0].Chunk.Text)) != 1200 {
		t.Fatal("chunk boundary")
	}
	id := s.chunks[0].Chunk.ID
	if err := Ingest(t.Context(), s, fixedEmbed{}, "docs", d); err != nil || id != s.chunks[0].Chunk.ID {
		t.Fatal("unstable IDs", err)
	}
	if Ingest(t.Context(), s, fixedEmbed{fail: true}, "docs", d) == nil || s.writes != 2 {
		t.Fatal("partial ingestion")
	}
	d.Subjects = nil
	if Ingest(t.Context(), s, fixedEmbed{}, "docs", d) == nil {
		t.Fatal("empty ACL accepted")
	}
}
func TestStrictArguments(t *testing.T) {
	for _, raw := range []string{`{"query":"x","query":"y"}`, `{"query":"x","scope":"other"}`, `{"query":"x"} {}`, `{"query":1}`, `{"query":" "}`, `[]`} {
		if _, e := parseArgument([]byte(raw), "query", 512); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestOllama(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["truncate"] != false || body["model"] != "test" {
			t.Error("invalid request")
		}
		w.Write([]byte(`{"embeddings":[[1,0,0]]}`))
	}))
	defer server.Close()
	e := &Ollama{URL: server.URL, Model: "test", Dimensions: 3, Client: server.Client()}
	if _, err := e.Embed(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	e.Dimensions = 4
	if _, err := e.Embed(t.Context(), "hello"); err == nil {
		t.Fatal("dimension mismatch accepted")
	}
}
func TestMCPIdentityACLAndAudit(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	identity, e := taglookup.NewJWTIdentity(taglookup.JWTConfig{Issuer: "issuer", Audience: "client", Scope: "docs", Keys: map[string]*rsa.PublicKey{"k": &key.PublicKey}})
	if e != nil {
		t.Fatal(e)
	}
	sign := func(scope string) string {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "issuer", "aud": "client", "sub": "alice", "scope": scope, "exp": time.Now().Add(time.Hour).Unix()})
		token.Header["kid"] = "k"
		raw, err := token.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	token := sign("knowledge:read")
	store := &memoryStore{}
	embed := &countingEmbed{}
	auditFail := false
	records := []taglookup.Record{}
	server, e := NewServer(Config{Store: store, Embedder: embed, Identity: identity, Scope: "docs", Token: func(context.Context) (string, error) { return token, nil }, Audit: func(ctx context.Context, r taglookup.Record) error {
		records = append(records, r)
		if auditFail {
			return errors.New("failure")
		}
		return nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	a, b := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	session, e := server.Connect(ctx, a, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, e := client.Connect(ctx, b, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer cs.Close()
	tools, e := cs.ListTools(ctx, nil)
	if e != nil || len(tools.Tools) != 2 {
		t.Fatal(tools, e)
	}
	result, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: "knowledge.search", Arguments: map[string]any{"query": "architecture"}})
	if e != nil || result.IsError || store.access != (Access{"docs", "alice"}) {
		t.Fatal(result, e, store.access)
	}
	result, e = cs.CallTool(ctx, &mcp.CallToolParams{Name: "knowledge.fetch", Arguments: map[string]any{"id": strings.Repeat("a", 64)}})
	if e != nil || !result.IsError {
		t.Fatal("missing fetch", result, e)
	}
	token = sign("tags:read")
	if _, e = cs.ListTools(ctx, nil); e == nil {
		t.Fatal("wrong permission exposed tools")
	}
	if _, e = cs.CallTool(ctx, &mcp.CallToolParams{Name: "knowledge.search", Arguments: map[string]any{"query": "private documents"}}); e == nil {
		t.Fatal("wrong permission allowed processing")
	}
	if embed.calls != 1 {
		t.Fatal("denied operation reached embeddings", embed.calls)
	}
	token = "invalid"
	if _, e = cs.ListTools(ctx, nil); e == nil {
		t.Fatal("invalid JWT accepted")
	}
	token = sign("knowledge:read")
	auditFail = true
	if _, e = cs.CallTool(ctx, &mcp.CallToolParams{Name: "knowledge.search", Arguments: map[string]any{"query": "architecture"}}); e == nil || !strings.Contains(e.Error(), "audit unavailable") {
		t.Fatal("audit did not withhold output", e)
	}
	if len(records) != 7 || records[3].Authorized || records[4].Authorized || records[5].Subject != "" {
		t.Fatal("invalid audit", records)
	}
}
func TestPostgresIsolationReplacementAndRollback(t *testing.T) {
	dsn := os.Getenv("KNOWLEDGE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set KNOWLEDGE_TEST_DATABASE_URL to a disposable pgvector database")
	}
	pool, e := pgxpool.New(t.Context(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	if _, e = pool.Exec(t.Context(), `CREATE EXTENSION IF NOT EXISTS vector`); e != nil {
		t.Fatal(e)
	}
	p := &Postgres{Pool: pool, Model: "test-model", Dimensions: 3}
	if e = p.Init(t.Context()); e != nil {
		t.Fatal(e)
	}
	scope := fmt.Sprintf("test-%d", time.Now().UnixNano())
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool.Exec(cleanup, `DELETE FROM hyrule_knowledge_chunks WHERE scope=$1`, scope)
		pool.Exec(cleanup, `DELETE FROM hyrule_knowledge_documents WHERE scope=$1`, scope)
	}()
	d := Document{ID: "adr", Source: "adr.md", Revision: "v1", Subjects: []string{"alice"}, Text: strings.Repeat("a", 1201)}
	if e = Ingest(t.Context(), p, fixedEmbed{}, scope, d); e != nil {
		t.Fatal(e)
	}
	hits, e := p.Search(t.Context(), Access{scope, "alice"}, []float32{1, 0, 0}, 5)
	if e != nil || len(hits) != 2 {
		t.Fatal(hits, e)
	}
	oldID := hits[0].ID
	for _, a := range []Access{{scope, "bob"}, {scope + "other", "alice"}} {
		h, e := p.Search(t.Context(), a, []float32{1, 0, 0}, 5)
		if e != nil || len(h) != 0 {
			t.Fatal("ACL leak", h, e)
		}
		if _, e = p.Fetch(t.Context(), a, oldID); !errors.Is(e, ErrNotFound) {
			t.Fatal("fetch leak", e)
		}
	}
	before, err := p.LoadSource(t.Context(), scope, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = Ingest(t.Context(), p, fixedEmbed{}, scope, d); err != nil {
		t.Fatal(err)
	}
	after, err := p.LoadSource(t.Context(), scope, d.ID)
	if err != nil || before.Version != after.Version {
		t.Fatal("identical snapshot advanced source version", before, after, err)
	}
	// Failed replacement must preserve previously committed content and ACL.
	e = p.Replace(t.Context(), scope, d, []EmbeddedChunk{{Chunk: Chunk{ID: "invalid", Text: "x"}, Vector: []float32{1, 0}}})
	if e == nil {
		t.Fatal("bad dimensions accepted")
	}
	if _, e = p.Fetch(t.Context(), Access{scope, "alice"}, oldID); e != nil {
		t.Fatal("rollback lost data", e)
	}
	d.Text = "new version"
	d.Revision = "v2"
	d.Subjects = []string{"bob"}
	if e = Ingest(t.Context(), p, fixedEmbed{}, scope, d); e != nil {
		t.Fatal(e)
	}
	h, e := p.Search(t.Context(), Access{scope, "alice"}, []float32{1, 0, 0}, 5)
	if e != nil || len(h) != 0 {
		t.Fatal("revoked access remains", h, e)
	}
	h, e = p.Search(t.Context(), Access{scope, "bob"}, []float32{1, 0, 0}, 5)
	if e != nil || len(h) != 1 || h[0].Revision != "v2" {
		t.Fatal("stale chunks", h, e)
	}
	if _, e = p.Fetch(t.Context(), Access{scope, "bob"}, oldID); !errors.Is(e, ErrNotFound) {
		t.Fatal("stale fetch", e)
	}
	// Nearer documents belonging to Bob must not crowd Alice out of top-k.
	for n := 0; n < 6; n++ {
		doc := Document{ID: fmt.Sprintf("private-%d", n), Source: "private.md", Revision: "v1", Subjects: []string{"bob"}}
		if err := p.Replace(t.Context(), scope, doc, []EmbeddedChunk{{Chunk: Chunk{ID: fmt.Sprintf("%064d", n), Text: "private"}, Vector: []float32{1, 0, 0}}}); err != nil {
			t.Fatal(err)
		}
	}
	visible := Document{ID: "visible", Source: "visible.md", Revision: "v1", Subjects: []string{"alice"}}
	if err := p.Replace(t.Context(), scope, visible, []EmbeddedChunk{{Chunk: Chunk{ID: strings.Repeat("f", 64), Text: "visible"}, Vector: []float32{0, 1, 0}}}); err != nil {
		t.Fatal(err)
	}
	allowed, err := p.Search(t.Context(), Access{scope, "alice"}, []float32{1, 0, 0}, 1)
	if err != nil || len(allowed) != 1 || allowed[0].DocumentID != "visible" {
		t.Fatal("ACL must precede top-k", allowed, err)
	}
	p.Model = "other-model"
	h, e = p.Search(t.Context(), Access{scope, "bob"}, []float32{1, 0, 0}, 5)
	if e != nil || len(h) != 0 {
		t.Fatal("mixed models", h, e)
	}
}

func TestEmbeddingTaskPrefixes(t *testing.T) {
	inputs := []string{}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input string `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid request")
		}
		inputs = append(inputs, body.Input)
		w.Write([]byte(`{"embeddings":[[1,0,0]]}`))
	}))
	defer endpoint.Close()
	e := &Ollama{URL: endpoint.URL, Model: "test", Dimensions: 3, DocumentPrefix: "title: none | text: ", QueryPrefix: "task: search result | query: ", Client: endpoint.Client()}
	if _, err := e.Embed(t.Context(), "document"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.EmbedQuery(t.Context(), "question"); err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || inputs[0] != "title: none | text: document" || inputs[1] != "task: search result | query: question" {
		t.Fatal(inputs)
	}
}

func TestEmbeddingContract(t *testing.T) {
	base := EmbeddingContract("m", 768, "document:", "query:")
	for _, other := range []string{EmbeddingContract("m2", 768, "document:", "query:"), EmbeddingContract("m", 384, "document:", "query:"), EmbeddingContract("m", 768, "other:", "query:"), EmbeddingContract("m", 768, "document:", "other:")} {
		if base == other {
			t.Fatal("incompatible embedding contracts match")
		}
	}
	if base != EmbeddingContract("m", 768, "document:", "query:") {
		t.Fatal("unstable contract")
	}
}
