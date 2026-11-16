package restsqlite

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/Gui-Rondon/hyrule/rest"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type queryError struct{ message string }

func (e *queryError) Error() string            { return "restsqlite: " + e.message }
func invalid(format string, args ...any) error { return &queryError{fmt.Sprintf(format, args...)} }

var fieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type filter struct {
	sql  string
	args []any
}
type sortTerm struct {
	field string
	desc  bool
}
type projection map[string]bool

func fieldSQL(name string) (string, error) {
	if !fieldName.MatchString(name) {
		return "", invalid("unsupported field %q: use first-level fields", name)
	}
	switch name {
	case "_id":
		return "id", nil
	case "_etag":
		return "CAST(version AS TEXT)", nil
	}
	return `json_extract(doc, '$.` + name + `')`, nil
}
func compile(value any) (filter, error) {
	switch f := value.(type) {
	case nil:
		return filter{"1", nil}, nil
	case filter:
		return f, nil
	case bson.M:
		return compile(map[string]any(f))
	case map[string]any:
		keys := make([]string, 0, len(f))
		for key := range f {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := []string{}
		args := []any{}
		for _, key := range keys {
			v := f[key]
			if key == "$and" {
				items, ok := v.([]any)
				if !ok || len(items) == 0 {
					return filter{}, invalid("$and needs a non-empty array")
				}
				for _, item := range items {
					child, err := compile(item)
					if err != nil {
						return filter{}, err
					}
					parts = append(parts, "("+child.sql+")")
					args = append(args, child.args...)
				}
				continue
			}
			expr, err := fieldSQL(key)
			if err != nil {
				return filter{}, err
			}
			ops, ok := v.(map[string]any)
			if !ok {
				ops = map[string]any{"$eq": v}
			}
			if len(ops) == 0 {
				return filter{}, invalid("object equality is unsupported")
			}
			opkeys := make([]string, 0, len(ops))
			for op := range ops {
				opkeys = append(opkeys, op)
			}
			sort.Strings(opkeys)
			for _, op := range opkeys {
				val := ops[op]
				sqlop, ok := map[string]string{"$eq": "IS", "$ne": "IS NOT", "$gt": ">", "$gte": ">=", "$lt": "<", "$lte": "<="}[op]
				if !ok {
					return filter{}, invalid("unsupported operator %q", op)
				}
				switch val.(type) {
				case json.Number:
					if integer, err := val.(json.Number).Int64(); err == nil {
						val = integer
					} else if number, err := val.(json.Number).Float64(); err == nil {
						val = number
					} else {
						return filter{}, invalid("invalid numeric comparison")
					}
				case nil, string, bool, float64, int, int32, int64:
				default:
					return filter{}, invalid("comparisons require scalar values")
				}
				parts = append(parts, expr+" "+sqlop+" ?")
				args = append(args, val)
			}
		}
		if len(parts) == 0 {
			return filter{"1", nil}, nil
		}
		return filter{strings.Join(parts, " AND "), args}, nil
	default:
		return filter{}, invalid("unsupported filter type %T", value)
	}
}
func (a *adapter) ParseWhere(raw string, _ *rest.Resource) (any, error) {
	var f map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&f); err != nil || f == nil {
		return nil, invalid("where must be a JSON object")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, invalid("where must contain one JSON object")
	}
	return compile(f)
}
func (a *adapter) CombineFilter(res *rest.Resource, value any, _ bool) (any, error) {
	if res.SoftDelete {
		return nil, invalid("soft delete is unsupported")
	}
	f, err := compile(value)
	if err != nil {
		return nil, err
	}
	base, err := compile(res.Datasource.Filter)
	if err != nil {
		return nil, err
	}
	return filter{"(" + base.sql + ") AND (" + f.sql + ")", append(base.args, f.args...)}, nil
}
func (a *adapter) ParseSort(raw string) (any, error) {
	terms := []sortTerm{}
	if raw == "" {
		return terms, nil
	}
	for _, term := range strings.Split(raw, ",") {
		term = strings.TrimSpace(term)
		name := strings.TrimPrefix(term, "-")
		if _, err := fieldSQL(name); err != nil {
			return nil, err
		}
		terms = append(terms, sortTerm{name, strings.HasPrefix(term, "-")})
	}
	return terms, nil
}
func (a *adapter) CombineSort(res *rest.Resource, value any) any {
	if terms, ok := value.([]sortTerm); ok && len(terms) > 0 {
		return terms
	}
	terms := []sortTerm{}
	for _, term := range res.Datasource.DefaultSort {
		terms = append(terms, sortTerm{term.Key, fmt.Sprint(term.Value) == "-1"})
	}
	return terms
}
func parseProjection(value map[string]any) (projection, error) {
	p := projection{}
	for k, v := range value {
		if _, err := fieldSQL(k); err != nil {
			return nil, err
		}
		switch v {
		case float64(1), 1, true:
			p[k] = true
		case float64(0), 0, false:
			p[k] = false
		default:
			return nil, invalid("projection values must be 0 or 1")
		}
	}
	include, exclude := false, false
	for k, v := range p {
		if k == "_id" {
			continue
		}
		if v {
			include = true
		} else {
			exclude = true
		}
	}
	if include && exclude {
		return nil, invalid("cannot mix projection inclusion and exclusion")
	}
	return p, nil
}
func (a *adapter) ParseProjection(raw string) (any, error) {
	var p map[string]any
	if err := json.Unmarshal([]byte(raw), &p); err != nil || p == nil {
		return nil, invalid("projection must be an object")
	}
	return parseProjection(p)
}
func (a *adapter) CombineProjection(res *rest.Resource, value any) (any, error) {
	// Schema projection is constructed by rest.NewService; the client may only narrow it.
	base := projection{}
	for k, v := range res.Datasource.Projection {
		base[k] = fmt.Sprint(v) == "1"
	}
	client, ok := value.(projection)
	if value != nil && !ok {
		return nil, invalid("invalid projection type")
	}
	if len(base) == 0 {
		return client, nil
	}
	if len(client) == 0 {
		return base, nil
	}
	include := false
	for _, v := range client {
		include = include || v
	}
	for k, v := range base {
		if cv, exists := client[k]; exists {
			base[k] = v && cv
		} else if include && !strings.HasPrefix(k, "_") {
			base[k] = false
		}
	}
	return base, nil
}
func project(doc rest.Doc, p projection) rest.Doc {
	if len(p) == 0 {
		return doc
	}
	include := false
	for _, v := range p {
		include = include || v
	}
	for k := range doc {
		v, exists := p[k]
		if exists && !v || include && !exists && !strings.HasPrefix(k, "_") {
			delete(doc, k)
		}
	}
	return doc
}
func (a *adapter) IDFilter(id any) (any, error) { return compile(map[string]any{"_id": id}) }
func (a *adapter) WriteLookup(doc rest.Doc) (any, error) {
	return compile(map[string]any{"_id": doc["_id"], "_etag": doc["_etag"]})
}
func (a *adapter) EqualsFilter(field string, value, exclude any) (any, error) {
	f := map[string]any{field: value}
	if exclude != nil {
		f["_id"] = map[string]any{"$ne": exclude}
	}
	return compile(f)
}
