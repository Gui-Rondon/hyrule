package postgres

import "testing"

func TestKindSQLCast(t *testing.T) {
	cases := []struct {
		kind Kind
		want string
	}{
		{KindString, "text"},
		{KindInteger, "int8"},
		{KindNumber, "numeric"},
		{KindBoolean, "boolean"},
		{KindDateTime, "timestamptz"},
		{KindArray, "text"},
		{KindObject, "text"},
	}
	for _, c := range cases {
		if got := c.kind.sqlCast(); got != c.want {
			t.Errorf("Kind(%d).sqlCast() = %q, want %q", c.kind, got, c.want)
		}
	}
}

func TestSchemaSpecKindDefault(t *testing.T) {
	spec := SchemaSpec{Fields: map[string]Kind{"views": KindInteger}}
	if got := spec.kind("views"); got != KindInteger {
		t.Errorf("kind(declared) = %v, want KindInteger", got)
	}
	if got := spec.kind("unknown_field"); got != KindString {
		t.Errorf("kind(undeclared) = %v, want KindString (the cast-agnostic default)", got)
	}
}

func TestSchemaSpecKindNilFields(t *testing.T) {
	var spec SchemaSpec // Fields is nil
	if got := spec.kind("anything"); got != KindString {
		t.Errorf("kind on zero-value SchemaSpec = %v, want KindString", got)
	}
}
