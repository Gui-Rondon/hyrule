package tagmcp

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func commandFiles(t *testing.T) map[string]string {
	t.Helper()
	f := setup(t)
	dir := t.TempDir()
	data := filepath.Join(dir, "tags data.db")
	resource(t, data, true)
	schema, err := filepath.Abs("../../resources.yaml")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKIXPublicKey(&f.key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "public.pem")
	if err = os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded}), 0600); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(dir, "token.jwt")
	if err = os.WriteFile(token, []byte(f.token.Load().(string)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"TAGS_MCP_RESOURCES_FILE": schema, "TAGS_MCP_DATA_FILE": data, "TAGS_MCP_AUDIT_FILE": filepath.Join(dir, "audit.db"), "TAGS_MCP_PUBLIC_KEY_FILE": key, "TAGS_MCP_KEY_ID": "key-1", "TAGS_MCP_ISSUER": issuer, "TAGS_MCP_AUDIENCE": audience, "TAGS_MCP_SCOPE": scope, "TAGS_MCP_TOKEN_FILE": token}
}
func TestCommandConfigurationRequiresExplicitFiles(t *testing.T) {
	values := commandFiles(t)
	get := func(name string) string { return values[name] }
	if _, err := commandConfiguration(get); err != nil {
		t.Fatal(err)
	}
	for _, name := range RequiredEnvironment() {
		before := values[name]
		values[name] = ""
		if _, err := commandConfiguration(get); err == nil {
			t.Fatalf("missing %s accepted", name)
		}
		values[name] = before
	}
	if _, err := commandConfiguration(nil); err == nil {
		t.Fatal("missing environment reader accepted")
	}
	source := values["TAGS_MCP_DATA_FILE"]
	values["TAGS_MCP_DATA_FILE"] = filepath.Join(t.TempDir(), "absent.db")
	if _, err := commandConfiguration(get); err == nil {
		t.Fatal("missing source accepted")
	}
	if _, err := os.Stat(values["TAGS_MCP_DATA_FILE"]); !os.IsNotExist(err) {
		t.Fatal("missing source was created")
	}
	values["TAGS_MCP_DATA_FILE"] = source
	for _, name := range []string{"TAGS_MCP_DATA_FILE", "TAGS_MCP_PUBLIC_KEY_FILE", "TAGS_MCP_TOKEN_FILE", "TAGS_MCP_RESOURCES_FILE"} {
		values["TAGS_MCP_AUDIT_FILE"] = values[name]
		if _, err := commandConfiguration(get); err == nil {
			t.Fatalf("audit reused %s", name)
		}
	}
	alias := filepath.Join(t.TempDir(), "alias.db")
	if err := os.Link(source, alias); err == nil {
		values["TAGS_MCP_AUDIT_FILE"] = alias
		if _, err = commandConfiguration(get); err == nil {
			t.Fatal("hardlinked audit/source accepted")
		}
	}
}
func TestCommandOpensSourceReadOnly(t *testing.T) {
	values := commandFiles(t)
	data := values["TAGS_MCP_DATA_FILE"]
	before, err := os.ReadFile(data)
	if err != nil {
		t.Fatal(err)
	}
	svc, db, err := openCommandResource(context.Background(), values["TAGS_MCP_RESOURCES_FILE"], data)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM tags").Scan(&count); err != nil || count != 2 {
		t.Fatalf("source read failed: %d %v", count, err)
	}
	if _, err = db.Exec("DELETE FROM tags"); err == nil {
		t.Fatal("data connection allowed mutations")
	}
	if svc.Resource().Name != "tags" {
		t.Fatal("wrong resource bound")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(data)
	if err != nil || string(before) != string(after) {
		t.Fatal("source file changed")
	}
}
func TestCommandRejectsWrongResourceAndStartupIdentity(t *testing.T) {
	values := commandFiles(t)
	raw, err := os.ReadFile(values["TAGS_MCP_RESOURCES_FILE"])
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"invalid yaml", strings.Replace(string(raw), "name: tags", "name: other", 1), strings.Replace(string(raw), "collection: tags", "collection: tags\n    backend: mongo", 1), strings.Repeat("x", 64*1024+1)} {
		path := filepath.Join(t.TempDir(), "resources.yaml")
		if err = os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, db, err := openCommandResource(context.Background(), path, values["TAGS_MCP_DATA_FILE"]); err == nil {
			db.Close()
			t.Fatal("incompatible resource accepted")
		}
	}
	values["TAGS_MCP_ISSUER"] = "https://wrong-issuer.example.test"
	if err = RunCommand(context.Background(), func(name string) string { return values[name] }); err == nil || !strings.Contains(err.Error(), "identity validation") {
		t.Fatalf("invalid startup identity accepted: %v", err)
	}
	if _, err = os.Stat(values["TAGS_MCP_AUDIT_FILE"]); !os.IsNotExist(err) {
		t.Fatal("invalid identity provisioned audit")
	}
}
func TestCommandTokenFileBoundedAndReread(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.jwt")
	if err := os.WriteFile(path, []byte("first\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	token, err := readCommandToken(context.Background(), path)
	if err != nil || token != "first" {
		t.Fatalf("token newline: %q %v", token, err)
	}
	if err = os.WriteFile(path, []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	token, err = readCommandToken(context.Background(), path)
	if err != nil || token != "second" {
		t.Fatal("token was cached")
	}
	if err = os.WriteFile(path, []byte(strings.Repeat("x", 9000)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readCommandToken(context.Background(), path); err == nil {
		t.Fatal("oversized token file accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = readCommandToken(ctx, path); err != context.Canceled {
		t.Fatalf("token read ignored context: %v", err)
	}
}
