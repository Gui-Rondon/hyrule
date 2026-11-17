// Package knowledgedocs is a concrete, optional document producer built on
// rest.Service. It projects selected article fields and explicit readers through
// pre-write hooks into the knowledge source/outbox in the same transaction.
package knowledgedocs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"example.com/standalone-tags-api/internal/knowledge"
	"example.com/standalone-tags-api/internal/taglookup"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restpostgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Table isolates corpora physically because the current Postgres adapter does
// not apply Resource.Datasource.Filter. Never infer tenant filtering from it.
func Table(scope string) string {
	sum := sha256.Sum256([]byte(scope))
	return fmt.Sprintf("hyrule_knowledge_articles_%x", sum[:12])
}

const MaxInput = 2 * 1024 * 1024

type Config struct {
	Pool     *pgxpool.Pool
	Scope    string
	Identity *taglookup.JWTIdentity
}
type Producer struct {
	service *rest.Service
	config  Config
	source  *knowledge.Postgres
}

// Init explicitly provisions the producer; New never creates or alters tables.
func Init(ctx context.Context, pool *pgxpool.Pool, scope string) error {
	_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+pgx.Identifier{Table(scope)}.Sanitize()+` (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),doc jsonb NOT NULL DEFAULT '{}',
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1,deleted boolean NOT NULL DEFAULT false)`)
	return err
}
func New(c Config) (*Producer, error) {
	if c.Pool == nil || strings.TrimSpace(c.Scope) == "" || c.Identity == nil {
		return nil, errors.New("document producer configuration required")
	}
	schema := rest.Schema{
		"title":         {Type: rest.TypeString, Required: true, NotEmpty: true, MaxLength: 256},
		"body":          {Type: rest.TypeString, Required: true, NotEmpty: true, MaxLength: 1024*1024 - 300},
		"readers":       {Type: rest.TypeList, Required: true, MaxLength: 100, Items: &rest.Field{Type: rest.TypeString, NotEmpty: true, MaxLength: 256}},
		"private_notes": {Type: rest.TypeString, MaxLength: 4096},
	}
	tx := postgres.Transactor{Pool: c.Pool}
	adapter, err := restpostgres.NewAdapter(postgres.NewStore(c.Pool, Table(c.Scope)), tx, restpostgres.Options{Schema: schema, Retry: rest.DefaultRetryOptions()})
	if err != nil {
		return nil, err
	}
	p := &Producer{config: c, source: &knowledge.Postgres{Pool: c.Pool}}
	res := &rest.Resource{Name: "articles", Adapter: adapter, Schema: schema}
	res.Hooks = rest.Hooks{
		OnInsert: []func(context.Context, []rest.Doc) error{func(ctx context.Context, docs []rest.Doc) error {
			for _, d := range docs {
				if err := p.project(ctx, d, false); err != nil {
					return err
				}
			}
			return nil
		}},
		OnUpdate: []func(context.Context, rest.Doc, rest.Doc) error{func(ctx context.Context, updates, original rest.Doc) error {
			merged, err := adapter.MergePatch(original, updates)
			if err != nil {
				return err
			}
			return p.project(ctx, merged, false)
		}},
		OnReplace: []func(context.Context, rest.Doc, rest.Doc) error{func(ctx context.Context, replacement, original rest.Doc) error {
			copy := rest.Doc{}
			for k, v := range replacement {
				copy[k] = v
			}
			copy["_id"] = original["_id"]
			return p.project(ctx, copy, false)
		}},
		OnDelete: []func(context.Context, rest.Doc) error{func(ctx context.Context, original rest.Doc) error { return p.project(ctx, original, true) }},
	}
	p.service = rest.NewService(res, tx)
	return p, nil
}
func (p *Producer) authorize(ctx context.Context) error {
	principal, err := p.config.Identity.Verify(ctx)
	if err != nil {
		return err
	}
	return p.config.Identity.AuthorizeKnowledgeWrite(ctx, principal, p.config.Scope)
}
func (p *Producer) project(ctx context.Context, d rest.Doc, deleted bool) error {
	if err := p.authorize(ctx); err != nil {
		return err
	}
	id, ok := d["_id"].(uuid.UUID)
	if !ok {
		return rest.Abort(422, "invalid article ID")
	}
	title, titleOK := d["title"].(string)
	body, bodyOK := d["body"].(string)
	// Projection is explicit: private notes, transport metadata and other fields
	// are never serialized into retrieval text or sent to the embedding provider.
	readers := []string{}
	switch list := d["readers"].(type) {
	case []any:
		for _, v := range list {
			s, ok := v.(string)
			if !ok {
				return rest.Abort(422, "invalid readers")
			}
			readers = append(readers, s)
		}
	case []string:
		readers = append(readers, list...)
	default:
		if !deleted {
			return rest.Abort(422, "readers required")
		}
	}
	if !deleted && (!titleOK || !bodyOK || strings.TrimSpace(title) == "" || strings.TrimSpace(body) == "") {
		return rest.Abort(422, "article title and body required")
	}
	text := title + "\n\n" + body
	raw, _ := json.Marshal([]any{title, body, readers, deleted})
	revision := fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	document := knowledge.Document{ID: "articles/" + id.String(), Source: "articles/" + id.String(), Revision: revision, Subjects: readers, Text: text}
	q := postgres.NewStore(p.config.Pool, "hyrule_knowledge_documents").Querier(ctx)
	var expected int64
	err := q.QueryRow(ctx, `SELECT version FROM hyrule_knowledge_documents WHERE scope=$1 AND document_id=$2`, p.config.Scope, document.ID).Scan(&expected)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = p.source.QueueSource(ctx, p.config.Scope, document, deleted, expected)
	if errors.Is(err, knowledge.ErrVersion) {
		return rest.Abort(412, "article source changed")
	}
	return err
}

// Apply authenticates before decoding input or parsing an ID. Curators manage
// the entire configured article corpus; readers cannot mutate it. No caller can
// opt out of validation or ETag checks through this entry point.
func (p *Producer) Apply(ctx context.Context, operation, id, etag string, raw []byte) (rest.Doc, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := p.authorize(ctx); err != nil {
		return nil, err
	}
	if operation != "create" && operation != "patch" && operation != "replace" && operation != "delete" {
		return nil, rest.Abort(400, "unsupported article operation")
	}
	var lookup any
	opts := rest.WriteOptions{}
	if operation != "create" {
		parsed, ok := p.service.Resource().Adapter.LookupByID(id)
		if !ok {
			return nil, rest.Abort(400, "invalid article ID")
		}
		var err error
		lookup, err = p.service.Resource().Adapter.IDFilter(parsed)
		if err != nil {
			return nil, err
		}
		match, valid := rest.ParseIfMatch([]string{etag})
		if !valid {
			return nil, rest.Abort(400, "invalid If-Match")
		}
		if match.IsZero() || match.Any {
			return nil, rest.Abort(428, "specific If-Match required")
		}
		opts.IfMatch = match
	}
	if operation == "delete" {
		if len(raw) != 0 {
			return nil, rest.Abort(400, "delete accepts no document")
		}
		return nil, p.service.Delete(ctx, lookup, opts)
	}
	doc, err := parse(raw)
	if err != nil {
		return nil, rest.Abort(400, "invalid article input")
	}
	switch operation {
	case "create":
		docs, err := p.service.Insert(ctx, []rest.Doc{doc}, opts)
		if err != nil {
			return nil, err
		}
		return docs[0], nil
	case "patch":
		return p.service.Patch(ctx, lookup, doc, opts)
	default:
		return p.service.Replace(ctx, lookup, doc, opts)
	}
}
func parse(raw []byte) (rest.Doc, error) {
	if len(raw) == 0 || len(raw) > MaxInput {
		return nil, errors.New("input limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("object required")
	}
	doc := rest.Doc{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := key.(string)
		if !ok {
			return nil, errors.New("invalid key")
		}
		if _, exists := doc[name]; exists {
			return nil, errors.New("duplicate key")
		}
		switch name {
		case "title", "body", "readers", "private_notes":
		default:
			return nil, errors.New("unknown key")
		}
		var value any
		if err = decoder.Decode(&value); err != nil {
			return nil, err
		}
		doc[name] = value
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, errors.New("invalid object")
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing input")
	}
	return doc, nil
}
