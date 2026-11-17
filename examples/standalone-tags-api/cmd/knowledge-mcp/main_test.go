package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/standalone-tags-api/internal/knowledge"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestKnowledgeProcess(t *testing.T) {
	if os.Getenv("KNOWLEDGE_TEST_HELPER") != "1" {
		return
	}
	os.Args = []string{"knowledge-mcp", os.Getenv("KNOWLEDGE_TEST_MODE")}
	main()
	os.Exit(0)
}
func TestCommandIngestionAndStdio(t *testing.T) {
	dsn := os.Getenv("KNOWLEDGE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable pgvector database")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	if _, e = pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); e != nil {
		t.Fatal(e)
	}
	scope := "command-" + time.Now().Format("150405.000000000")
	defer pool.Exec(context.Background(), `DELETE FROM hyrule_knowledge_chunks WHERE scope=$1`, scope)
	dir := t.TempDir()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	write := func(name string, raw []byte) string {
		path := filepath.Join(dir, name)
		if e := os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
		return path
	}
	pub := write("key.pem", pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&key.PublicKey)}))
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "issuer", "aud": "client", "sub": "alice", "scope": "knowledge:read", "exp": time.Now().Add(time.Hour).Unix()})
	token.Header["kid"] = "k"
	signed, e := token.SignedString(key)
	if e != nil {
		t.Fatal(e)
	}
	tokenPath := write("token", []byte(signed))
	write("adr.md", []byte("Hyrule exposes MCP tools with explicit policy and audit."))
	manifest := write("manifest.json", []byte(`[{"id":"adr","source":"adr.md","revision":"v1","subjects":["alice"]}]`))
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"embeddings":[[1,0,0]]}`)) }))
	defer endpoint.Close()
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	environment := append(os.Environ(), "KNOWLEDGE_TEST_HELPER=1", "KNOWLEDGE_DATABASE_URL="+dsn, "KNOWLEDGE_SCOPE="+scope, "KNOWLEDGE_EMBED_URL="+endpoint.URL, "KNOWLEDGE_EMBED_MODEL=test-model", "KNOWLEDGE_EMBED_DIMENSIONS=3", "KNOWLEDGE_MANIFEST="+manifest, "KNOWLEDGE_DOCUMENT_ROOT="+dir, "KNOWLEDGE_PUBLIC_KEY_FILE="+pub, "KNOWLEDGE_KEY_ID=k", "KNOWLEDGE_ISSUER=issuer", "KNOWLEDGE_AUDIENCE=client", "KNOWLEDGE_TOKEN_FILE="+tokenPath, "KNOWLEDGE_AUDIT_FILE="+filepath.Join(dir, "audit.db"))
	command := func(mode string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestKnowledgeProcess$")
		cmd.Env = append(environment, "KNOWLEDGE_TEST_MODE="+mode)
		return cmd
	}
	for _, mode := range []string{"init", "ingest", "ingest"} {
		if output, e := command(mode).CombinedOutput(); e != nil || len(output) != 0 {
			t.Fatalf("%s: %s %v", mode, output, e)
		}
	}
	cmd := command("serve")
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, e := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	listed, e := session.ListTools(ctx, nil)
	if e != nil || len(listed.Tools) != 2 {
		t.Fatal(listed, e)
	}
	result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "knowledge.search", Arguments: map[string]any{"query": "MCP policy"}})
	if e != nil || result.IsError {
		t.Fatal(result, e)
	}
	var payload struct {
		Chunks []knowledge.Chunk `json:"chunks"`
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if json.Unmarshal([]byte(text), &payload) != nil || len(payload.Chunks) != 1 || payload.Chunks[0].Source != "adr.md" {
		t.Fatal(text)
	}
	fetched, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "knowledge.fetch", Arguments: map[string]any{"id": payload.Chunks[0].ID}})
	if e != nil || fetched.IsError {
		t.Fatal(fetched, e)
	}
	if e = os.WriteFile(tokenPath, []byte("invalid"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = session.ListTools(ctx, nil); e == nil {
		t.Fatal("token revocation ignored")
	}
	if e = session.Close(); e != nil {
		t.Fatal(e)
	}
	auditDB, e := sql.Open("sqlite", filepath.Join(dir, "audit.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer auditDB.Close()
	var count int
	if e = auditDB.QueryRowContext(ctx, "SELECT count(*) FROM tags_lookup_audit").Scan(&count); e != nil || count != 4 {
		t.Fatal("persistent audit", count, e)
	}
	// Manifest paths cannot escape the configured corpus root.
	if e = os.WriteFile(manifest, []byte(`[{"id":"escape","source":"../secret.md","revision":"v1","subjects":["alice"]}]`), 0600); e != nil {
		t.Fatal(e)
	}
	output, e := command("ingest").CombinedOutput()
	if e == nil || !strings.Contains(string(output), "outside root") {
		t.Fatal(string(output), e)
	}
}
