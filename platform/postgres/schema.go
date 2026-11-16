package postgres

// Kind is the neutral type of a declared document field. query.go's DSL
// compiler uses it to pick the SQL cast (to_jsonb($N::<cast>)) so a
// filter value compares against doc's jsonb content as the field's real
// type, never as a bare string. It mirrors rest.Schema's field kinds
// without importing rest: platform/postgres never imports rest.
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

// SQLCast is the Postgres type a filter parameter is cast to before
// to_jsonb() wraps it, so e.g. a KindInteger field compares against a
// jsonb number, never a jsonb string holding the same digits.
func (k Kind) SQLCast() string {
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
	// ElementKinds declares the Kind of the elements inside an array
	// field, for $has/$hasSome/$hasEvery's search-value cast. This is
	// independent from Fields[field]: an array field's own Kind is
	// KindArray, which says nothing about what's inside it (e.g. a
	// KindArray field of integers still needs KindInteger here for its
	// elements to cast correctly).
	ElementKinds map[string]Kind
}

// FieldKind reports field's declared Kind, or KindString for a field
// with no entry -- the cast-agnostic default, consistent with a client
// being able to filter/sort by an AllowUnknown field the schema never
// declared. Exported for restpostgres (and any other package building
// SQL alongside the DSL compiler, e.g. for ORDER BY) to pick the same
// cast CompileFilter would for a given field.
func (s SchemaSpec) FieldKind(field string) Kind {
	if k, ok := s.Fields[field]; ok {
		return k
	}
	return KindString
}

// ElementKind reports the declared Kind of field's array elements, or
// KindString for a field with no entry -- same cast-agnostic default as
// FieldKind.
func (s SchemaSpec) ElementKind(field string) Kind {
	if k, ok := s.ElementKinds[field]; ok {
		return k
	}
	return KindString
}
