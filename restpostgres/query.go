package restpostgres

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
)

// This file completes postgresAdapter's rest.QueryAdapter half (Fase
// 4b) -- ParseWhere/ParseSort/ParseProjection decode a client's
// where/sort/projection query parameters, and CombineFilter/
// CombineSort/CombineProjection combine them with the resource's own
// defaults, same role as mongoAdapter's methods of the same name
// (rest/mongoadapter.go). rest/http.go's parseListQueryFor
// (rest/query.go) calls through these for a resource's own collection
// GET -- a Postgres resource is reachable over HTTP end to end since
// Fase 4b's HTTP-wiring cut (restpostgres/http_test.go).
//
// Deliberately out of scope, named so it isn't mistaken for an
// oversight: rest.Resource.Datasource.Filter/DefaultSort/Projection
// are bson.M/bson.D-typed (Mongo-only) -- a Postgres resource is
// assumed, for now, to never set them. CombineFilter still applies
// soft delete (Resource.SoftDelete has no such typing problem);
// CombineSort/CombineProjection fall back to "nothing set" rather than
// a Datasource default.
//
// Also deliberately out of scope, named rather than silently wrong:
// ParseWhere decodes raw with the standard library's default JSON
// number handling (every number becomes float64), not UseNumber(). A
// comparison operator ($gt/$lt/...) on a KindInteger field therefore
// fails checkKindValue's type check (platform/postgres/query.go),
// which accepts int/int32/int64 for KindInteger but not float64 --
// the same unresolved numeric policy question Fase 0b's 🔴 decisions
// (scientific notation, int64 for declared fields) cover, not solved
// here. A KindNumber field (float64 accepted) is unaffected.

// combinedFilter is CombineFilter's result: a client filter ANDed with
// "deleted = false" when excludeDeleted -- the Postgres equivalent of
// mongoAdapter's bson.M{"_deleted": bson.M{"$ne": true}} condition,
// except "deleted" is a physical column, never part of the jsonb doc,
// so it can't be expressed as a postgres.Query operator; compileFilter
// (crud.go) ANDs it in as raw SQL instead.
//
// The client filter itself is either a postgres.Query (ParseWhere's
// result, a jsonb doc condition) or an id (IDFilter's result, a
// physical-column condition) -- never both; exactly one of query/id is
// set, enforced by CombineFilter's type switch below, not by this
// struct's shape alone.
type combinedFilter struct {
	query          postgres.Query
	id             *uuid.UUID
	excludeDeleted bool
}

// ParseWhere decodes raw as a JSON object into a postgres.Query --
// CompileFilter (platform/postgres/query.go) itself rejects any
// operator it doesn't recognize, so there is no separate denylist to
// apply here the way Mongo's QueryDenylist is (Postgres has no
// $where/$function/$accumulator equivalent to deny in the first
// place).
func (a *postgresAdapter) ParseWhere(raw string, res *rest.Resource) (any, error) {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, fmt.Errorf("restpostgres: invalid where: %w", err)
	}
	return postgres.Query(decoded), nil
}

// CombineFilter combines clientFilter (ParseWhere's result, IDFilter's
// result, or another postgres.Query/map[string]any/uuid.UUID) with
// soft delete, read from res.SoftDelete -- never from a.softDelete.
// The two can diverge (a resource's rest.Resource.SoftDelete and the
// restpostgres.Options.SoftDelete its NewAdapter call was given are
// two independently set booleans, with nothing enforcing they agree),
// and res is this method's own parameter for exactly this reason --
// the same contract mongoAdapter's CombineFilter follows for Mongo.
// a.softDelete stays authoritative only for what it already was
// before this file (hydrate's _deleted field, doc.go), not reopened
// here. Resource.Datasource.Filter is not applied -- see this file's
// top doc comment.
func (a *postgresAdapter) CombineFilter(res *rest.Resource, clientFilter any, includeDeleted bool) (any, error) {
	cf := combinedFilter{excludeDeleted: res.SoftDelete && !includeDeleted}
	switch f := clientFilter.(type) {
	case nil:
	case postgres.Query:
		cf.query = f
	case map[string]any:
		cf.query = postgres.Query(f)
	case uuid.UUID:
		cf.id = &f
	default:
		return nil, fmt.Errorf("restpostgres: CombineFilter: clientFilter must be a postgres.Query, map[string]any or uuid.UUID, got %T", clientFilter)
	}
	return cf, nil
}

// ParseSort decodes raw ("-field,other", the same textual convention
// rest.ParseSort accepts for Mongo) into a []SortTerm.
func (a *postgresAdapter) ParseSort(raw string) (any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var terms []SortTerm
	for field := range strings.SplitSeq(raw, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		desc := false
		if after, ok := strings.CutPrefix(field, "-"); ok {
			desc, field = true, after // not re-trimmed: a space right
			// after "-" must fail the check below, not be cleaned away
		}
		// Checked again after stripping "-", deliberately without
		// re-trimming it first: a bare "-" (or "-,field", "- name",
		// "--name") must not produce a SortTerm with an empty or
		// space-corrupted Field -- that would reach orderByClause
		// (sort.go) as a parameterized, cast ORDER BY on a field that
		// doesn't match any real column/jsonb key, silently sorting
		// wrong instead of erroring loudly, the same discipline
		// toSortTerms/toProjection already apply to a wrong type.
		if field == "" || strings.ContainsAny(field, " \t") || strings.HasPrefix(field, "-") {
			return nil, fmt.Errorf("restpostgres: invalid sort %q", raw)
		}
		terms = append(terms, SortTerm{Field: field, Desc: desc})
	}
	if len(terms) == 0 {
		return nil, fmt.Errorf("restpostgres: invalid sort %q", raw)
	}
	return terms, nil
}

// CombineSort returns clientSort (a []SortTerm) as-is, or no sort at
// all if clientSort is absent/wrong-typed -- Resource.Datasource.
// DefaultSort is not applied, see this file's doc comment.
func (a *postgresAdapter) CombineSort(res *rest.Resource, clientSort any) any {
	terms, _ := clientSort.([]SortTerm)
	return terms
}

// ParseProjection decodes raw (a JSON object, {"field":1,...}, Eve's
// convention) into a Projection: any truthy value includes the field.
func (a *postgresAdapter) ParseProjection(raw string) (any, error) {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, fmt.Errorf("restpostgres: invalid projection: %w", err)
	}
	proj := make(Projection, len(decoded))
	for field, v := range decoded {
		proj[field] = truthy(v)
	}
	return proj, nil
}

// CombineProjection returns clientProjection (a Projection) as-is, or
// no projection (keep everything) if clientProjection is absent/
// wrong-typed -- Resource.Datasource.Projection is not applied, see
// this file's doc comment. applyProjection (projection.go) always
// keeps the meta fields regardless.
func (a *postgresAdapter) CombineProjection(res *rest.Resource, clientProjection any) (any, error) {
	proj, _ := clientProjection.(Projection)
	return proj, nil
}

// truthy is Python's bool(): false for JSON's zero number and false,
// everything decoded into a Projection's values is a JSON number or
// boolean. Used instead of rest's own unexported truthyValue, which
// this package cannot reach.
func truthy(v any) bool {
	switch n := v.(type) {
	case float64:
		return n != 0
	case bool:
		return n
	}
	return false
}
