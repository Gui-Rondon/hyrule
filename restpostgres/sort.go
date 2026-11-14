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

// toSortTerms normalizes Find's sort parameter: nil means no sort
// (empty result, not an error); any non-nil value must be a
// []SortTerm. A wrong type errors loudly here instead of silently
// sorting as if sort had never been given -- the same failure
// discipline compileFilter/lookupWhere already apply to filter/lookup.
func toSortTerms(sort any) ([]SortTerm, error) {
	if sort == nil {
		return nil, nil
	}
	terms, ok := sort.([]SortTerm)
	if !ok {
		return nil, fmt.Errorf("restpostgres: sort must be a []SortTerm, got %T", sort)
	}
	return terms, nil
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
// field's declared Kind. A dotted field ("a.b") addresses a nested
// path, sorted as text -- the same restriction CompileFilter's
// compileNestedField has (nested paths never use a declared Kind).
func (a *postgresAdapter) orderByClause(sort []SortTerm, startAt int) (string, []any) {
	if len(sort) == 0 {
		return "", nil
	}
	terms := make([]string, len(sort))
	args := make([]any, 0, len(sort))
	n := startAt
	for i, s := range sort {
		var expr string
		switch column, isMeta := metaColumn(s.Field); {
		case isMeta:
			expr = column
		case strings.Contains(s.Field, "."):
			n++
			args = append(args, strings.Split(s.Field, "."))
			expr = fmt.Sprintf("doc #>> $%d::text[]", n)
		default:
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
