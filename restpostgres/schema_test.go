package restpostgres

import (
	"testing"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
)

func TestToSchemaSpecMapsEveryFieldType(t *testing.T) {
	schema := rest.Schema{
		"name":    rest.Field{Type: rest.TypeString},
		"active":  rest.Field{Type: rest.TypeBoolean},
		"views":   rest.Field{Type: rest.TypeInteger},
		"rating":  rest.Field{Type: rest.TypeNumber},
		"created": rest.Field{Type: rest.TypeDatetime},
		"ref":     rest.Field{Type: rest.TypeObjectID},
		"meta":    rest.Field{Type: rest.TypeDict},
		"tags":    rest.Field{Type: rest.TypeList},
		"untyped": rest.Field{}, // empty type accepts any value
	}
	spec, err := ToSchemaSpec(schema)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]postgres.Kind{
		"name":    postgres.KindString,
		"active":  postgres.KindBoolean,
		"views":   postgres.KindInteger,
		"rating":  postgres.KindNumber,
		"created": postgres.KindDateTime,
		"ref":     postgres.KindString,
		"meta":    postgres.KindObject,
		"tags":    postgres.KindArray,
		"untyped": postgres.KindString,
	}
	for field, kind := range want {
		if got := spec.FieldKind(field); got != kind {
			t.Errorf("FieldKind(%q) = %v, want %v", field, got, kind)
		}
	}
}

func TestToSchemaSpecRejectsUnsupportedType(t *testing.T) {
	schema := rest.Schema{"bad": rest.Field{Type: "nonsense"}}
	if _, err := ToSchemaSpec(schema); err == nil {
		t.Fatal("expected an error for an unsupported field type")
	}
}

func TestToSchemaSpecListItemsBecomeElementKind(t *testing.T) {
	schema := rest.Schema{
		"scores": rest.Field{Type: rest.TypeList, Items: &rest.Field{Type: rest.TypeInteger}},
		"tags":   rest.Field{Type: rest.TypeList}, // no Items declared
	}
	spec, err := ToSchemaSpec(schema)
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.ElementKind("scores"); got != postgres.KindInteger {
		t.Errorf("ElementKind(scores) = %v, want KindInteger", got)
	}
	if got := spec.ElementKind("tags"); got != postgres.KindString {
		t.Errorf("ElementKind(tags) = %v, want KindString (default, no Items declared)", got)
	}
}

func TestToSchemaSpecRejectsUnsupportedListItemsType(t *testing.T) {
	schema := rest.Schema{"scores": rest.Field{Type: rest.TypeList, Items: &rest.Field{Type: "nonsense"}}}
	if _, err := ToSchemaSpec(schema); err == nil {
		t.Fatal("expected an error for an unsupported Items field type")
	}
}
