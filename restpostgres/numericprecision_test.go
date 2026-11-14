package restpostgres

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Gui-Rondon/hyrule/rest"
)

// numericSchema mirrors rest/numericprecision_test.go's numericResource
// (same two declared fields, "f" also Unique), so the two halves of a
// divergence stay easy to compare side by side.
func numericSchema() rest.Schema {
	return rest.Schema{
		"n": {Type: rest.TypeInteger},
		"f": {Type: rest.TypeNumber, Unique: true},
	}
}

// docField reads field's raw jsonb text straight from table's doc
// column -- never through Go's own (lossy, plain float64) JSON decode
// -- for table/id. Postgres' own jsonb storage is what's under test,
// not anything this package's Go code does to shape it.
func docField(t *testing.T, table, id, field string) string {
	t.Helper()
	pool := testPool(t)
	var text string
	err := pool.QueryRow(context.Background(),
		"SELECT doc->>$1 FROM "+table+" WHERE id = $2", field, id).Scan(&text)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestNumericPrecisionRejectsScientificNotationAndMagnitude(t *testing.T) {
	c := newHTTPTestAPI(t, "numeric_reject_test", numericSchema(), false, nil)
	cases := []struct {
		name string
		body string
	}{
		{"scientific notation, declared integer", `{"n":1e3}`},
		{"scientific notation, declared number", `{"f":1e3}`},
		{"out of int64 range", `{"n":99999999999999999999}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := c.Do("POST", "/numeric_reject_test", tc.body)
			if res.Status != http.StatusUnprocessableEntity {
				t.Fatalf("POST %s = %d %s", tc.body, res.Status, res.Raw)
			}
		})
	}
}

// TestNumericPrecisionDeclaredIntegerBeyond2_53 is the Postgres half
// of rest/numericprecision_test.go's test of the same name: the
// correction Fase 0b makes applies to both backends identically (it's
// the shared rest/http.go+rest/schema.go validation path) -- confirmed
// here down to the raw jsonb text actually stored, not just the HTTP
// response Go already re-encoded.
func TestNumericPrecisionDeclaredIntegerBeyond2_53(t *testing.T) {
	table := "numeric_big_int_test"
	c := newHTTPTestAPI(t, table, numericSchema(), false, nil)
	const exact = "9007199254740993" // 2^53 + 1
	res := c.Do("POST", "/"+table, `{"n":`+exact+`}`)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s", res.Status, res.Raw)
	}
	id := res.String("_id")
	got := c.Do("GET", "/"+table+"/"+id, nil)
	if got.Status != http.StatusOK || !strings.Contains(got.Raw, `"n":`+exact) {
		t.Fatalf("GET = %d %s, want exact %s", got.Status, got.Raw, exact)
	}
	if text := docField(t, table, id, "n"); text != exact {
		t.Errorf("doc->>'n' = %q, want exact %q", text, exact)
	}
}

// TestNumericPrecisionAllowUnknownDivergence is the Postgres half of
// rest/numericprecision_test.go's test of the same name -- same input,
// opposite outcome: seção 16's 🔴 decision, approved 2026-11-14,
// accepts this as a deliberate backend divergence rather than closing
// it. An undeclared field never reaches checkType/convert (validateDict
// skips type validation for it) on either backend, so a magnitude
// beyond int64 is accepted on both -- but Postgres' write path is a
// plain json.Marshal of the doc (restpostgres/doc.go's
// businessFieldsOnly, restpostgres/crud.go), which re-emits a
// json.Number's literal text byte for byte; there is no float64
// round-trip to lose precision in, unlike Mongo's bsonizeNumbers
// fallback (rest/mongoadapter.go).
func TestNumericPrecisionAllowUnknownDivergence(t *testing.T) {
	table := "numeric_allow_unknown_test"
	c := newHTTPTestAPI(t, table, numericSchema(), false, func(r *rest.Resource) { r.AllowUnknown = true })
	const huge = "99999999999999999999" // outside int64 and float64's exact integer range
	res := c.Do("POST", "/"+table, `{"undeclared":`+huge+`}`)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s", res.Status, res.Raw)
	}
	id := res.String("_id")
	got := c.Do("GET", "/"+table+"/"+id, nil)
	if got.Status != http.StatusOK || !strings.Contains(got.Raw, huge) {
		t.Fatalf("GET = %d %s, want the exact digits %s back (unlike Mongo)", got.Status, got.Raw, huge)
	}
	if text := docField(t, table, id, "undeclared"); text != huge {
		t.Errorf("doc->>'undeclared' = %q, want exact %q", text, huge)
	}
}

// TestNumericPrecisionUniqueTypeNumberField is the Postgres half of
// rest/numericprecision_test.go's test of the same name: checkUnique
// (rest/schema.go) converts a TypeNumber value out of json.Number
// before calling EqualsFilter -- without it, postgresAdapter's
// EqualsFilter would hand CompileFilter a json.Number, a Go type its
// KindNumber value check (platform/postgres/query.go's checkKindValue)
// has no case for either.
func TestNumericPrecisionUniqueTypeNumberField(t *testing.T) {
	table := "numeric_unique_test"
	c := newHTTPTestAPI(t, table, numericSchema(), false, nil)
	first := c.Do("POST", "/"+table, `{"f":1.5}`)
	if first.Status != http.StatusCreated {
		t.Fatalf("first POST = %d %s", first.Status, first.Raw)
	}
	dup := c.Do("POST", "/"+table, `{"f":1.5}`)
	if dup.Status != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate POST = %d %s, want 422 not unique", dup.Status, dup.Raw)
	}
}
