package restpostgres

import (
	"fmt"
	"strings"
)

// SortTerm is one ORDER BY term for Find. Field is either a meta field
// (_id/_etag/_created/_updated/_deleted, mapped to its physical
// column) or a jsonb doc field, cast using the field's declared Kind
// -- the same cast CompileFilter would use for it.
type SortTerm struct {
	Field string
	Desc  bool
}

// metaColumn maps a Doc meta field to its physical column, or reports
// ok=false for a jsonb doc field.
func metaColumn(field string) (column string, ok bool) {
	switch field {
	case "_id":
		return "id", true
	case "_etag":
		return "version", true
	case "_created":
		return "created_at", true
	case "_updated":
		return "updated_at", true
	case "_deleted":
		return "deleted", true
	}
	return "", false
}

// orderByClause renders sort as an " ORDER BY ..." clause (empty string
// for no sort), with placeholder numbers starting after startAt
// existing arguments. A jsonb doc field is bound as a parameter (doc
// ->>$N), never concatenated into the SQL text, and cast using the
// field's declared Kind.
func (a *postgresAdapter) orderByClause(sort []SortTerm, startAt int) (string, []any) {
	if len(sort) == 0 {
		return "", nil
	}
	terms := make([]string, len(sort))
	args := make([]any, 0, len(sort))
	n := startAt
	for i, s := range sort {
		var expr string
		if column, ok := metaColumn(s.Field); ok {
			expr = column
		} else {
			n++
			args = append(args, s.Field)
			expr = fmt.Sprintf("(doc->>$%d)::%s", n, a.schema.FieldKind(s.Field).SQLCast())
		}
		dir := "ASC"
		if s.Desc {
			dir = "DESC"
		}
		terms[i] = expr + " " + dir
	}
	return " ORDER BY " + strings.Join(terms, ", "), args
}
