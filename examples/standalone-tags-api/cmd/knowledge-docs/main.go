// knowledge-docs is a curator CLI, separate from the read-only MCP server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"example.com/standalone-tags-api/internal/boundedfile"
	"example.com/standalone-tags-api/internal/knowledge"
	"example.com/standalone-tags-api/internal/knowledgedocs"
	"example.com/standalone-tags-api/internal/taglookup"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "knowledge-docs:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, getenv func(string) string, output io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: knowledge-docs init|create|patch|replace|delete")
	}
	op := args[0]
	switch op {
	case "init", "create", "patch", "replace", "delete":
	default:
		return errors.New("unsupported operation")
	}
	env := map[string]string{}
	names := []string{"DATABASE_URL", "SCOPE"}
	if op != "init" {
		names = append(names, "PUBLIC_KEY_FILE", "KEY_ID", "ISSUER", "AUDIENCE", "TOKEN_FILE")
	}
	for _, name := range names {
		value := getenv("KNOWLEDGE_" + name)
		if strings.TrimSpace(value) == "" {
			return errors.New("missing KNOWLEDGE_" + name)
		}
		env[name] = value
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var identity *taglookup.JWTIdentity
	if op != "init" {
		var err error
		identity, err = taglookup.LoadJWTIdentity(taglookup.JWTFileConfig{Issuer: env["ISSUER"], Audience: env["AUDIENCE"], Scope: env["SCOPE"], KeyID: env["KEY_ID"], PublicKeyFile: env["PUBLIC_KEY_FILE"]})
		if err != nil {
			return errors.New("invalid identity configuration")
		}
		raw, err := boundedfile.Read(env["TOKEN_FILE"], taglookup.MaxToken+2)
		if err != nil {
			return err
		}
		ctx = taglookup.WithToken(ctx, strings.TrimSpace(string(raw)))
		p, err := identity.Verify(ctx)
		if err != nil {
			return err
		}
		if err = identity.AuthorizeKnowledgeWrite(ctx, p, env["SCOPE"]); err != nil {
			return err
		}
	}
	pool, err := pgxpool.New(ctx, env["DATABASE_URL"])
	if err != nil {
		return errors.New("invalid database configuration")
	}
	defer pool.Close()
	if op == "init" {
		source := &knowledge.Postgres{Pool: pool}
		if source.Init(ctx) != nil || source.InitSync(ctx, env["SCOPE"]) != nil || knowledgedocs.Init(ctx, pool, env["SCOPE"]) != nil {
			return errors.New("provisioning failed; install vector extension first")
		}
		return nil
	}
	producer, err := knowledgedocs.New(knowledgedocs.Config{Pool: pool, Scope: env["SCOPE"], Identity: identity})
	if err != nil {
		return errors.New("producer unavailable")
	}
	var raw []byte
	if op != "delete" {
		path := getenv("KNOWLEDGE_DOCUMENT_FILE")
		if path == "" {
			return errors.New("missing KNOWLEDGE_DOCUMENT_FILE")
		}
		raw, err = boundedfile.Read(path, int64(knowledgedocs.MaxInput))
		if err != nil {
			return err
		}
	}
	doc, err := producer.Apply(ctx, op, getenv("KNOWLEDGE_DOCUMENT_ID"), getenv("KNOWLEDGE_IF_MATCH"), raw)
	if err != nil {
		if errors.Is(err, taglookup.ErrIdentity) || errors.Is(err, taglookup.ErrForbidden) {
			return err
		}
		return errors.New("document write rejected; check input, schema and If-Match")
	}
	result := map[string]any{"operation": op}
	if doc != nil {
		result["id"] = doc["_id"]
		result["etag"] = doc["_etag"]
	} else {
		result["id"] = getenv("KNOWLEDGE_DOCUMENT_ID")
	}
	return json.NewEncoder(output).Encode(result)
}
