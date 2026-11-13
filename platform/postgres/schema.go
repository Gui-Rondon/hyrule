package postgres

// Kind is the neutral type of a declared document field. query.go's DSL
// compiler uses it to pick the SQL cast (to_jsonb($N::<cast>)) so a
// filter value compares against doc's jsonb content as the field's real
// type, never as a bare string -- see postgres-rfc.md §10's "Tipagem por
// Kind do campo, nunca text fixo". It mirrors rest.Schema's field kinds
// without importing rest: platform/postgres never imports rest
// (postgres-rfc.md's restrictions, reaffirmed for Fase 1).
type Kind int

const (
	KindString Kind = iota
	KindInteger
	KindNumber
	KindBoolean
	KindDateTime
	KindArray
	KindObject
)

// sqlCast is the Postgres type a filter parameter is cast to before
// to_jsonb() wraps it, so e.g. a KindInteger field compares against a
// jsonb number, never a jsonb string holding the same digits.
func (k Kind) sqlCast() string {
	switch k {
	case KindInteger:
		return "int8"
	case KindNumber:
		return "numeric"
	case KindBoolean:
		return "boolean"
	case KindDateTime:
		return "timestamptz"
	default:
		return "text"
	}
}

// SchemaSpec is a neutral, flattened view of a resource's declared
// field kinds -- everything the DSL compiler needs to pick the right
// SQL cast for a filter value. restpostgres (Fase 2) builds one of
// these from a rest.Schema; platform/postgres itself never depends on
// rest.Schema's shape.
type SchemaSpec struct {
	Fields map[string]Kind
}

// kind reports field's declared Kind, or KindString for a field with no
// entry -- the cast-agnostic default, consistent with a client being
// able to filter/sort by an AllowUnknown field the schema never declared.
func (s SchemaSpec) kind(field string) Kind {
	if k, ok := s.Fields[field]; ok {
		return k
	}
	return KindString
}
