package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"example.com/standalone-tags-api/internal/knowledge"
	"example.com/standalone-tags-api/internal/knowledgedocs"
	"example.com/standalone-tags-api/internal/taglookup"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCuratorCommand(t *testing.T) {
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "public.pem")
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&key.PublicKey)}), 0600); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(dir, "token")
	token := func(permission string) {
		raw := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "issuer", "aud": "curator", "sub": "editor", "scope": permission, "exp": time.Now().Add(time.Hour).Unix()})
		raw.Header["kid"] = "k"
		signed, e := raw.SignedString(key)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(tokenPath, []byte(signed), 0600); e != nil {
			t.Fatal(e)
		}
	}
	scope := fmt.Sprintf("cli-articles-%d", time.Now().UnixNano())
	env := map[string]string{"KNOWLEDGE_DATABASE_URL": "invalid database configuration", "KNOWLEDGE_SCOPE": scope, "KNOWLEDGE_PUBLIC_KEY_FILE": keyPath, "KNOWLEDGE_KEY_ID": "k", "KNOWLEDGE_ISSUER": "issuer", "KNOWLEDGE_AUDIENCE": "curator", "KNOWLEDGE_TOKEN_FILE": tokenPath, "KNOWLEDGE_DOCUMENT_FILE": filepath.Join(dir, "missing")}
	get := func(name string) string { return env[name] }
	var output bytes.Buffer
	token("knowledge:read")
	if err = run(t.Context(), []string{"create"}, get, &output); !errors.Is(err, taglookup.ErrForbidden) || output.Len() != 0 {
		t.Fatal("authorization must precede database/body processing", err, output.String())
	}
	dsn := os.Getenv("KNOWLEDGE_TEST_DATABASE_URL")
	if dsn == "" {
		return
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(t.Context(), `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool.Exec(ctx, `DROP TABLE `+pgx.Identifier{knowledgedocs.Table(scope)}.Sanitize())
		pool.Exec(ctx, `DROP TABLE `+pgx.Identifier{(&knowledge.Postgres{Pool: pool}).SyncOutbox(scope).Table}.Sanitize())
		pool.Exec(ctx, `DELETE FROM hyrule_knowledge_documents WHERE scope=$1`, scope)
	}()
	env["KNOWLEDGE_DATABASE_URL"] = dsn
	if err = run(t.Context(), []string{"init"}, get, &output); err != nil {
		t.Fatal(err)
	}
	token("knowledge:write")
	path := filepath.Join(dir, "document.json")
	env["KNOWLEDGE_DOCUMENT_FILE"] = path
	if err = os.WriteFile(path, []byte(`{"title":"Example","body":"Body","readers":["alice"],"private_notes":"SECRET_NOT_IN_STDOUT"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = run(t.Context(), []string{"create"}, get, &output); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if json.Unmarshal(output.Bytes(), &result) != nil || len(result) != 3 || result["id"] == nil || result["etag"] == nil {
		t.Fatal(output.String())
	}
	env["KNOWLEDGE_DOCUMENT_ID"] = result["id"].(string)
	env["KNOWLEDGE_IF_MATCH"] = result["etag"].(string)
	output.Reset()
	if err = run(t.Context(), []string{"delete"}, get, &output); err != nil {
		t.Fatal(err)
	}
	source, err := (&knowledge.Postgres{Pool: pool}).LoadSource(t.Context(), scope, "articles/"+env["KNOWLEDGE_DOCUMENT_ID"])
	if err != nil || !source.Deleted {
		t.Fatal(source, err)
	}
}
