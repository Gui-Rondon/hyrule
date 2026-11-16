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

func TestOrderByClauseNestedPath(t *testing.T) {
	a := &postgresAdapter{}
	clause, args := a.orderByClause([]SortTerm{{Field: "meta.author", Desc: true}}, 0)
	if want := " ORDER BY doc #>> $1::text[] DESC"; clause != want {
		t.Fatalf("orderByClause(meta.author) = %q, want %q", clause, want)
	}
	if len(args) != 1 || !reflect.DeepEqual(args[0], []string{"meta", "author"}) {
		t.Fatalf("args = %v", args)
	}
}

func TestToSortTermsRejectsWrongType(t *testing.T) {
	if _, err := toSortTerms("not a []SortTerm"); err == nil {
		t.Fatal("expected an error for a sort value that isn't a []SortTerm")
	}
	terms, err := toSortTerms(nil)
	if err != nil || terms != nil {
		t.Fatalf("toSortTerms(nil) = %v, %v, want nil, nil", terms, err)
	}
}

func TestOrderByIDQualifiesSanitizedTable(t *testing.T) {
	a := &postgresAdapter{table: `"odd""table"`}
	clause, args := a.orderByClause([]SortTerm{{Field: "_id", Desc: true}, {Field: "name"}}, 2)
	if want := ` ORDER BY "odd""table".id DESC, (doc->>$3)::text ASC`; clause != want {
		t.Fatalf("orderByClause = %q, want %q", clause, want)
	}
	if !reflect.DeepEqual(args, []any{"name"}) {
		t.Fatalf("args = %v", args)
	}
}

func TestOrderByDateTimeSortIsExplicit(t *testing.T) {
	for _, safe := range []bool{false, true} {
		a := &postgresAdapter{table: `"docs"`, safeDateTimeSort: safe, schema: postgres.SchemaSpec{Fields: map[string]postgres.Kind{"when": postgres.KindDateTime, "odd'\\雪": postgres.KindDateTime, "nested.when": postgres.KindDateTime}}}
		terms := []SortTerm{{Field: "when", Desc: true}, {Field: "odd'\\雪"}, {Field: "nested.when"}, {Field: "_id"}, {Field: "name"}}
		got, args := a.orderByClause(terms, 2)
		want := ` ORDER BY (doc->>$3)::timestamptz DESC, (doc->>$4)::timestamptz ASC, doc #>> $5::text[] ASC, "docs".id ASC, (doc->>$6)::text ASC`
		if safe {
			want = ` ORDER BY hyrule_safe_timestamptz(doc->>$3) DESC, hyrule_safe_timestamptz(doc->>$4) ASC, doc #>> $5::text[] ASC, "docs".id ASC, (doc->>$6)::text ASC`
		}
		if got != want {
			t.Fatalf("safe=%t: %s; want %s", safe, got, want)
		}
		if !reflect.DeepEqual(args, []any{"when", "odd'\\雪", []string{"nested", "when"}, "name"}) {
			t.Fatalf("safe=%t args=%v", safe, args)
		}
	}
}
