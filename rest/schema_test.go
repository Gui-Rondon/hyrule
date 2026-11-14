package rest

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func ptr(f float64) *float64 { return &f }

// Messages of Cerberus 1.3, as returned in Eve's _issues.
func TestValidationMessages(t *testing.T) {
	schema := Schema{
		"status":  {Type: TypeString, Allowed: []any{"draft", "published"}},
		"tags":    {Type: TypeList, Allowed: []any{"a", "b"}},
		"title":   {Type: TypeString, MinLength: 3, MaxLength: 5},
		"name":    {Type: TypeString, NotEmpty: true},
		"slug":    {Type: TypeString, Regex: regexp.MustCompile(`^[a-z]+$`)},
		"count":   {Type: TypeInteger, Min: ptr(1), Max: ptr(10)},
		"must":    {Type: TypeString, Required: true},
		"nullok":  {Type: TypeString, Nullable: true},
		"notnull": {Type: TypeString},
		"id":      {Type: TypeObjectID},
		"head": {Type: TypeDict, Schema: Schema{
			"text": {Type: TypeString},
		}},
		"items": {Type: TypeList, Items: &Field{Type: TypeString}},
	}
	doc := map[string]any{
		"status":  "x",
		"tags":    []any{"a", "c"},
		"title":   "ab",
		"name":    "",
		"slug":    "A1",
		"count":   float64(11),
		"nullok":  nil,
		"notnull": nil,
		"id":      "zz",
		"head":    map[string]any{"text": float64(1), "extra": "x"},
		"items":   []any{"ok", float64(2)},
		"unknown": 1,
	}
	issues := validateDict(context.Background(), schema, doc, false, false)
	want := Issues{
		"status":  "unallowed value x",
		"tags":    "unallowed values ['c']",
		"title":   "min length is 3",
		"name":    "empty values not allowed",
		"slug":    "value does not match regex '^[a-z]+$'",
		"count":   "max value is 10",
		"must":    "required field",
		"notnull": "null value not allowed",
		"id":      "value 'zz' cannot be converted to a ObjectId",
		"head":    Issues{"text": "must be of string type", "extra": "unknown field"},
		"items":   Issues{"1": "must be of string type"},
		"unknown": "unknown field",
	}
	if !reflect.DeepEqual(issues, want) {
		for k := range want {
			if !reflect.DeepEqual(issues[k], want[k]) {
				t.Errorf("%s: got %#v, want %#v", k, issues[k], want[k])
			}
		}
		for k := range issues {
			if _, ok := want[k]; !ok {
				t.Errorf("unexpected issue %s: %#v", k, issues[k])
			}
		}
	}
}

// empty: True declared (authors-api email): an empty value skips the
// regex; without it, "" is still checked (twitter), as Cerberus 1.3.
func TestAllowEmpty(t *testing.T) {
	re := regexp.MustCompile(`^.+@.+$`)
	schema := Schema{
		"email":   {Type: TypeString, Regex: re, AllowEmpty: true},
		"twitter": {Type: TypeString, Regex: re},
		"tags":    {Type: TypeList, MinLength: 1, AllowEmpty: true},
	}
	issues := validateDict(context.Background(), schema, map[string]any{"email": "", "twitter": "", "tags": []any{}}, false, false)
	if len(issues) != 1 || issues["twitter"] == nil {
		t.Errorf("issues = %v", issues)
	}
	issues = validateDict(context.Background(), schema, map[string]any{"email": "x"}, false, false)
	if issues["email"] == nil {
		t.Errorf("a non empty value is still checked: %v", issues)
	}
}

func TestCustomCheck(t *testing.T) {
	schema := Schema{"component": {Type: TypeDict, Check: func(_ context.Context, v any, _ map[string]any) any {
		if v.(map[string]any)["type"] == nil {
			return "missing type"
		}
		return nil
	}}}
	issues := validateDict(context.Background(), schema, map[string]any{"component": map[string]any{}}, false, false)
	if issues["component"] != "missing type" {
		t.Errorf("issues = %v", issues)
	}
}

func TestPartialValidationSkipsRequired(t *testing.T) {
	schema := Schema{"must": {Type: TypeString, Required: true}}
	if issues := validateDict(context.Background(), schema, map[string]any{}, true, false); len(issues) != 0 {
		t.Errorf("issues = %v", issues)
	}
}

func TestTypesAndConversions(t *testing.T) {
	schema := Schema{
		"i":    {Type: TypeInteger},
		"n":    {Type: TypeNumber, Min: ptr(0.5)},
		"d":    {Type: TypeDatetime},
		"o":    {Type: TypeObjectID},
		"b":    {Type: TypeBoolean, Allowed: []any{true}},
		"ids":  {Type: TypeList, Items: &Field{Type: TypeObjectID}},
		"sub":  {Type: TypeDict, Schema: Schema{"when": {Type: TypeDatetime}}},
		"name": {Type: TypeString, MaxLength: 2},
	}
	doc := map[string]any{
		"i": float64(3), "n": 1.5, "d": "Thu, 22 Oct 2026 17:15:03 GMT", "o": "6ada449701ecde6e488b8ac9",
		"b": true, "ids": []any{"6ada449701ecde6e488b8ac9"}, "sub": map[string]any{"when": "Thu, 22 Oct 2026 17:15:03 GMT"},
	}
	if issues := validateDict(context.Background(), schema, doc, false, false); len(issues) != 0 {
		t.Fatalf("issues = %v", issues)
	}
	if _, ok := doc["i"].(int64); !ok {
		t.Errorf("integer not converted: %T", doc["i"])
	}
	if _, ok := doc["d"].(time.Time); !ok {
		t.Errorf("datetime not converted: %T", doc["d"])
	}
	if _, ok := doc["o"].(bson.ObjectID); !ok {
		t.Errorf("objectid not converted: %T", doc["o"])
	}
	if _, ok := doc["ids"].([]any)[0].(bson.ObjectID); !ok {
		t.Errorf("list of objectids not converted")
	}
	if _, ok := doc["sub"].(map[string]any)["when"].(time.Time); !ok {
		t.Errorf("nested datetime not converted")
	}

	bad := map[string]any{"i": 1.5, "n": "x", "d": "ontem", "b": false, "name": "abc"}
	issues := validateDict(context.Background(), schema, bad, true, false)
	want := Issues{
		"i": "must be of integer type", "n": "must be of number type", "d": "must be of datetime type",
		"b": "unallowed value False", "name": "max length is 2",
	}
	if !reflect.DeepEqual(issues, want) {
		t.Errorf("issues = %v", issues)
	}
	if got := validateDict(context.Background(), schema, map[string]any{"n": 0.1, "d": 1}, true, false); got["n"] != "min value is 0.5" || got["d"] != "must be of datetime type" {
		t.Errorf("issues = %v", got)
	}
	if pyRepr(1.5) != "1.5" || pyRepr([]any{1}) != "[1]" {
		t.Error("pyRepr")
	}
}

// NORMALIZE_ON_PATCH of Eve 2.x: a PATCH gets the default of a field that is
// neither sent nor stored; a stored value is never reset.
func TestPatchDefaults(t *testing.T) {
	schema := Schema{
		"is_partner": {Type: TypeBoolean, Default: false},
		"tags_list":  {Type: TypeList, Default: []any{}},
		"seo": {Type: TypeDict, Schema: Schema{
			"title":    {Type: TypeString},
			"no_index": {Type: TypeBoolean, Default: false},
		}},
	}
	v := validator{schema: schema}
	doc := map[string]any{"seo": map[string]any{"title": "t"}}
	stored := Doc{"is_partner": true, "seo": bson.D{{Key: "no_index", Value: true}}}
	if issues, err := v.validate(context.Background(), doc, true, stored); err != nil || len(issues) != 0 {
		t.Fatal(issues, err)
	}
	want := map[string]any{"seo": map[string]any{"title": "t"}, "tags_list": []any{}}
	if !reflect.DeepEqual(doc, want) {
		t.Errorf("doc = %v, want %v", doc, want)
	}
}

// TestParseExactInteger covers the matrix seção 10.2's revision of the
// Postgres portability plan documents for a declared TypeInteger
// field -- both the normal path (json.Number, readBody's exact
// literal text) and the compatibility path (a plain float64 a Go
// caller built a Doc with directly, never through JSON).
func TestParseExactInteger(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  int64
		ok    bool
	}{
		{"exact beyond 2^53", json.Number("9007199254740993"), 9007199254740993, true},
		{"whole decimal accepted", json.Number("1.0"), 1, true},
		{"whole decimal, multiple zeros", json.Number("1.00"), 1, true},
		{"negative whole decimal", json.Number("-5.0"), -5, true},
		{"non-zero fraction rejected", json.Number("1.5"), 0, false},
		{"scientific notation rejected", json.Number("1e3"), 0, false},
		{"scientific notation, negative exponent", json.Number("1e-3"), 0, false},
		{"magnitude beyond int64", json.Number("99999999999999999999"), 0, false},
		{"not a number", json.Number("bogus"), 0, false},
		{"plain negative", json.Number("-5"), -5, true},
		{"float64 compat, whole", float64(5), 5, true},
		{"float64 compat, fraction rejected", 5.5, 0, false},
		{"float64 compat, NaN rejected", math.NaN(), 0, false},
		{"float64 compat, +Inf rejected", math.Inf(1), 0, false},
		{"float64 compat, -Inf rejected", math.Inf(-1), 0, false},
		{"float64 compat, exactly int64 min", float64(math.MinInt64), math.MinInt64, true},
		{"float64 compat, at the +2^63 boundary, rejected", float64(1 << 63), 0, false},
		{"wrong type", "5", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseExactInteger(c.value)
			if ok != c.ok || (ok && got != c.want) {
				t.Errorf("parseExactInteger(%v) = %d, %v; want %d, %v", c.value, got, ok, c.want, c.ok)
			}
		})
	}
}

// TestCheckTypeNumberRejectsScientificNotation is seção 16's other
// 🔴 decision for a declared TypeNumber field -- convert() leaves it
// as json.Number (checkType's own doc comment). Mostly a format check
// (TypeNumber was never promised int64 range, so losing precision
// within float64's finite range is still accepted, seção 10.2), but
// not purely one: a magnitude truly beyond float64's range (not just
// beyond 2^53) is rejected too -- /code-review found that Float64()
// returns +-Inf with a discarded error there, which used to reach
// checkRules' Max (silently skipped, read as "not comparable") and
// concreteNumber (silently stored as a BSON Infinity).
func TestCheckTypeNumberRejectsScientificNotation(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string // "" means valid
	}{
		{"scientific notation rejected", json.Number("1e3"), "must be of number type"},
		{"large whole number accepted", json.Number("9007199254740993"), ""},
		{"decimal accepted", json.Number("1.5"), ""},
		{"beyond float64's finite range rejected", json.Number("1" + strings.Repeat("0", 400)), "must be of number type"},
		{"NaN rejected", math.NaN(), "must be of number type"},
		{"Inf rejected", math.Inf(1), "must be of number type"},
		{"finite float64 accepted", 1.5, ""},
	}
	field := Field{Type: TypeNumber}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := checkType(field, c.value); got != c.want {
				t.Errorf("checkType(%v) = %q, want %q", c.value, got, c.want)
			}
		})
	}
}
