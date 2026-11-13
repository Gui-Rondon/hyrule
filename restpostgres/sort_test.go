package restpostgres

import (
	"reflect"
	"testing"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
)

func TestOrderByClauseEmpty(t *testing.T) {
	a := &postgresAdapter{}
	clause, args := a.orderByClause(nil, 0)
	if clause != "" || len(args) != 0 {
		t.Fatalf("orderByClause(nil) = %q, %v", clause, args)
	}
}

func TestOrderByClauseMetaField(t *testing.T) {
	a := &postgresAdapter{}
	clause, args := a.orderByClause([]SortTerm{{Field: "_created", Desc: true}}, 0)
	if clause != " ORDER BY created_at DESC" || len(args) != 0 {
		t.Fatalf("orderByClause(_created) = %q, %v", clause, args)
	}
}

func TestOrderByClauseDocField(t *testing.T) {
	a := &postgresAdapter{schema: postgres.SchemaSpec{Fields: map[string]postgres.Kind{"views": postgres.KindInteger}}}
	clause, args := a.orderByClause([]SortTerm{{Field: "views"}}, 0)
	if want := " ORDER BY (doc->>$1)::int8 ASC"; clause != want {
		t.Fatalf("orderByClause(views) = %q, want %q", clause, want)
	}
	if !reflect.DeepEqual(args, []any{"views"}) {
		t.Fatalf("args = %v", args)
	}
}

func TestOrderByClauseStartAtOffsetsPlaceholders(t *testing.T) {
	a := &postgresAdapter{}
	clause, args := a.orderByClause([]SortTerm{{Field: "name"}}, 2)
	if want := " ORDER BY (doc->>$3)::text ASC"; clause != want {
		t.Fatalf("orderByClause = %q, want %q", clause, want)
	}
	if !reflect.DeepEqual(args, []any{"name"}) {
		t.Fatalf("args = %v", args)
	}
}

func TestMetaColumn(t *testing.T) {
	cases := []struct {
		field  string
		column string
		ok     bool
	}{
		{"_id", "id", true},
		{"_etag", "version", true},
		{"_created", "created_at", true},
		{"_updated", "updated_at", true},
		{"_deleted", "deleted", true},
		{"name", "", false},
	}
	for _, c := range cases {
		column, ok := metaColumn(c.field)
		if column != c.column || ok != c.ok {
			t.Errorf("metaColumn(%q) = %q, %v; want %q, %v", c.field, column, ok, c.column, c.ok)
		}
	}
}

func TestOrderByClauseMultipleTerms(t *testing.T) {
	a := &postgresAdapter{}
	clause, args := a.orderByClause([]SortTerm{{Field: "_updated", Desc: true}, {Field: "name"}}, 0)
	if want := " ORDER BY updated_at DESC, (doc->>$1)::text ASC"; clause != want {
		t.Fatalf("orderByClause = %q, want %q", clause, want)
	}
	if !reflect.DeepEqual(args, []any{"name"}) {
		t.Fatalf("args = %v", args)
	}
}
