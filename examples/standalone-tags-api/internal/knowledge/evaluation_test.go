package knowledge

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"example.com/standalone-tags-api/internal/taglookup"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type judgment struct {
	Query   string   `json:"query"`
	Sources []string `json:"expected_sources"`
	Terms   []string `json:"expected_terms"`
}
type rankedResult struct {
	Sources           []string `json:"sources"`
	FirstRelevantRank int      `json:"first_relevant_rank"`
	Milliseconds      float64  `json:"milliseconds"`
}
type evaluationRow struct {
	Query   string       `json:"query"`
	Vector  rankedResult `json:"vector"`
	Lexical rankedResult `json:"lexical"`
}

// TestRealModelEvaluation is opt-in and never downloads weights or contacts a
// provider by default. It measures public repository docs, not private memory.
func TestRealModelEvaluation(t *testing.T) {
	endpoint := os.Getenv("KNOWLEDGE_EVAL_EMBED_URL")
	if endpoint == "" {
		t.Skip("set local evaluation endpoint and disposable KNOWLEDGE_TEST_DATABASE_URL")
	}
	dsn := os.Getenv("KNOWLEDGE_TEST_DATABASE_URL")
	model := os.Getenv("KNOWLEDGE_EVAL_MODEL")
	if dsn == "" || model == "" {
		t.Fatal("explicit evaluation database/model required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		t.Fatal(err)
	}
	store := &Postgres{Pool: pool, Model: EmbeddingContract(model, 768, "title: none | text: ", "task: search result | query: "), Dimensions: 768}
	if err = store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	scope := fmt.Sprintf("eval-%d", time.Now().UnixNano())
	subject := "evaluation-owner"
	defer pool.Exec(context.Background(), `DELETE FROM hyrule_knowledge_chunks WHERE scope=$1`, scope)
	embed := &Ollama{URL: endpoint, Model: model, Dimensions: 768, DocumentPrefix: "title: none | text: ", QueryPrefix: "task: search result | query: ", Client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	root := filepath.Clean("../../../..")
	filenames := []string{"architecture-decisions", "read-pilot", "mcp-pilot", "phase-6-contract-study", "ai-readiness", "outbox", "dry-run", "postgres-rfc"}
	digests := map[string]string{}
	indexStart := time.Now()
	for _, name := range filenames {
		source := "website/docs/reference/" + name + ".md"
		raw, e := os.ReadFile(filepath.Join(root, source))
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(raw)
		revision := hex.EncodeToString(sum[:])
		digests[source] = revision
		if e = Ingest(ctx, store, embed, scope, Document{ID: name, Source: source, Revision: revision, Subjects: []string{subject}, Text: string(raw)}); e != nil {
			t.Fatalf("index %s: %v", source, e)
		}
	}
	indexMS := float64(time.Since(indexStart).Microseconds()) / 1000
	var chunkCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM hyrule_knowledge_chunks WHERE scope=$1`, scope).Scan(&chunkCount); err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := taglookup.NewJWTIdentity(taglookup.JWTConfig{Issuer: "evaluation", Audience: "knowledge", Scope: scope, Keys: map[string]*rsa.PublicKey{"evaluation": &key.PublicKey}})
	if err != nil {
		t.Fatal(err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "evaluation", "aud": "knowledge", "sub": subject, "scope": "knowledge:read", "exp": time.Now().Add(time.Hour).Unix()})
	token.Header["kid"] = "evaluation"
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := taglookup.OpenSQLiteAudit(ctx, filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	server, err := NewServer(Config{Store: store, Embedder: embed, Identity: identity, Scope: scope, Token: func(context.Context) (string, error) { return signed, nil }, Audit: audit.Write})
	if err != nil {
		t.Fatal(err)
	}
	a, b := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "quality-evaluation", Version: "1"}, nil)
	session, err := client.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	raw, err := os.ReadFile("../../knowledge/questions.json")
	if err != nil {
		t.Fatal(err)
	}
	var questions []judgment
	if err = json.Unmarshal(raw, &questions); err != nil {
		t.Fatal(err)
	}
	rows := []evaluationRow{}
	vectorHits, lexicalHits := 0, 0
	vectorMRR, lexicalMRR := 0.0, 0.0
	latencies := []float64{}
	for _, q := range questions {
		start := time.Now()
		result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "knowledge.search", Arguments: map[string]any{"query": q.Query}})
		if e != nil || result.IsError {
			t.Fatalf("MCP search %q: %v %+v", q.Query, e, result)
		}
		elapsed := float64(time.Since(start).Microseconds()) / 1000
		latencies = append(latencies, elapsed)
		var payload struct {
			Chunks []Chunk `json:"chunks"`
		}
		if json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &payload) != nil {
			t.Fatal("invalid MCP output")
		}
		vector := judge(q, payload.Chunks, elapsed)
		// The operator baseline repeats JWT policy and applies exactly the same ACL,
		// model, scope and top-k as retrieval; it never ranks inaccessible rows.
		verified := taglookup.WithToken(ctx, signed)
		principal, e := identity.Verify(verified)
		if e != nil {
			t.Fatal(e)
		}
		if e = identity.AuthorizeKnowledge(verified, principal, taglookup.Action{Tool: "knowledge.search", Version: "1", Permission: "knowledge:read", Scope: scope}); e != nil {
			t.Fatal(e)
		}
		start = time.Now()
		lex, e := pool.Query(ctx, `SELECT id,source,revision,content FROM hyrule_knowledge_chunks WHERE scope=$1 AND $2=ANY(subjects) AND model=$3 AND vector_dims(embedding)=768 AND to_tsvector('english',content) @@ websearch_to_tsquery('english',$4) ORDER BY ts_rank_cd(to_tsvector('english',content),websearch_to_tsquery('english',$4)) DESC,id LIMIT 5`, scope, principal.Subject, store.Model, strings.Join(strings.Fields(q.Query), " OR "))
		if e != nil {
			t.Fatal(e)
		}
		lexicalChunks := []Chunk{}
		for lex.Next() {
			var c Chunk
			if e = lex.Scan(&c.ID, &c.Source, &c.Revision, &c.Text); e != nil {
				t.Fatal(e)
			}
			lexicalChunks = append(lexicalChunks, c)
		}
		e = lex.Err()
		lex.Close()
		if e != nil {
			t.Fatal(e)
		}
		lexical := judge(q, lexicalChunks, float64(time.Since(start).Microseconds())/1000)
		if vector.FirstRelevantRank > 0 {
			vectorHits++
			vectorMRR += 1 / float64(vector.FirstRelevantRank)
		}
		if lexical.FirstRelevantRank > 0 {
			lexicalHits++
			lexicalMRR += 1 / float64(lexical.FirstRelevantRank)
		}
		rows = append(rows, evaluationRow{q.Query, vector, lexical})
	}
	sort.Float64s(latencies)
	report := map[string]any{"model": model, "embedding_contract": store.Model, "dimensions": 768, "document_prefix": embed.DocumentPrefix, "query_prefix": embed.QueryPrefix, "documents_sha256": digests, "chunks": chunkCount, "index_ms": indexMS, "questions": len(questions), "vector_hit_at_5": float64(vectorHits) / float64(len(questions)), "lexical_hit_at_5": float64(lexicalHits) / float64(len(questions)), "vector_mrr_at_5": vectorMRR / float64(len(questions)), "lexical_mrr_at_5": lexicalMRR / float64(len(questions)), "vector_p95_ms": latencies[(len(latencies)*95-1)/100], "rows": rows, "judgment": "Expected source AND any expected term in an excerpt; Hit@5/MRR@5 on curated questions, not exhaustive Recall@5 or answer quality. English PostgreSQL full-text OR baseline; includes Portuguese-to-English queries."}
	output, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if path := os.Getenv("KNOWLEDGE_EVAL_REPORT"); path != "" {
		if err = os.WriteFile(path, append(output, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("model=%s chunks=%d hit@5=%d/%d lexical=%d/%d MRR@5=%.3f lexicalMRR=%.3f p95=%.1fms", model, chunkCount, vectorHits, len(questions), lexicalHits, len(questions), vectorMRR/float64(len(questions)), lexicalMRR/float64(len(questions)), latencies[(len(latencies)*95-1)/100])
}
func judge(q judgment, chunks []Chunk, ms float64) rankedResult {
	r := rankedResult{Sources: []string{}, Milliseconds: ms}
	for i, c := range chunks {
		r.Sources = append(r.Sources, c.Source)
		sourceMatch := false
		for _, s := range q.Sources {
			if c.Source == s {
				sourceMatch = true
			}
		}
		termMatch := false
		for _, term := range q.Terms {
			if strings.Contains(strings.ToLower(c.Text), strings.ToLower(term)) {
				termMatch = true
			}
		}
		if sourceMatch && termMatch && r.FirstRelevantRank == 0 {
			r.FirstRelevantRank = i + 1
		}
	}
	return r
}
