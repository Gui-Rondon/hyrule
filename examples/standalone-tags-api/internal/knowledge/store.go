// Package knowledge implements an optional document retrieval pilot. Store is
// deliberately independent of MCP and of a particular vector database.
package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Access struct{ Scope, Subject string }
type Document struct {
	ID       string   `json:"id"`
	Source   string   `json:"source"`
	Revision string   `json:"revision"`
	Subjects []string `json:"subjects"`
	Text     string   `json:"-"`
}
type Chunk struct {
	ID         string  `json:"id"`
	DocumentID string  `json:"document_id"`
	Source     string  `json:"source"`
	Revision   string  `json:"revision"`
	Text       string  `json:"text"`
	Distance   float64 `json:"distance"`
}
type EmbeddedChunk struct {
	Chunk  Chunk
	Vector []float32
}

// Replace atomically replaces one document including its ACL and removes stale
// chunks. Search and Fetch must apply Access before returning content.
type Store interface {
	Replace(context.Context, string, Document, []EmbeddedChunk) error
	Search(context.Context, Access, []float32, int) ([]Chunk, error)
	Fetch(context.Context, Access, string) (Chunk, error)
}
type Embedder interface {
	Embed(context.Context, string) ([]float32, error)
}

// QueryEmbedder optionally supplies a query-specific task prompt. Providers
// without asymmetric prompts keep using Embed for both documents and queries.
type QueryEmbedder interface {
	EmbedQuery(context.Context, string) ([]float32, error)
}

var ErrNotFound = errors.New("document not found")

// Ingest embeds a complete bounded document before replacing any stored data.
func Ingest(ctx context.Context, s Store, e Embedder, scope string, d Document) error {
	if s == nil || e == nil || scope == "" || d.ID == "" || len(d.ID) > 256 || d.Source == "" || len(d.Source) > 1024 || d.Revision == "" || len(d.Revision) > 256 || len(d.Subjects) == 0 || len(d.Subjects) > 100 || len(d.Text) == 0 || len(d.Text) > 1024*1024 || !utf8.ValidString(d.Text) {
		return errors.New("invalid document")
	}
	for _, subject := range d.Subjects {
		if strings.TrimSpace(subject) == "" || len(subject) > 256 {
			return errors.New("invalid document ACL")
		}
	}
	chunks, err := embedDocument(ctx, e, d)
	if err != nil {
		return err
	}
	return s.Replace(ctx, scope, d, chunks)
}

func embedDocument(ctx context.Context, e Embedder, d Document) ([]EmbeddedChunk, error) {
	runes := []rune(d.Text)
	chunks := make([]EmbeddedChunk, 0, (len(runes)+1199)/1200)
	for start := 0; start < len(runes); start += 1200 {
		end := min(start+1200, len(runes))
		content := string(runes[start:end])
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s", d.ID, start, content)))
		vector, err := e.Embed(ctx, content)
		if err != nil {
			return nil, err
		}
		if _, err = vectorLiteral(vector); err != nil {
			return nil, err
		}
		chunks = append(chunks, EmbeddedChunk{Chunk: Chunk{ID: hex.EncodeToString(sum[:]), DocumentID: d.ID, Source: d.Source, Revision: d.Revision, Text: content}, Vector: vector})
	}
	return chunks, nil
}

type Postgres struct {
	Pool       *pgxpool.Pool
	Model      string
	Dimensions int
}

// Init is explicit provisioning, never executed by the read server. The database
// administrator must install CREATE EXTENSION vector beforehand.
func (p *Postgres) Init(ctx context.Context) error {
	return p.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('hyrule:knowledge-schema-v2',0))`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS hyrule_knowledge_documents (
 scope text NOT NULL, document_id text NOT NULL, version bigint NOT NULL CHECK(version>0),
 source text NOT NULL, revision text NOT NULL, subjects text[] NOT NULL,
 content text NOT NULL, deleted boolean NOT NULL, digest text NOT NULL,
 indexed_version bigint NOT NULL DEFAULT 0, indexed_model text NOT NULL DEFAULT '',
 PRIMARY KEY(scope,document_id));
 CREATE TABLE IF NOT EXISTS hyrule_knowledge_chunks (
 scope text NOT NULL, id text NOT NULL, document_id text NOT NULL,
 source text NOT NULL, revision text NOT NULL, subjects text[] NOT NULL CHECK(cardinality(subjects)>0),
 model text NOT NULL, content text NOT NULL, embedding vector NOT NULL,
 source_version bigint NOT NULL DEFAULT 0,
 PRIMARY KEY(scope,id));
 ALTER TABLE hyrule_knowledge_chunks ADD COLUMN IF NOT EXISTS source_version bigint NOT NULL DEFAULT 0;
 CREATE INDEX IF NOT EXISTS hyrule_knowledge_document ON hyrule_knowledge_chunks(scope,document_id)`)

		return err
	})
}
func (p *Postgres) vector(v []float32) (string, error) {
	if p.Model == "" || p.Dimensions < 1 || len(v) != p.Dimensions {
		return "", errors.New("embedding model or dimension mismatch")
	}
	return vectorLiteral(v)
}
func vectorLiteral(v []float32) (string, error) {
	if len(v) == 0 || len(v) > 4096 {
		return "", errors.New("invalid embedding dimensions")
	}
	parts := make([]string, len(v))
	nonzero := false
	for i, x := range v {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return "", errors.New("invalid embedding")
		}
		if x != 0 {
			nonzero = true
		}
		parts[i] = strconv.FormatFloat(float64(x), 'g', -1, 32)
	}
	if !nonzero {
		return "", errors.New("zero embedding")
	}
	return "[" + strings.Join(parts, ",") + "]", nil
}

// Replace synchronously publishes a trusted snapshot and its chunks atomically.
// QueueSource is the asynchronous path; both share the same source/version lock.
func (p *Postgres) Replace(ctx context.Context, scope string, d Document, chunks []EmbeddedChunk) error {
	if scope == "" || d.ID == "" || len(d.Subjects) == 0 || len(chunks) == 0 {
		return errors.New("invalid replacement")
	}
	if d.Text == "" {
		for _, c := range chunks {
			d.Text += c.Chunk.Text
		}
	}
	return p.transaction(ctx, func(tx pgx.Tx) error {
		if err := lockDocument(ctx, tx, scope, d.ID); err != nil {
			return err
		}
		var version int64
		var digest string
		err := tx.QueryRow(ctx, `SELECT version,digest FROM hyrule_knowledge_documents WHERE scope=$1 AND document_id=$2`, scope, d.ID).Scan(&version, &digest)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if version == 0 || digest != digestDocument(d, false) {
			version, err = writeSource(ctx, tx, scope, d, false)
			if err != nil {
				return err
			}
		}
		return p.replaceChunks(ctx, tx, scope, d, version, chunks)
	})
}
func (p *Postgres) Search(ctx context.Context, a Access, v []float32, limit int) ([]Chunk, error) {
	if a.Scope == "" || a.Subject == "" || limit < 1 || limit > 5 {
		return nil, errors.New("invalid search")
	}
	literal, err := p.vector(v)
	if err != nil {
		return nil, err
	}
	// Exact cosine search: filter ACL before LIMIT; no approximate index recall loss.
	rows, err := p.Pool.Query(ctx, `SELECT c.id,c.document_id,c.source,c.revision,c.content,c.embedding <=> $1::vector
 FROM hyrule_knowledge_chunks c JOIN hyrule_knowledge_documents d ON d.scope=c.scope AND d.document_id=c.document_id
 WHERE c.scope=$2 AND $3=ANY(d.subjects) AND NOT d.deleted AND d.version=c.source_version AND d.indexed_version=d.version AND d.indexed_model=c.model
 AND c.model=$4 AND vector_dims(c.embedding)=$5 ORDER BY c.embedding <=> $1::vector,c.id LIMIT $6`, literal, a.Scope, a.Subject, p.Model, p.Dimensions, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Chunk{}
	for rows.Next() {
		var c Chunk
		if err = rows.Scan(&c.ID, &c.DocumentID, &c.Source, &c.Revision, &c.Text, &c.Distance); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func (p *Postgres) Fetch(ctx context.Context, a Access, id string) (Chunk, error) {
	if a.Scope == "" || a.Subject == "" || len(id) != 64 {
		return Chunk{}, ErrNotFound
	}
	var c Chunk
	err := p.Pool.QueryRow(ctx, `SELECT c.id,c.document_id,c.source,c.revision,c.content
 FROM hyrule_knowledge_chunks c JOIN hyrule_knowledge_documents d ON d.scope=c.scope AND d.document_id=c.document_id
 WHERE c.scope=$1 AND $2=ANY(d.subjects) AND NOT d.deleted AND d.version=c.source_version AND d.indexed_version=d.version AND d.indexed_model=c.model
 AND c.id=$3 AND c.model=$4 AND vector_dims(c.embedding)=$5`, a.Scope, a.Subject, id, p.Model, p.Dimensions).Scan(&c.ID, &c.DocumentID, &c.Source, &c.Revision, &c.Text)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Chunk{}, ErrNotFound
		}
		return Chunk{}, err
	}
	return c, nil
}
