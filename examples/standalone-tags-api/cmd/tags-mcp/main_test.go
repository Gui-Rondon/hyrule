package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"example.com/standalone-tags-api/internal/taglookup"
	"example.com/standalone-tags-api/internal/tagmcp"
	"github.com/Gui-Rondon/hyrule/declarative"
	"github.com/Gui-Rondon/hyrule/platform/sqlite"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restsqlite"
	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestExecutableMCPStdio(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "tags-mcp")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-race", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build command: %v\n%s", err, output)
	}
	schemaPath, err := filepath.Abs("../../resources.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "tags data.db")
	db, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	declaration, err := declarative.Parse(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	res, err := declaration.Resources[0].Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Adapter, err = restsqlite.NewAdapter(context.Background(), db, "tags", res)
	if err != nil {
		t.Fatal(err)
	}
	svc := rest.New(rest.Options{Transactor: sqlite.Transactor{DB: db}}).Register(res)
	if _, err = svc.Insert(context.Background(), []rest.Doc{{"name": "Go", "slug": "go", "enabled": true}, {"name": "Hidden", "slug": "hidden", "enabled": false}}, rest.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "public.pem")
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}), 0600); err != nil {
		t.Fatal(err)
	}
	claims := jwt.MapClaims{"iss": "https://cli.example.test", "aud": "tags-cli", "sub": "reader", "exp": time.Now().Add(time.Hour).Unix(), "scope": taglookup.Permission}
	signed := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed.Header["kid"] = "key-1"
	raw, err := signed.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(dir, "owner.jwt")
	if err = os.WriteFile(tokenPath, []byte(raw+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"TAGS_MCP_RESOURCES_FILE": schemaPath, "TAGS_MCP_DATA_FILE": dbPath, "TAGS_MCP_AUDIT_FILE": filepath.Join(dir, "audit.db"), "TAGS_MCP_PUBLIC_KEY_FILE": keyPath, "TAGS_MCP_KEY_ID": "key-1", "TAGS_MCP_ISSUER": "https://cli.example.test", "TAGS_MCP_AUDIENCE": "tags-cli", "TAGS_MCP_SCOPE": "local:tags", "TAGS_MCP_TOKEN_FILE": tokenPath}
	environment := func(values map[string]string) []string {
		var env []string
		for _, v := range os.Environ() {
			if !strings.HasPrefix(v, "TAGS_MCP_") {
				env = append(env, v)
			}
		}
		for name, value := range values {
			env = append(env, name+"="+value)
		}
		return env
	}
	t.Run("real client and token reload", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command := exec.Command(binary)
		command.Dir = t.TempDir()
		command.Env = environment(values)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		client := mcp.NewClient(&mcp.Implementation{Name: "tags-command-client", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, &mcp.ClientSessionOptions{ProtocolVersion: tagmcp.ProtocolVersion})
		if err != nil {
			t.Fatalf("connect: %v; stderr: %s", err, stderr.String())
		}
		defer session.Close()
		tools, err := session.ListTools(ctx, nil)
		if err != nil || len(tools.Tools) != 1 {
			t.Fatalf("discover: %+v %v", tools, err)
		}
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: taglookup.Name, Arguments: map[string]any{"slug": "go"}})
		if err != nil || result.IsError {
			t.Fatalf("lookup: %+v %v", result, err)
		}
		output, _ := json.Marshal(result.StructuredContent)
		if !strings.Contains(string(output), `"slug":"go"`) {
			t.Fatalf("wrong output: %s", output)
		}
		if err = os.WriteFile(tokenPath, []byte("invalid-owner-token"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = session.CallTool(ctx, &mcp.CallToolParams{Name: taglookup.Name, Arguments: map[string]any{"slug": "go"}}); err == nil || !strings.Contains(err.Error(), "unauthenticated") {
			t.Fatalf("token file not reread: %v", err)
		}
		if err = os.WriteFile(tokenPath, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: taglookup.Name, Arguments: map[string]any{"slug": "hidden"}})
		if err != nil || result.IsError {
			t.Fatalf("reload: %+v %v", result, err)
		}
		output, _ = json.Marshal(result.StructuredContent)
		if string(output) != `{"found":false}` {
			t.Fatalf("disabled tag leaked: %s", output)
		}
		if err = session.Close(); err != nil {
			t.Fatal(err)
		}
		if command.ProcessState == nil || command.ProcessState.ExitCode() != 0 {
			t.Fatalf("stdio EOF did not shut down cleanly: %v; %s", command.ProcessState, stderr.String())
		}
		if strings.Contains(stderr.String(), raw) {
			t.Fatal("stderr leaked token")
		}
		after, err := os.ReadFile(dbPath)
		if err != nil || !bytes.Equal(original, after) {
			t.Fatal("executable changed source database")
		}
		audit, err := sqlite.Open(ctx, values["TAGS_MCP_AUDIT_FILE"])
		if err != nil {
			t.Fatal(err)
		}
		defer audit.Close()
		var count, denied int
		var records string
		if err = audit.QueryRow(`SELECT count(*),sum(json_extract(record,'$.Outcome')='unauthenticated'),group_concat(record,'') FROM tags_lookup_audit`).Scan(&count, &denied, &records); err != nil || count != 4 || denied != 1 {
			t.Fatalf("audit: %d %d %v", count, denied, err)
		}
		if strings.Contains(records, raw) || strings.Contains(records, `"slug"`) {
			t.Fatal("audit leaked credentials/input")
		}
	})
	t.Run("startup and usage stay off stdout", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			args    []string
			change  func(map[string]string)
			success bool
		}{
			{name: "help", args: []string{"--help"}, success: true},
			{name: "arguments", args: []string{"--token=never-print-this"}},
			{name: "missing setting", change: func(c map[string]string) { delete(c, "TAGS_MCP_ISSUER") }},
			{name: "wrong audience", change: func(c map[string]string) { c["TAGS_MCP_AUDIENCE"] = "wrong" }},
			{name: "same source and audit", change: func(c map[string]string) { c["TAGS_MCP_AUDIT_FILE"] = c["TAGS_MCP_DATA_FILE"] }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				copy := map[string]string{}
				for name, value := range values {
					copy[name] = value
				}
				if tc.change != nil {
					tc.change(copy)
				}
				command := exec.Command(binary, tc.args...)
				command.Env = environment(copy)
				var stdout, stderr bytes.Buffer
				command.Stdout = &stdout
				command.Stderr = &stderr
				err := command.Run()
				if (err == nil) != tc.success || stdout.Len() != 0 || stderr.Len() == 0 {
					t.Fatalf("startup result: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
				}
				if strings.Contains(stderr.String(), raw) || strings.Contains(stderr.String(), "never-print-this") {
					t.Fatal("startup diagnostics leaked credential arguments")
				}
			})
		}
	})
	t.Run("interrupt shutdown", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("os.Interrupt delivery is not supported for child processes on Windows; EOF is tested above")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		command := exec.Command(binary)
		command.Env = environment(values)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		client := mcp.NewClient(&mcp.Implementation{Name: "shutdown-client", Version: "1"}, nil)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, &mcp.ClientSessionOptions{ProtocolVersion: tagmcp.ProtocolVersion})
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		if _, err = session.ListTools(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if err = command.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- session.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			command.Process.Kill()
			t.Fatal("interrupt did not shut down executable")
		}
		if command.ProcessState == nil || command.ProcessState.ExitCode() != 0 {
			t.Fatalf("interrupt exit: %v; %s", command.ProcessState, stderr.String())
		}
	})
}
