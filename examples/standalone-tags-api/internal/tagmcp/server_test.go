package tagmcp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"example.com/standalone-tags-api/internal/taglookup"
	"github.com/Gui-Rondon/hyrule/declarative"
	"github.com/Gui-Rondon/hyrule/platform/sqlite"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restsqlite"
	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const issuer = "https://mcp.example.test"
const audience = "tags-mcp-pilot"
const scope = "local:tags"

type observed struct {
	rest.Adapter
	reads atomic.Int64
	find  func(context.Context) ([]rest.Doc, error)
}

func (o *observed) Find(ctx context.Context, f, s, p any, skip, limit int64) ([]rest.Doc, error) {
	o.reads.Add(1)
	if limit != 1 {
		panic("unbounded MCP query")
	}
	if o.find != nil {
		return o.find(ctx)
	}
	return o.Adapter.Find(ctx, f, s, p, skip, limit)
}

type fixture struct {
	cfg       Config
	key       *rsa.PrivateKey
	token     atomic.Value
	adapter   *observed
	audit     *taglookup.SQLiteAudit
	auditPath string
}

func resource(t *testing.T, path string, seed bool) *rest.Service {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	file, err := os.Open("../../resources.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decl, err := declarative.Parse(file)
	if err != nil {
		t.Fatal(err)
	}
	res, err := decl.Resources[0].Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Adapter, err = restsqlite.NewAdapter(ctx, db, "tags", res)
	if err != nil {
		t.Fatal(err)
	}
	svc := rest.New(rest.Options{Transactor: sqlite.Transactor{DB: db}}).Register(res)
	if seed {
		_, err = svc.Insert(ctx, []rest.Doc{{"name": "Go", "slug": "go", "enabled": true}, {"name": "Hidden", "slug": "hidden", "enabled": false}}, rest.WriteOptions{})
		if err != nil {
			t.Fatal(err)
		}
	}
	return svc
}
func setup(t *testing.T) *fixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	id, err := taglookup.NewJWTIdentity(taglookup.JWTConfig{Issuer: issuer, Audience: audience, Scope: scope, Keys: map[string]*rsa.PublicKey{"key-1": &key.PublicKey}})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{key: key, auditPath: filepath.Join(t.TempDir(), "mcp-audit.db")}
	f.audit, err = taglookup.OpenSQLiteAudit(context.Background(), f.auditPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.audit.Close() })
	svc := resource(t, ":memory:", true)
	f.adapter = &observed{Adapter: svc.Resource().Adapter}
	svc.Resource().Adapter = f.adapter
	f.cfg = Config{Service: svc, Identity: id, Scope: scope, Audit: f.audit.Write, Token: func(ctx context.Context) (string, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > taglookup.ExecutionTimeout {
			return "", errors.New("missing credential retrieval deadline")
		}
		return f.token.Load().(string), nil
	}}
	f.token.Store(f.signed(t, taglookup.Permission))
	return f
}
func (f *fixture) signed(t *testing.T, permission string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": issuer, "aud": audience, "sub": "reader", "exp": time.Now().Add(time.Hour).Unix(), "scope": permission})
	token.Header["kid"] = "key-1"
	raw, err := token.SignedString(f.key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func connect(t *testing.T, cfg Config) *mcp.ClientSession {
	t.Helper()
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "pilot-test-client", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: ProtocolVersion})
	if err != nil {
		ss.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close(); ss.Close() })
	if cs.InitializeResult().ProtocolVersion != ProtocolVersion {
		t.Fatal("wrong protocol negotiated")
	}
	return cs
}
func compileSchema(t *testing.T, value any) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	if err := c.AddResource("urn:mcp-pilot", value); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("urn:mcp-pilot")
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestMCPClientDiscoversAndCallsExistingExecutor(t *testing.T) {
	f := setup(t)
	client := connect(t, f.cfg)
	ctx := context.Background()
	listed, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 1 || listed.Tools[0].Name != taglookup.Name || !listed.Tools[0].Annotations.ReadOnlyHint || f.adapter.reads.Load() != 0 {
		t.Fatal("invalid discovery or storage access")
	}
	tool := listed.Tools[0]
	if tool.OutputSchema.(map[string]any)["type"] != "object" {
		t.Fatal("MCP output schema must declare object root")
	}
	input, output := compileSchema(t, tool.InputSchema), compileSchema(t, tool.OutputSchema)
	if err = input.Validate(map[string]any{"slug": "go"}); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"go", "hidden", "missing"} {
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: taglookup.Name, Arguments: map[string]any{"slug": slug}})
		if err != nil || result.IsError {
			t.Fatalf("%s: %+v %v", slug, result, err)
		}
		if err = output.Validate(result.StructuredContent); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(result.StructuredContent)
		if strings.Contains(string(data), "_id") {
			t.Fatal("private metadata exposed")
		}
		if slug == "go" && !strings.Contains(string(data), `"slug":"go"`) {
			t.Fatal("missing tag")
		}
	}
	// MCP exposes the same operation contract, not SDK-inferred duplicate models.
	e, err := taglookup.New(taglookup.Dependencies{Service: f.cfg.Service, Scope: scope, Verify: f.cfg.Identity.Verify, Authorize: f.cfg.Identity.Authorize, Audit: f.cfg.Audit})
	if err != nil {
		t.Fatal(err)
	}
	contract, err := e.Discover(taglookup.WithToken(ctx, f.token.Load().(string)))
	if err != nil {
		t.Fatal(err)
	}
	meta := tool.Meta["hyrule"].(map[string]any)
	if meta["contractHash"] != contract.Hash {
		t.Fatal("contract hash changed in bridge")
	}
	want, _ := json.Marshal(contract.InputSchema)
	got, _ := json.Marshal(tool.InputSchema)
	if string(want) != string(got) {
		t.Fatal("MCP input contract diverged")
	}
}
func TestMCPReauthorizesAndIgnoresClientIdentityMetadata(t *testing.T) {
	f := setup(t)
	client := connect(t, f.cfg)
	ctx := context.Background()
	if _, err := client.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	f.token.Store("")
	fake := mcp.Meta{"scope": taglookup.Permission, "token": "not a verified credential"}
	_, err := client.ListTools(ctx, &mcp.ListToolsParams{Meta: fake})
	if err == nil || !strings.Contains(err.Error(), "unauthenticated") {
		t.Fatalf("cached authorization: %v", err)
	}
	_, err = client.CallTool(ctx, &mcp.CallToolParams{Name: taglookup.Name, Arguments: map[string]any{"slug": "go"}, Meta: fake})
	if err == nil || !strings.Contains(err.Error(), "unauthenticated") {
		t.Fatalf("metadata granted identity: %v", err)
	}
	f.token.Store(f.signed(t, "other:read"))
	if _, err = client.ListTools(ctx, nil); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("permission not rechecked: %v", err)
	}
	if f.adapter.reads.Load() != 0 {
		t.Fatal("denied MCP accessed data")
	}
	// Each server owns its credential source; an allowed peer cannot authorize it.
	second := setup(t)
	allowed := connect(t, second.cfg)
	if _, err = allowed.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = client.ListTools(ctx, nil); err == nil {
		t.Fatal("cross-server identity leak")
	}
}
func TestMCPInvalidInputUnknownToolAndAuditFailure(t *testing.T) {
	f := setup(t)
	client := connect(t, f.cfg)
	ctx := context.Background()
	for _, args := range []any{map[string]any{"slug": "go", "where": map[string]any{}}, json.RawMessage(`{"slug":"go","slug":"hidden"}`), map[string]any{"slug": strings.Repeat("x", taglookup.MaxInput+1)}} {
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: taglookup.Name, Arguments: args})
		if err != nil || !result.IsError || result.StructuredContent != nil {
			t.Fatalf("bad argument escaped: %+v %v", result, err)
		}
	}
	_, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "private-unknown", Arguments: map[string]any{"slug": "go"}})
	var rpc *jsonrpc.Error
	if !errors.As(err, &rpc) || rpc.Code != jsonrpc.CodeInvalidParams {
		t.Fatalf("unknown tool error: %v", err)
	}
	if f.adapter.reads.Load() != 0 {
		t.Fatal("invalid MCP queried storage")
	}
	if err = f.audit.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = client.ListTools(ctx, nil); err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatal("audit failure exposed contracts")
	}
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: taglookup.Name, Arguments: map[string]any{"slug": "go"}})
	if err != nil || !result.IsError || result.StructuredContent != nil {
		t.Fatalf("audit failure exposed output: %+v %v", result, err)
	}
}
func TestMCPCancellationReachesStorage(t *testing.T) {
	f := setup(t)
	entered := make(chan struct{})
	finished := make(chan struct{})
	f.adapter.find = func(ctx context.Context) ([]rest.Doc, error) {
		close(entered)
		<-ctx.Done()
		close(finished)
		return nil, ctx.Err()
	}
	client := connect(t, f.cfg)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := client.CallTool(ctx, &mcp.CallToolParams{Name: taglookup.Name, Arguments: map[string]any{"slug": "go"}})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("call did not reach storage")
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancellation ignored")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client did not cancel")
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not reach storage")
	}
}
func TestMCPRequiresConfiguredDependencies(t *testing.T) {
	f := setup(t)
	for _, change := range []func(*Config){func(c *Config) { c.Token = nil }, func(c *Config) { c.Identity = nil }, func(c *Config) { c.Audit = nil }, func(c *Config) { c.Service = nil }, func(c *Config) { c.Scope = "" }} {
		c := f.cfg
		change(&c)
		if _, err := NewServer(c); err == nil {
			t.Fatal("incomplete MCP configuration accepted")
		}
	}
}
func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("HYRULE_MCP_TEST_CHILD") != "1" {
		return
	}
	id, err := taglookup.LoadJWTIdentity(taglookup.JWTFileConfig{Issuer: issuer, Audience: audience, Scope: scope, KeyID: "key-1", PublicKeyFile: os.Getenv("HYRULE_MCP_TEST_KEY")})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	audit, err := taglookup.OpenSQLiteAudit(context.Background(), os.Getenv("HYRULE_MCP_TEST_AUDIT"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg := Config{Service: resource(t, os.Getenv("HYRULE_MCP_TEST_DB"), false), Identity: id, Scope: scope, Audit: audit.Write, Token: func(context.Context) (string, error) { return os.Getenv("HYRULE_MCP_TEST_TOKEN"), nil }}
	err = RunStdio(context.Background(), cfg)
	audit.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
func TestMCPClientOverActualStdio(t *testing.T) {
	f := setup(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "tags.db")
	resource(t, path, true)
	encoded, err := x509.MarshalPKIXPublicKey(&f.key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "public.pem")
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded}), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestMCPStdioHelper$")
	command.Env = append(os.Environ(), "HYRULE_MCP_TEST_CHILD=1", "HYRULE_MCP_TEST_KEY="+keyPath, "HYRULE_MCP_TEST_DB="+path, "HYRULE_MCP_TEST_AUDIT="+filepath.Join(dir, "audit.db"), "HYRULE_MCP_TEST_TOKEN="+f.token.Load().(string))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, &mcp.ClientSessionOptions{ProtocolVersion: ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 1 {
		t.Fatalf("stdio discovery: %+v %v", tools, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: taglookup.Name, Arguments: map[string]any{"slug": "go"}})
	if err != nil || result.IsError {
		t.Fatalf("stdio lookup: %+v %v", result, err)
	}
	data, _ := json.Marshal(result.StructuredContent)
	if !strings.Contains(string(data), `"slug":"go"`) {
		t.Fatalf("wrong stdio result: %s", data)
	}
}

func TestMCPNegotiatesSelectedProtocolAndRejectsCredentialSourceErrors(t *testing.T) {
	f := setup(t)
	server, err := NewServer(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "new-protocol-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.InitializeResult().ProtocolVersion != ProtocolVersion {
		t.Fatal("client did not negotiate the selected legacy protocol")
	}
	if _, err = session.ListTools(ctx, nil); err != nil || f.adapter.reads.Load() != 0 {
		t.Fatalf("negotiated discovery failed or accessed data: %v", err)
	}
	f.cfg.Token = func(context.Context) (string, error) { return "", errors.New("secret credential source") }
	allowed := connect(t, f.cfg)
	_, err = allowed.ListTools(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "unauthenticated") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("credential error leaked: %v", err)
	}
}
func TestMCPAuditFailureHasPriorityForInvalidRequests(t *testing.T) {
	f := setup(t)
	client := connect(t, f.cfg)
	if err := f.audit.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := client.ListTools(context.Background(), &mcp.ListToolsParams{Cursor: "invalid"})
	if err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatalf("cursor ignored audit failure: %v", err)
	}
	_, err = client.CallTool(context.Background(), &mcp.CallToolParams{Name: "unknown", Arguments: map[string]any{}})
	if err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatalf("unknown tool ignored audit failure: %v", err)
	}
}
