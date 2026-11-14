// Package restpostgres implements a rest.Resource's storage contract
// against Postgres: the first (and, for now, only) package that
// imports both rest and platform/postgres, so neither of those needs
// to know the other exists.
package restpostgres

import (
	"fmt"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
)

// ToSchemaSpec converts schema into the neutral SchemaSpec
// platform/postgres's DSL compiler needs -- a field's declared
// rest.FieldType becomes a postgres.Kind, and a TypeList field's Items
// type (when declared) becomes its ElementKinds entry, for
// $has/$hasSome/$hasEvery.
func ToSchemaSpec(schema rest.Schema) (postgres.SchemaSpec, error) {
	spec := postgres.SchemaSpec{
		Fields:       make(map[string]postgres.Kind, len(schema)),
		ElementKinds: make(map[string]postgres.Kind),
	}
	for name, field := range schema {
		kind, err := kindForFieldType(field.Type)
		if err != nil {
			return postgres.SchemaSpec{}, fmt.Errorf("restpostgres: field %q: %w", name, err)
		}
		spec.Fields[name] = kind
		if field.Type == rest.TypeList && field.Items != nil {
			elemKind, err := kindForFieldType(field.Items.Type)
			if err != nil {
				return postgres.SchemaSpec{}, fmt.Errorf("restpostgres: field %q items: %w", name, err)
			}
			spec.ElementKinds[name] = elemKind
		}
	}
	return spec, nil
}

// kindForFieldType maps a rest.FieldType to its neutral postgres.Kind.
// TypeObjectID maps to KindString: a Postgres resource's cross-reference
// fields hold the referenced id's string form (see rest.Adapter.IDString),
// never a Mongo-specific object id.
func kindForFieldType(t rest.FieldType) (postgres.Kind, error) {
	switch t {
	case "", rest.TypeString, rest.TypeObjectID:
		return postgres.KindString, nil
	case rest.TypeBoolean:
		return postgres.KindBoolean, nil
	case rest.TypeInteger:
		return postgres.KindInteger, nil
	case rest.TypeNumber:
		return postgres.KindNumber, nil
	case rest.TypeDatetime:
		return postgres.KindDateTime, nil
	case rest.TypeList:
		return postgres.KindArray, nil
	case rest.TypeDict:
		return postgres.KindObject, nil
	default:
		return 0, fmt.Errorf("unsupported field type %q", t)
	}
}
