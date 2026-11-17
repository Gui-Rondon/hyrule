package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gui-Rondon/hyrule/outbox"
	"github.com/Gui-Rondon/hyrule/outboxpostgres"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/jackc/pgx/v5"
)

const syncTopic = "local:knowledge-reindex-v1"

var ErrVersion = errors.New("knowledge source version conflict")
var ErrSync = errors.New("knowledge synchronization unavailable")

type Source struct {
	Document                Document
	Version, IndexedVersion int64
	Deleted                 bool
	IndexedModel            string
}
type change struct {
	Scope   string `json:"scope"`
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

// SyncOutbox isolates the relay by corpus; its events contain references only.
func (p *Postgres) SyncOutbox(scope string) *outboxpostgres.Outbox {
	sum := sha256.Sum256([]byte(scope))
	return &outboxpostgres.Outbox{Pool: p.Pool, Table: "hyrule_knowledge_outbox_" + hex.EncodeToString(sum[:12])}
}
func (p *Postgres) InitSync(ctx context.Context, scope string) error {
	if strings.TrimSpace(scope) == "" {
		return errors.New("corpus scope required")
	}
	return outboxpostgres.EnsureTable(ctx, p.Pool, p.SyncOutbox(scope).Table)
}
func (p *Postgres) transaction(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func lockDocument(ctx context.Context, q postgres.Querier, scope, id string) error {
	_, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fmt.Sprintf("%d:%s%s", len(scope), scope, id))
	return err
}
func digestDocument(d Document, deleted bool) string {
	subjects := slices.Clone(d.Subjects)
	slices.Sort(subjects)
	subjects = slices.Compact(subjects)
	raw, _ := json.Marshal([]any{d.Source, d.Revision, subjects, d.Text, deleted})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func writeSource(ctx context.Context, q postgres.Querier, scope string, d Document, deleted bool) (int64, error) {
	var version int64
	err := q.QueryRow(ctx, `INSERT INTO hyrule_knowledge_documents(scope,document_id,version,source,revision,subjects,content,deleted,digest)
 VALUES($1,$2,1,$3,$4,$5,$6,$7,$8) ON CONFLICT(scope,document_id) DO UPDATE SET
 version=hyrule_knowledge_documents.version+1,source=excluded.source,revision=excluded.revision,
 subjects=excluded.subjects,content=excluded.content,deleted=excluded.deleted,digest=excluded.digest
 RETURNING version`, scope, d.ID, d.Source, d.Revision, d.Subjects, d.Text, deleted, digestDocument(d, deleted)).Scan(&version)
	return version, err
}
func (p *Postgres) LoadSource(ctx context.Context, scope, id string) (Source, error) {
	var s Source
	s.Document.ID = id
	err := p.Pool.QueryRow(ctx, `SELECT version,source,revision,subjects,content,deleted,indexed_version,indexed_model FROM hyrule_knowledge_documents WHERE scope=$1 AND document_id=$2`, scope, id).Scan(&s.Version, &s.Document.Source, &s.Document.Revision, &s.Document.Subjects, &s.Document.Text, &s.Deleted, &s.IndexedVersion, &s.IndexedModel)
	if errors.Is(err, pgx.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	return s, err
}

// QueueSource must run inside the caller's Hyrule Postgres transaction. It
// changes source/ACL and adds an outbox event in that same transaction. Expected
// is a numeric source version (zero for creation), never the opaque Git revision.
// Empty ACL denies everyone. Deleted records remain as versioned tombstones.
func (p *Postgres) QueueSource(ctx context.Context, scope string, d Document, deleted bool, expected int64) (int64, error) {
	if !postgres.InTransaction(ctx, p.Pool) {
		return 0, outbox.ErrNoTransaction
	}
	if strings.TrimSpace(scope) == "" || d.ID == "" || len(d.ID) > 256 || len(d.Revision) == 0 || len(d.Revision) > 256 || len(d.Source) > 1024 || expected < 0 || len(d.Subjects) > 100 {
		return 0, errors.New("invalid source")
	}
	if deleted {
		d.Text = ""
		d.Subjects = []string{}
	}
	if d.Subjects == nil {
		d.Subjects = []string{}
	}
	if !deleted && (d.Source == "" || len(d.Source) > 1024 || d.Text == "" || len(d.Text) > 1024*1024 || !utf8.ValidString(d.Text)) {
		return 0, errors.New("invalid source document")
	}
	for _, s := range d.Subjects {
		if strings.TrimSpace(s) == "" || len(s) > 256 {
			return 0, errors.New("invalid source ACL")
		}
	}
	q := postgres.NewStore(p.Pool, "hyrule_knowledge_documents").Querier(ctx)
	if err := lockDocument(ctx, q, scope, d.ID); err != nil {
		return 0, err
	}
	var version int64
	var digest string
	err := q.QueryRow(ctx, `SELECT version,digest FROM hyrule_knowledge_documents WHERE scope=$1 AND document_id=$2`, scope, d.ID).Scan(&version, &digest)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if version > 0 && expected <= version && digest == digestDocument(d, deleted) {
		return version, nil
	}
	if version != expected {
		return 0, ErrVersion
	}

	version, err = writeSource(ctx, q, scope, d, deleted)
	if err != nil {
		return 0, err
	}
	event := outbox.Event{Topic: syncTopic, Type: "knowledge.changed.v1", Key: fmt.Sprintf("%d:%s%s", len(scope), scope, d.ID), Body: change{scope, d.ID, version}}
	if err = p.SyncOutbox(scope).Add(ctx, event); err != nil {
		return 0, err
	}
	return version, nil
}
func (p *Postgres) replaceChunks(ctx context.Context, q postgres.Querier, scope string, d Document, version int64, chunks []EmbeddedChunk) error {
	if _, err := q.Exec(ctx, `DELETE FROM hyrule_knowledge_chunks WHERE scope=$1 AND document_id=$2`, scope, d.ID); err != nil {
		return err
	}
	for _, c := range chunks {
		vector, err := p.vector(c.Vector)
		if err != nil {
			return err
		}
		_, err = q.Exec(ctx, `INSERT INTO hyrule_knowledge_chunks(scope,id,document_id,source,revision,subjects,model,content,embedding,source_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::vector,$10)`, scope, c.Chunk.ID, d.ID, d.Source, d.Revision, d.Subjects, p.Model, c.Chunk.Text, vector, version)
		if err != nil {
			return err
		}
	}
	_, err := q.Exec(ctx, `UPDATE hyrule_knowledge_documents SET indexed_version=$3,indexed_model=$4 WHERE scope=$1 AND document_id=$2`, scope, d.ID, version, p.Model)
	return err
}

// Worker implements the existing outbox.Publisher interface. Embeddings run
// outside source/index transactions. Replays reread current state, never trust
// event content or ordering. Index writes compare source version under its lock.
type Worker struct {
	Store    *Postgres
	Embedder Embedder
	Scope    string
}

func (w *Worker) Publish(ctx context.Context, msg outbox.Message) error {
	if w.Store == nil || w.Embedder == nil || w.Scope == "" || msg.Topic != syncTopic || len(msg.Data) > 4096 {
		return ErrSync
	}
	var event change
	if json.Unmarshal(msg.Data, &event) != nil || event.Scope != w.Scope || event.ID == "" || event.Version < 1 {
		return ErrSync
	}
	snapshot, err := w.Store.LoadSource(ctx, event.Scope, event.ID)
	if err != nil || event.Version > snapshot.Version {
		return ErrSync
	}
	if snapshot.IndexedVersion == snapshot.Version && snapshot.IndexedModel == w.Store.Model {
		return nil
	}
	var chunks []EmbeddedChunk
	if !snapshot.Deleted && len(snapshot.Document.Subjects) > 0 {
		chunks, err = embedDocument(ctx, w.Embedder, snapshot.Document)
		if err != nil {
			return ErrSync
		}
	}
	err = w.Store.transaction(ctx, func(tx pgx.Tx) error {
		if err := lockDocument(ctx, tx, event.Scope, event.ID); err != nil {
			return err
		}
		var version, indexed int64
		var model string
		if err := tx.QueryRow(ctx, `SELECT version,indexed_version,indexed_model FROM hyrule_knowledge_documents WHERE scope=$1 AND document_id=$2`, event.Scope, event.ID).Scan(&version, &indexed, &model); err != nil {
			return err
		}
		if version != snapshot.Version || (indexed == version && model == w.Store.Model) {
			return nil
		}
		return w.Store.replaceChunks(ctx, tx, event.Scope, snapshot.Document, version, chunks)
	})
	if err != nil {
		return ErrSync
	}
	return nil
}
