package postgres

import (
	"fmt"
	"sort"
	"strings"
)

// Query is the neutral, JSON-shaped filter CompileFilter compiles to
// SQL. restpostgres (Fase 2) builds one of these from a client's query
// string -- the same role bson.M plays for mongoAdapter's ParseWhere,
// without this package importing rest or Mongo.
//
// A top-level key is either a field name (its value is a literal, for
// an implicit $eq, or an operator map such as Query{"$gt": 5, "$lt":
// 10}, ANDed together) or one of $and/$or (value []Query) / $not (value
// Query). A dotted field name ("a.b") addresses a nested path and
// accepts only $exists/$eq/$ne.
//
// Operator maps and $and/$or lists are also accepted as plain
// map[string]any/[]any -- what json.Unmarshal actually produces for a
// JSON object/array decoded into an any field, since a caller building
// a Query from a client's raw JSON query string never gets the named
// Query/[]Query types back from the decoder. See asQuery/asQueryList.
type Query map[string]any

// asQuery normalizes v into a Query, accepting both the named Query
// type and the map[string]any json.Unmarshal produces for a JSON
// object -- the two are the same underlying type, so this is a cheap
// reinterpretation, not a copy.
func asQuery(v any) (Query, bool) {
	switch q := v.(type) {
	case Query:
		return q, true
	case map[string]any:
		return Query(q), true
	}
	return nil, false
}

// asQueryList normalizes v into a []Query, accepting both []Query and
// the []any json.Unmarshal produces for a JSON array of objects.
func asQueryList(v any) ([]Query, bool) {
	switch list := v.(type) {
	case []Query:
		return list, true
	case []any:
		out := make([]Query, len(list))
		for i, item := range list {
			q, ok := asQuery(item)
			if !ok {
				return nil, false
			}
			out[i] = q
		}
		return out, true
	}
	return nil, false
}

// CompileFilter compiles q into a parameterized boolean SQL expression,
// usable directly as a WHERE clause (no leading "WHERE"), against a
// jsonb column named doc. startAt offsets placeholder numbers, as
// whereClause's does, for a caller that already used $1..$N for
// something else. An empty/nil Query compiles to "TRUE" (matches every
// row). schema picks the SQL cast for each field's filter values.
func CompileFilter(q Query, schema SchemaSpec, startAt int) (where string, args []any, err error) {
	if len(q) == 0 {
		return "TRUE", nil, nil
	}
	c := &compiler{schema: schema, n: startAt}
	expr, err := c.compileQuery(q)
	if err != nil {
		return "", nil, err
	}
	return expr, c.args, nil
}

type compiler struct {
	schema SchemaSpec
	args   []any
	n      int
}

// param appends v as the next bind argument and returns its placeholder.
func (c *compiler) param(v any) string {
	c.args = append(c.args, v)
	c.n++
	return fmt.Sprintf("$%d", c.n)
}

// checkKindValue rejects a Go value whose type pgx cannot bind to
// kind.SQLCast()'s target Postgres type -- e.g. a plain Go int against
// KindString's "text" cast, which pgx fails to encode only once the
// query actually runs, with a cryptic "cannot find encode plan"
// message. Caught here instead, as a clear compile-time error: the
// value's Go type must already match what restpostgres's Schema
// validation (Fase 2) normalizes a field of this Kind to.
func checkKindValue(val any, kind Kind) error {
	ok := true
	switch kind {
	case KindString, KindDateTime:
		_, ok = val.(string)
	case KindInteger:
		switch val.(type) {
		case int, int32, int64:
		default:
			ok = false
		}
	case KindNumber:
		switch val.(type) {
		case int, int32, int64, float32, float64:
		default:
			ok = false
		}
	case KindBoolean:
		_, ok = val.(bool)
	}
	if !ok {
		return fmt.Errorf("postgres: value of type %T does not match field kind %v", val, kind)
	}
	return nil
}

// jsonbLiteral renders val as a jsonb value comparable against doc's
// content: 'null'::jsonb for a nil value (JSON null, never SQL NULL),
// otherwise to_jsonb($N::<kind's cast>) so e.g. a KindInteger field
// compares against a jsonb number, never a jsonb string of the same
// digits.
func (c *compiler) jsonbLiteral(val any, kind Kind) (string, error) {
	if val == nil {
		return "'null'::jsonb", nil
	}
	if err := checkKindValue(val, kind); err != nil {
		return "", err
	}
	return fmt.Sprintf("to_jsonb(%s::%s)", c.param(val), kind.SQLCast()), nil
}

// docRef is the jsonb value at field, directly under doc.
func (c *compiler) docRef(field string) string {
	return fmt.Sprintf("doc->%s::text", c.param(field))
}

func (c *compiler) compileQuery(q Query) (string, error) {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic SQL text across calls with the same q

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		expr, err := c.compileTop(k, q[k])
		if err != nil {
			return "", err
		}
		parts = append(parts, expr)
	}
	// An empty Query (no top-level keys) compiles to "TRUE" -- matches
	// every row, the AND-identity -- rather than the syntactically
	// invalid "()". Reached both directly (CompileFilter's own q) and
	// recursively (an empty sub-Query inside $not/$and/$or, or as a
	// field's empty operator map via compileField/compileTopField).
	if len(parts) == 0 {
		return "TRUE", nil
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return "(" + strings.Join(parts, " AND ") + ")", nil
}

func (c *compiler) compileTop(key string, value any) (string, error) {
	switch key {
	case "$and":
		return c.compileBoolList(value, " AND ")
	case "$or":
		return c.compileBoolList(value, " OR ")
	case "$not":
		inner, ok := asQuery(value)
		if !ok {
			return "", fmt.Errorf("postgres: $not expects a Query, got %T", value)
		}
		expr, err := c.compileQuery(inner)
		if err != nil {
			return "", err
		}
		return "(NOT (" + expr + "))", nil
	default:
		return c.compileField(key, value)
	}
}

func (c *compiler) compileBoolList(value any, sep string) (string, error) {
	list, ok := asQueryList(value)
	if !ok {
		return "", fmt.Errorf("postgres: operator expects []Query, got %T", value)
	}
	if len(list) == 0 {
		return "", fmt.Errorf("postgres: empty $and/$or list")
	}
	parts := make([]string, len(list))
	for i, sub := range list {
		expr, err := c.compileQuery(sub)
		if err != nil {
			return "", err
		}
		parts[i] = expr
	}
	return "(" + strings.Join(parts, sep) + ")", nil
}

func (c *compiler) compileField(field string, value any) (string, error) {
	ops, isOps := asQuery(value)
	if !isOps {
		ops = Query{"$eq": value}
	}
	if path := strings.Split(field, "."); len(path) > 1 {
		return c.compileNestedField(path, ops)
	}
	return c.compileTopField(field, ops)
}

// compileNestedField supports only $exists/$eq/$ne for a dotted path --
// no other operator is defined for nested content.
func (c *compiler) compileNestedField(path []string, ops Query) (string, error) {
	if len(ops) != 1 {
		return "", fmt.Errorf("postgres: nested path %q: exactly one of $exists/$eq/$ne, got %d operators", strings.Join(path, "."), len(ops))
	}
	for op, val := range ops {
		ptr := c.param(path)
		ref := fmt.Sprintf("doc #> %s::text[]", ptr)
		switch op {
		case "$exists":
			b, ok := val.(bool)
			if !ok {
				return "", fmt.Errorf("postgres: $exists expects a bool, got %T", val)
			}
			if b {
				return fmt.Sprintf("(%s IS NOT NULL) IS TRUE", ref), nil
			}
			return fmt.Sprintf("(%s IS NULL) IS TRUE", ref), nil
		case "$eq":
			lit, err := c.jsonbLiteral(val, KindString)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("(%s = %s) IS TRUE", ref, lit), nil
		case "$ne":
			lit, err := c.jsonbLiteral(val, KindString)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("(%s IS DISTINCT FROM %s) IS TRUE", ref, lit), nil
		default:
			return "", fmt.Errorf("postgres: nested path %q: unsupported operator %q", strings.Join(path, "."), op)
		}
	}
	panic("unreachable")
}

func (c *compiler) compileTopField(field string, ops Query) (string, error) {
	kind := c.schema.FieldKind(field)
	keys := make([]string, 0, len(ops))
	for k := range ops {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, op := range keys {
		expr, err := c.compileOp(field, kind, op, ops[op])
		if err != nil {
			return "", err
		}
		parts = append(parts, expr)
	}
	// An empty operator map (e.g. Query{"field": Query{}}) compiles to
	// "TRUE" -- same rationale as compileQuery's empty-Query case.
	if len(parts) == 0 {
		return "TRUE", nil
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return "(" + strings.Join(parts, " AND ") + ")", nil
}

var comparisonSQL = map[string]string{"$gt": ">", "$gte": ">=", "$lt": "<", "$lte": "<="}

// compileOp dispatches by operator. Each branch calls c.docRef(field)
// (which consumes a bind parameter) itself, exactly where its result is
// used -- never upfront -- so a branch that can return without
// referencing doc at all ($in/$nin on an empty list) never leaves an
// orphan argument with no matching placeholder in the output SQL.
func (c *compiler) compileOp(field string, kind Kind, op string, val any) (string, error) {
	switch op {
	case "$eq":
		ref := c.docRef(field)
		lit, err := c.jsonbLiteral(val, kind)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s = %s) IS TRUE", ref, lit), nil
	case "$ne":
		ref := c.docRef(field)
		lit, err := c.jsonbLiteral(val, kind)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s IS DISTINCT FROM %s) IS TRUE", ref, lit), nil
	case "$gt", "$gte", "$lt", "$lte":
		if val == nil {
			return "", fmt.Errorf("postgres: %s does not accept null", op)
		}
		ref := c.docRef(field)
		lit, err := c.jsonbLiteral(val, kind)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(%s %s %s) IS TRUE", ref, comparisonSQL[op], lit), nil
	case "$in", "$nin":
		return c.compileInNin(field, kind, op, val)
	case "$exists":
		b, ok := val.(bool)
		if !ok {
			return "", fmt.Errorf("postgres: $exists expects a bool, got %T", val)
		}
		ptr := c.param(field)
		if b {
			return fmt.Sprintf("(doc ? %s::text) IS TRUE", ptr), nil
		}
		return fmt.Sprintf("(NOT (doc ? %s::text)) IS TRUE", ptr), nil
	case "$has":
		return c.compileHas(field, val, c.schema.ElementKind(field))
	case "$hasSome":
		return c.compileHasSome(field, c.schema.ElementKind(field), val)
	case "$hasEvery":
		return c.compileHasEvery(field, c.schema.ElementKind(field), val)
	default:
		return "", fmt.Errorf("postgres: unsupported operator %q on field %q", op, field)
	}
}

// jsonbArray renders list as a Postgres jsonb[] array literal, one
// jsonbLiteral-cast element per item -- shared by $in/$nin/$hasSome, so
// a future change to how the array literal is assembled only needs to
// happen in one place.
func (c *compiler) jsonbArray(list []any, kind Kind) (string, error) {
	elems := make([]string, len(list))
	for i, v := range list {
		lit, err := c.jsonbLiteral(v, kind)
		if err != nil {
			return "", err
		}
		elems[i] = lit
	}
	return "ARRAY[" + strings.Join(elems, ", ") + "]::jsonb[]", nil
}

// compileInNin: $in on an empty list never matches (vacuous); $nin on
// an empty list always matches -- both return before touching doc at
// all, so field's placeholder is never consumed for an empty list.
func (c *compiler) compileInNin(field string, kind Kind, op string, val any) (string, error) {
	list, ok := val.([]any)
	if !ok {
		return "", fmt.Errorf("postgres: %s expects a slice, got %T", op, val)
	}
	if len(list) == 0 {
		if op == "$in" {
			return "FALSE", nil
		}
		return "TRUE", nil
	}
	ref := c.docRef(field)
	arr, err := c.jsonbArray(list, kind)
	if err != nil {
		return "", err
	}
	exists := fmt.Sprintf("EXISTS (SELECT 1 FROM unnest(%s) AS item(value) WHERE %s IS NOT DISTINCT FROM item.value)", arr, ref)
	if op == "$in" {
		return "(" + exists + ") IS TRUE", nil
	}
	return "(NOT (" + exists + ")) IS TRUE", nil
}

// compileHas: doc[field] is an array containing exactly val.
func (c *compiler) compileHas(field string, val any, kind Kind) (string, error) {
	ref := c.docRef(field)
	elem := "'null'::jsonb"
	if val != nil {
		if err := checkKindValue(val, kind); err != nil {
			return "", err
		}
		elem = fmt.Sprintf("%s::%s", c.param(val), kind.SQLCast())
	}
	return fmt.Sprintf("(CASE WHEN jsonb_typeof(%s) = 'array' THEN %s @> jsonb_build_array(%s) ELSE FALSE END)", ref, ref, elem), nil
}

func (c *compiler) compileHasSome(field string, kind Kind, val any) (string, error) {
	list, ok := val.([]any)
	if !ok {
		return "", fmt.Errorf("postgres: $hasSome expects a slice, got %T", val)
	}
	ref := c.docRef(field)
	arr, err := c.jsonbArray(list, kind)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"(CASE WHEN jsonb_typeof(%s) = 'array' THEN EXISTS (SELECT 1 FROM jsonb_array_elements(%s) AS e(value) WHERE e.value = ANY(%s)) ELSE FALSE END)",
		ref, ref, arr), nil
}

// compileHasEvery builds its jsonb array via jsonb_build_array (which
// takes each cast element directly, not wrapped in to_jsonb like
// jsonbArray's ARRAY[...]::jsonb[] elements), but reuses jsonbLiteral
// for the null/cast/kind-check logic rather than duplicating it --
// jsonbLiteral's to_jsonb(...) output nests fine as a jsonb_build_array
// argument.
func (c *compiler) compileHasEvery(field string, kind Kind, val any) (string, error) {
	list, ok := val.([]any)
	if !ok {
		return "", fmt.Errorf("postgres: $hasEvery expects a slice, got %T", val)
	}
	ref := c.docRef(field)
	elems := make([]string, len(list))
	for i, v := range list {
		lit, err := c.jsonbLiteral(v, kind)
		if err != nil {
			return "", err
		}
		elems[i] = lit
	}
	arr := "jsonb_build_array(" + strings.Join(elems, ", ") + ")"
	return fmt.Sprintf("(CASE WHEN jsonb_typeof(%s) = 'array' THEN %s @> %s ELSE FALSE END)", ref, ref, arr), nil
}
