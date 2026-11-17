package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"example.com/standalone-tags-api/internal/knowledge"
	"example.com/standalone-tags-api/internal/taglookup"
	"github.com/Gui-Rondon/hyrule/outboxpostgres"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "knowledge-mcp:", err)
		os.Exit(1)
	}
}
func required(name string) (string, error) {
	v := os.Getenv("KNOWLEDGE_" + name)
	if strings.TrimSpace(v) == "" {
		return "", errors.New("missing KNOWLEDGE_" + name)
	}
	return v, nil
}
func run(ctx context.Context) error {
	if len(os.Args) != 2 || (os.Args[1] != "init" && os.Args[1] != "ingest" && os.Args[1] != "serve" && os.Args[1] != "enqueue" && os.Args[1] != "worker") {
		return errors.New("usage: knowledge-mcp init|ingest|enqueue|worker|serve (configuration via KNOWLEDGE_* environment)")
	}
	mode := os.Args[1]
	dsn, err := required("DATABASE_URL")
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(startup, dsn)
	if err != nil {
		return errors.New("invalid database configuration")
	}
	defer pool.Close()
	if pool.Ping(startup) != nil {
		return errors.New("database unavailable")
	}
	store := &knowledge.Postgres{Pool: pool}
	if mode == "init" {
		scope, e := required("SCOPE")
		if e != nil {
			return e
		}
		if store.Init(startup) != nil {
			return errors.New("schema provisioning failed (install vector extension first)")
		}
		if store.InitSync(startup, scope) != nil {
			return errors.New("outbox provisioning failed")
		}
		return nil
	}
	settings := map[string]string{}
	names := []string{"SCOPE"}
	if mode != "enqueue" {
		names = append(names, "EMBED_URL", "EMBED_MODEL", "EMBED_DIMENSIONS")
	}
	if mode == "ingest" || mode == "enqueue" {
		names = append(names, "MANIFEST", "DOCUMENT_ROOT")
	} else if mode == "serve" {
		names = append(names, "PUBLIC_KEY_FILE", "KEY_ID", "ISSUER", "AUDIENCE", "TOKEN_FILE", "AUDIT_FILE")
	}
	for _, name := range names {
		v, e := required(name)
		if e != nil {
			return e
		}
		settings[name] = v
	}
	if mode == "enqueue" {
		settings["ASYNC"] = "true"
		return ingest(ctx, store, nil, settings)
	}
	dimensions, err := strconv.Atoi(settings["EMBED_DIMENSIONS"])
	if err != nil || dimensions < 1 || dimensions > 4096 {
		return errors.New("invalid embedding dimensions")
	}
	store.Model = knowledge.EmbeddingContract(settings["EMBED_MODEL"], dimensions, os.Getenv("KNOWLEDGE_EMBED_DOCUMENT_PREFIX"), os.Getenv("KNOWLEDGE_EMBED_QUERY_PREFIX"))
	store.Dimensions = dimensions
	embed := &knowledge.Ollama{URL: settings["EMBED_URL"], Model: settings["EMBED_MODEL"], Dimensions: dimensions, DocumentPrefix: os.Getenv("KNOWLEDGE_EMBED_DOCUMENT_PREFIX"), QueryPrefix: os.Getenv("KNOWLEDGE_EMBED_QUERY_PREFIX"), Client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if mode == "ingest" {
		return ingest(ctx, store, embed, settings)
	}
	if mode == "worker" {
		if pool.Config().MaxConns < 2 {
			return errors.New("worker requires at least two database connections")
		}
		rows, e := pool.Query(startup, "SELECT 1 FROM "+pgx.Identifier{store.SyncOutbox(settings["SCOPE"]).Table}.Sanitize()+" LIMIT 0")
		if e != nil {
			return errors.New("outbox schema unavailable; run init")
		}
		rows.Close()
		worker := &knowledge.Worker{Store: store, Embedder: embed, Scope: settings["SCOPE"]}
		relay := &outboxpostgres.Relay{Outbox: store.SyncOutbox(settings["SCOPE"]), Publisher: worker, Interval: time.Second, PublishTimeout: 5 * time.Minute}
		return relay.Run(ctx)
	}
	identity, err := taglookup.LoadJWTIdentity(taglookup.JWTFileConfig{Issuer: settings["ISSUER"], Audience: settings["AUDIENCE"], Scope: settings["SCOPE"], KeyID: settings["KEY_ID"], PublicKeyFile: settings["PUBLIC_KEY_FILE"]})
	if err != nil {
		return errors.New("invalid identity configuration")
	}
	auditInfo, _ := os.Stat(settings["AUDIT_FILE"])
	for _, name := range []string{"TOKEN_FILE", "PUBLIC_KEY_FILE"} {
		info, e := os.Stat(settings[name])
		if e != nil || !info.Mode().IsRegular() {
			return errors.New("credential must be an existing regular file")
		}
		if auditInfo != nil && os.SameFile(info, auditInfo) {
			return errors.New("audit must be separate from credentials")
		}
		if settings[name] == settings["AUDIT_FILE"] {
			return errors.New("audit must be separate from credentials")
		}
	}
	audit, err := taglookup.OpenSQLiteAudit(startup, settings["AUDIT_FILE"])
	if err != nil {
		return errors.New("audit unavailable")
	}
	defer audit.Close()
	server, err := knowledge.NewServer(knowledge.Config{Store: store, Embedder: embed, Identity: identity, Scope: settings["SCOPE"], Audit: audit.Write, Token: func(ctx context.Context) (string, error) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		raw, e := readFile(settings["TOKEN_FILE"], taglookup.MaxToken+2)
		return strings.TrimSpace(string(raw)), e
	}})
	if err != nil {
		return err
	}
	err = server.Run(ctx, &mcp.StdioTransport{MaxLineLength: 64 * 1024})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return errors.New("MCP transport failed")
	}
	return nil
}
func readFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot read configured file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("regular file required")
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errors.New("file exceeds limit")
	}
	return raw, nil
}
func ingest(ctx context.Context, store knowledge.Store, embed knowledge.Embedder, c map[string]string) error {
	raw, err := readFile(c["MANIFEST"], 1024*1024)
	if err != nil {
		return err
	}
	var docs []struct {
		knowledge.Document
		Deleted         bool   `json:"deleted"`
		ExpectedVersion *int64 `json:"expected_version"`
	}
	if json.Unmarshal(raw, &docs) != nil || len(docs) == 0 || len(docs) > 1000 {
		return errors.New("invalid manifest")
	}
	root, err := os.OpenRoot(c["DOCUMENT_ROOT"])
	if err != nil {
		return errors.New("invalid document root")
	}
	defer root.Close()
	seen := map[string]bool{}
	for _, d := range docs {
		if seen[d.ID] || d.ID == "" || (!d.Deleted && d.Source == "") || d.Revision == "" || (len(d.Subjects) == 0 && c["ASYNC"] != "true") {
			return errors.New("invalid manifest document")
		}
		seen[d.ID] = true
		if c["ASYNC"] == "true" && (d.ExpectedVersion == nil || *d.ExpectedVersion < 0) {
			return errors.New("expected_version required for asynchronous source writes")
		}
		if d.Deleted {
			if c["ASYNC"] != "true" {
				return errors.New("tombstones require enqueue")
			}
			continue
		}
		f, e := root.Open(d.Source)
		if e != nil {
			return errors.New("document outside root or unavailable")
		}
		info, e := f.Stat()
		f.Close()
		if e != nil || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return errors.New("invalid document file")
		}
	}
	for _, d := range docs {
		if !d.Deleted {
			f, e := root.Open(d.Source)
			if e != nil {
				return errors.New("document unavailable")
			}
			raw, e := io.ReadAll(io.LimitReader(f, 1024*1024+1))
			f.Close()
			if e != nil || len(raw) > 1024*1024 {
				return errors.New("document read failed")
			}
			d.Text = string(raw)
		}
		documentCtx, done := context.WithTimeout(ctx, 5*time.Minute)
		var e error
		if c["ASYNC"] == "true" {
			pg, ok := store.(*knowledge.Postgres)
			if !ok {
				done()
				return errors.New("asynchronous source requires Postgres")
			}
			e = (postgres.Transactor{Pool: pg.Pool}).WithTransaction(documentCtx, func(txctx context.Context) error {
				_, err := pg.QueueSource(txctx, c["SCOPE"], d.Document, d.Deleted, *d.ExpectedVersion)
				return err
			})
		} else {
			e = knowledge.Ingest(documentCtx, store, embed, c["SCOPE"], d.Document)
		}
		done()
		if errors.Is(e, knowledge.ErrVersion) {
			return errors.New("source version conflict; reload expected_version")
		}
		if e != nil {
			return errors.New("document ingestion failed")
		}
	}
	return nil
}
