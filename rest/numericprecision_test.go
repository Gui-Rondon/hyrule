package rest_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/Gui-Rondon/hyrule/platform/mongodb"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/testkit"
)

// numericResource declares one TypeInteger and one Unique TypeNumber
// field, with AllowUnknown so an undeclared field's own numeric policy
// (or lack of one, seção 16's 🔴 "divergência real entre backends,
// aceita deliberadamente") can be driven over the same HTTP surface.
// "f" is Unique specifically so checkUnique's EqualsFilter path (rest/
// schema.go) gets exercised with a json.Number value -- convert()
// deliberately never touches TypeNumber, so without checkUnique's own
// concreteNumber conversion this would reach an Adapter with a type it
// has no codec/compiler case for.
func numericResource() *rest.Resource {
	return &rest.Resource{
		Name: "nums",
		Schema: rest.Schema{
			"n": {Type: rest.TypeInteger},
			"f": {Type: rest.TypeNumber, Unique: true},
		},
		AllowUnknown:    true,
		ResourceMethods: []string{http.MethodGet, http.MethodPost},
		ItemMethods:     []string{http.MethodGet},
	}
}

// newNumericAPI is newAPI (service_test.go), except it also returns
// the underlying *mongo.Database -- bsonTypeOf below needs it to
// inspect the raw stored document, something no Adapter/Service
// method exposes (and shouldn't: it's a test-only concern).
func newNumericAPI(t *testing.T) (*testkit.Client, *mongo.Database) {
	t.Helper()
	db := testkit.Mongo(t)
	tx, _, err := mongodb.TransactorFor(context.Background(), db.Client())
	if err != nil {
		t.Fatal(err)
	}
	res := numericResource()
	res.Store = mongodb.NewStore(db.Collection("nums"))
	api := rest.New(rest.Options{Transactor: tx})
	api.Register(res)
	return testkit.NewClient(t, api), db
}

// bsonTypeOf looks up field on the raw stored document for id --
// never through the driver's own Go decode, which would hide the very
// distinction this test exists to confirm (seção 19's pendência de
// inspeção: "o tipo BSON efetivamente armazenado").
func bsonTypeOf(t *testing.T, db *mongo.Database, collection, id, field string) bson.Type {
	t.Helper()
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		t.Fatalf("bad id %q: %v", id, err)
	}
	raw, err := db.Collection(collection).FindOne(context.Background(), bson.M{"_id": oid}).Raw()
	if err != nil {
		t.Fatal(err)
	}
	return raw.Lookup(field).Type
}

func TestNumericPrecisionRejectsScientificNotation(t *testing.T) {
	c, _ := newNumericAPI(t)
	cases := []struct {
		name string
		body string
	}{
		{"declared integer", `{"n":1e3}`},
		{"declared number", `{"f":1e3}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := c.Do("POST", "/nums", tc.body)
			if res.Status != http.StatusUnprocessableEntity {
				t.Fatalf("POST %s = %d %s", tc.body, res.Status, res.Raw)
			}
		})
	}
}

func TestNumericPrecisionRejectsOutOfInt64Range(t *testing.T) {
	c, _ := newNumericAPI(t)
	res := c.Do("POST", "/nums", `{"n":99999999999999999999}`)
	if res.Status != http.StatusUnprocessableEntity {
		t.Fatalf("POST = %d %s", res.Status, res.Raw)
	}
}

func TestNumericPrecisionAcceptsWholeDecimalInteger(t *testing.T) {
	// "1.0" stays accepted for a declared integer -- idêntico ao
	// histórico, never part of what this revision changes.
	c, _ := newNumericAPI(t)
	res := c.Do("POST", "/nums", `{"n":1.0}`)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s", res.Status, res.Raw)
	}
	id := res.String("_id")
	got := c.Do("GET", "/nums/"+id, nil)
	if got.Status != http.StatusOK || !strings.Contains(got.Raw, `"n":1`) {
		t.Fatalf("GET = %d %s", got.Status, got.Raw)
	}
}

func TestNumericPrecisionRejectsNonZeroFraction(t *testing.T) {
	c, _ := newNumericAPI(t)
	res := c.Do("POST", "/nums", `{"n":1.5}`)
	if res.Status != http.StatusUnprocessableEntity {
		t.Fatalf("POST = %d %s", res.Status, res.Raw)
	}
}

func TestNumericPrecisionTrailingContentRejected(t *testing.T) {
	c, _ := newNumericAPI(t)
	res := c.Do("POST", "/nums", `{"n":1}x`)
	if res.Status != http.StatusBadRequest {
		t.Fatalf("POST = %d %s", res.Status, res.Raw)
	}
}

// TestNumericPrecisionDeclaredIntegerBeyond2_53 is the correction this
// revision makes for a declared TypeInteger field: before Fase 0b,
// readBody decoded straight to float64 and this value was already
// rounded before checkType/convert ever saw it. Confirmed here against
// a real Mongo, down to the BSON type actually stored (int64, not
// double) -- the pendência de inspeção named in seção 19.
func TestNumericPrecisionDeclaredIntegerBeyond2_53(t *testing.T) {
	c, db := newNumericAPI(t)
	const exact = "9007199254740993" // 2^53 + 1
	res := c.Do("POST", "/nums", `{"n":`+exact+`}`)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s", res.Status, res.Raw)
	}
	id := res.String("_id")
	got := c.Do("GET", "/nums/"+id, nil)
	if got.Status != http.StatusOK || !strings.Contains(got.Raw, `"n":`+exact) {
		t.Fatalf("GET = %d %s, want exact %s", got.Status, got.Raw, exact)
	}
	if typ := bsonTypeOf(t, db, "nums", id, "n"); typ != bson.TypeInt64 {
		t.Errorf("stored BSON type = %v, want TypeInt64", typ)
	}
}

// TestNumericPrecisionAllowUnknownDivergence confirms seção 16's 🔴
// decision, approved 2026-11-14: an undeclared field never goes
// through checkType/convert at all (validateDict skips type validation
// entirely for it), so a magnitude beyond int64 is accepted on both
// backends -- but Mongo rounds it through bsonizeNumbers' Float64()
// fallback (no exact int64 representation exists for it), while
// Postgres preserves it exactly (restpostgres/numericprecision_test.go's
// TestNumericPrecisionAllowUnknownDivergence is the Postgres half of
// this same scenario). This is the one case the plan accepts as a
// deliberate, documented backend divergence rather than closing it.
func TestNumericPrecisionAllowUnknownDivergence(t *testing.T) {
	c, db := newNumericAPI(t)
	const huge = "99999999999999999999" // outside int64 and float64's exact integer range
	res := c.Do("POST", "/nums", `{"undeclared":`+huge+`}`)
	if res.Status != http.StatusCreated {
		t.Fatalf("POST = %d %s", res.Status, res.Raw)
	}
	id := res.String("_id")
	got := c.Do("GET", "/nums/"+id, nil)
	if got.Status != http.StatusOK {
		t.Fatalf("GET = %d %s", got.Status, got.Raw)
	}
	if strings.Contains(got.Raw, huge) {
		t.Errorf("GET = %s: got the exact digits back, expected Mongo's float64 rounding to have changed them", got.Raw)
	}
	if typ := bsonTypeOf(t, db, "nums", id, "undeclared"); typ != bson.TypeDouble {
		t.Errorf("stored BSON type = %v, want TypeDouble (rounded via Float64 fallback)", typ)
	}
}

// TestNumericPrecisionUniqueTypeNumberField confirms checkUnique
// (rest/schema.go) converts a TypeNumber value out of json.Number
// before building the EqualsFilter -- without it, mongoAdapter's
// EqualsFilter hands bson.M{"f": json.Number("1.5")} straight to the
// driver, which encodes a json.Number (a string-kinded Go type) as a
// BSON string; comparing that against the real stored BSON double
// never matches, so the second insert's duplicate would have gone
// undetected (silently wrong, not even a crash) instead of the 422
// confirmed here.
func TestNumericPrecisionUniqueTypeNumberField(t *testing.T) {
	c, _ := newNumericAPI(t)
	first := c.Do("POST", "/nums", `{"f":1.5}`)
	if first.Status != http.StatusCreated {
		t.Fatalf("first POST = %d %s", first.Status, first.Raw)
	}
	dup := c.Do("POST", "/nums", `{"f":1.5}`)
	if dup.Status != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate POST = %d %s, want 422 not unique", dup.Status, dup.Raw)
	}
}
