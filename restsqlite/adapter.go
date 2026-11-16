// Package restsqlite implements an experimental local SQLite rest.Adapter.
// It supports scalar queries; outbox, soft delete and dry run are unsupported.
package restsqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Gui-Rondon/hyrule/platform/sqlite"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/google/uuid"
	driver "modernc.org/sqlite"
)

type adapter struct {
	tx     sqlite.Transactor
	table  string
	schema rest.Schema
}

var _ rest.Adapter = (*adapter)(nil)

// NewAdapter creates a resource table and single-field unique indexes. Call
// before serving; table names must be SQL identifiers. Schema changes require
// explicit migrations; CREATE IF NOT EXISTS does not migrate existing data.
func NewAdapter(ctx context.Context, db *sql.DB, table string, res *rest.Resource) (rest.Adapter, error) {
	if res == nil {
		return nil, errors.New("restsqlite: nil resource")
	}
	if res.SoftDelete {
		return nil, errors.New("restsqlite: soft delete is unsupported")
	}
	schema := res.Schema
	if db == nil {
		return nil, errors.New("restsqlite: nil database")
	}
	if !fieldName.MatchString(table) {
		return nil, fmt.Errorf("restsqlite: invalid table %q", table)
	}
	for name, field := range schema {
		if _, err := fieldSQL(name); err != nil {
			return nil, err
		}
		if strings.HasPrefix(name, "_") {
			return nil, fmt.Errorf("restsqlite: reserved schema field %q", name)
		}
		if field.Unique && (field.Type == rest.TypeDict || field.Type == rest.TypeList) {
			return nil, fmt.Errorf("restsqlite: unique indexes require scalar fields")
		}
	}
	a := &adapter{sqlite.Transactor{DB: db}, `"` + table + `"`, schema}
	err := a.tx.WithTransaction(ctx, func(ctx context.Context) error {
		q := a.tx.Querier(ctx)
		if _, err := q.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+a.table+` (id TEXT NOT NULL PRIMARY KEY, version INTEGER NOT NULL, doc TEXT NOT NULL CHECK(json_valid(doc)))`); err != nil {
			return err
		}
		for name, field := range schema {
			if !field.Unique {
				continue
			}
			expr, _ := fieldSQL(name)
			if _, err := q.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS "`+fmt.Sprintf("hyrule_unique_%x", sha256.Sum256([]byte(table+"\x00"+name)))+`" ON `+a.table+` (`+expr+`)`); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}
func (a *adapter) NewID() any             { return uuid.NewString() }
func (a *adapter) IDString(id any) string { return fmt.Sprint(id) }
func (a *adapter) LookupByID(value string) (any, bool) {
	id, err := uuid.Parse(value)
	return id.String(), err == nil
}
func (a *adapter) LookupID(id any) any            { return id }
func (a *adapter) RetryPolicy() rest.RetryOptions { return rest.RetryOptions{} }
func (a *adapter) ValidatesMergedDocument() bool  { return true }
func (a *adapter) IsNotFound(err error) bool      { return errors.Is(err, sql.ErrNoRows) }
func (a *adapter) IsDuplicateKey(err error) bool {
	var e *driver.Error
	return errors.As(err, &e) && (e.Code() == 2067 || e.Code() == 1555)
}
func (a *adapter) IsClientError(err error) bool { var e *queryError; return errors.As(err, &e) }
func (a *adapter) IsTimeout(err error) bool {
	var e *driver.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || (errors.As(err, &e) && (e.Code()&255 == 5 || e.Code()&255 == 6))
}
func (a *adapter) ClassifiesWriteTimeouts() bool { return true }

func (a *adapter) Find(ctx context.Context, value, sortValue, proj any, skip, limit int64) ([]rest.Doc, error) {
	f, err := compile(value)
	if err != nil {
		return nil, err
	}
	query := `SELECT doc,version FROM ` + a.table + ` WHERE ` + f.sql
	if sortValue != nil {
		terms, ok := sortValue.([]sortTerm)
		if !ok {
			return nil, invalid("invalid sort type")
		}
		for i, t := range terms {
			expr, err := fieldSQL(t.field)
			if err != nil {
				return nil, err
			}
			if i == 0 {
				query += " ORDER BY "
			} else {
				query += ","
			}
			query += expr
			if t.desc {
				query += " DESC"
			}
		}
	}
	if limit > 0 {
		query += " LIMIT ? OFFSET ?"
		f.args = append(f.args, limit, skip)
	}
	p, ok := proj.(projection)
	if proj != nil && !ok {
		return nil, invalid("invalid projection type")
	}
	rows, err := a.tx.Querier(ctx).QueryContext(ctx, query, f.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	docs := []rest.Doc{}
	for rows.Next() {
		var raw string
		var version int64
		if err := rows.Scan(&raw, &version); err != nil {
			return nil, err
		}
		doc, err := a.decode(raw, version)
		if err != nil {
			return nil, err
		}
		docs = append(docs, project(doc, p))
	}
	return docs, rows.Err()
}
func (a *adapter) CountDocuments(ctx context.Context, value any) (int64, error) {
	f, err := compile(value)
	if err != nil {
		return 0, err
	}
	var n int64
	err = a.tx.Querier(ctx).QueryRowContext(ctx, `SELECT count(*) FROM `+a.table+` WHERE `+f.sql, f.args...).Scan(&n)
	return n, err
}
func (a *adapter) FindOne(ctx context.Context, value, proj any) (rest.Doc, error) {
	docs, err := a.Find(ctx, value, nil, proj, 0, 1)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, sql.ErrNoRows
	}
	return docs[0], nil
}
func (a *adapter) InsertMany(ctx context.Context, docs []rest.Doc) error {
	return a.tx.WithTransaction(ctx, func(ctx context.Context) error {
		for _, doc := range docs {
			doc["_etag"] = "1"
			raw, err := json.Marshal(doc)
			if err != nil {
				return err
			}
			if _, err := a.tx.Querier(ctx).ExecContext(ctx, `INSERT INTO `+a.table+` (id,version,doc) VALUES (?,1,?)`, doc["_id"], string(raw)); err != nil {
				return err
			}
		}
		return nil
	})
}
func (a *adapter) UpdateOne(ctx context.Context, lookup any, doc, changes rest.Doc) (int64, error) {
	return a.ReplaceOne(ctx, lookup, doc)
}
func (a *adapter) ReplaceOne(ctx context.Context, lookup any, doc rest.Doc) (int64, error) {
	f, err := compile(lookup)
	if err != nil {
		return 0, err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return 0, err
	}
	var v int64
	err = a.tx.Querier(ctx).QueryRowContext(ctx, `UPDATE `+a.table+` SET doc=?,version=version+1 WHERE `+f.sql+` RETURNING version`, append([]any{string(raw)}, f.args...)...).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	doc["_etag"] = strconv.FormatInt(v, 10)
	return 1, nil
}
func (a *adapter) DeleteOne(ctx context.Context, lookup any) (int64, error) {
	f, err := compile(lookup)
	if err != nil {
		return 0, err
	}
	result, err := a.tx.Querier(ctx).ExecContext(ctx, `DELETE FROM `+a.table+` WHERE `+f.sql, f.args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
func (a *adapter) decode(raw string, version int64) (rest.Doc, error) {
	var doc rest.Doc
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	doc["_etag"] = strconv.FormatInt(version, 10)
	for _, key := range []string{"_created", "_updated"} {
		if value, ok := doc[key].(string); ok {
			t, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return nil, err
			}
			doc[key] = t
		}
	}
	for key, field := range a.schema {
		if field.Type == rest.TypeDatetime {
			if value, ok := doc[key].(string); ok {
				t, err := time.Parse(time.RFC3339Nano, value)
				if err != nil {
					return nil, err
				}
				doc[key] = t
			}
		}
	}
	return doc, nil
}

// MergePatch applies RFC 7396, copying JSON objects rather than aliasing inputs.
func (a *adapter) MergePatch(original, changes rest.Doc) (rest.Doc, error) {
	out := merge(original, changes)
	for key, value := range original {
		if strings.HasPrefix(key, "_") {
			out[key] = value
		}
	}
	return out, nil
}
func merge(target, patch map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range target {
		if object, ok := v.(map[string]any); ok {
			out[k] = merge(object, nil)
		} else {
			out[k] = cloneJSON(v)
		}
	}
	for k, v := range patch {
		if v == nil {
			delete(out, k)
			continue
		}
		if object, ok := v.(map[string]any); ok {
			old, _ := out[k].(map[string]any)
			out[k] = merge(old, object)
		} else {
			out[k] = cloneJSON(v)
		}
	}
	return out
}

func cloneJSON(v any) any {
	switch value := v.(type) {
	case map[string]any:
		return merge(value, nil)
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = cloneJSON(item)
		}
		return out
	default:
		return v
	}
}
