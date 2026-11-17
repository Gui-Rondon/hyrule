package taglookup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func compileContract(t *testing.T, schema map[string]any) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("urn:tags-contract", schema); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("urn:tags-contract")
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}
func TestDiscoverySchemaMatchesInputParser(t *testing.T) {
	r := setup(t, nil)
	contract, err := r.e.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	schema := compileContract(t, contract.InputSchema)
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"normal", `{"slug":"go"}`, true}, {"minimum", `{"slug":"a"}`, true}, {"maximum", `{"slug":"` + strings.Repeat("a", 128) + `"}`, true},
		{"allowed characters", `{"slug":"Go_123-abc"}`, true}, {"too long", `{"slug":"` + strings.Repeat("a", 129) + `"}`, false},
		{"empty", `{"slug":""}`, false}, {"space", `{"slug":"a b"}`, false}, {"final newline", `{"slug":"go\n"}`, false},
		{"unicode", `{"slug":"ação"}`, false}, {"number", `{"slug":1}`, false}, {"null", `{"slug":null}`, false},
		{"missing", `{}`, false}, {"extra", `{"slug":"go","where":{}}`, false}, {"array", `["go"]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(tc.body), &value); err != nil {
				t.Fatal(err)
			}
			schemaValid := schema.Validate(value) == nil
			_, parseErr := parse(strings.NewReader(tc.body))
			runtimeValid := parseErr == nil
			if schemaValid != tc.valid || runtimeValid != tc.valid {
				t.Fatalf("schema=%t runtime=%t want=%t", schemaValid, runtimeValid, tc.valid)
			}
		})
	}
	// Duplicate keys and total JSON bytes are transport/server rules, not claims
	// that structural validation alone can enforce.
	for _, body := range []string{`{"slug":"go","slug":"a"}`, strings.Repeat(" ", MaxInput) + `{"slug":"go"}`} {
		var value any
		if err := json.Unmarshal([]byte(body), &value); err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(value); err != nil {
			t.Fatal(err)
		}
		if _, err := parse(strings.NewReader(body)); err != ErrInput {
			t.Fatal("server-only rule not enforced")
		}
	}
	if r.a.reads != 0 {
		t.Fatal("discovery accessed storage")
	}
}
func TestDiscoveryOutputSchemaMatchesLookup(t *testing.T) {
	r := setup(t, nil)
	contract, err := r.e.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	schema := compileContract(t, contract.OutputSchema)
	for _, slug := range []string{"go", "hidden", "missing"} {
		raw, err := r.e.Execute(context.Background(), strings.NewReader(`{"slug":"`+slug+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err = json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		if err = schema.Validate(value); err != nil {
			t.Fatalf("%s response mismatches schema: %v", slug, err)
		}
	}
	for _, body := range []string{
		`{}`, `{"found":true}`, `{"found":false,"tag":{}}`,
		`{"found":true,"tag":{"name":"Go","slug":"go","enabled":false}}`,
		`{"found":true,"tag":{"name":"Go","slug":"go","enabled":true,"_id":"private"}}`,
		`{"found":true,"tag":{"name":42,"slug":"go","enabled":true}}`,
		`{"found":true,"tag":{"name":"Go","slug":"go\n","enabled":true}}`,
	} {
		var value any
		if err = json.Unmarshal([]byte(body), &value); err != nil {
			t.Fatal(err)
		}
		if err = schema.Validate(value); err == nil {
			t.Fatalf("invalid output accepted: %s", body)
		}
	}
}
func TestDiscoveryAuthorizationAuditAndIsolation(t *testing.T) {
	r := setup(t, nil)
	r.d.Verify = func(context.Context) (Principal, error) { return Principal{}, errors.New("secret credential") }
	r.rebuild(t)
	contract, err := r.e.Discover(context.Background())
	if err != ErrIdentity || !reflect.DeepEqual(contract, Contract{}) || r.a.reads != 0 {
		t.Fatal("unauthorized discovery exposed metadata")
	}
	r = setup(t, nil)
	r.d.Authorize = func(context.Context, Principal, Action) error { return ErrForbidden }
	r.rebuild(t)
	contract, err = r.e.Discover(context.Background())
	if err != ErrForbidden || !reflect.DeepEqual(contract, Contract{}) {
		t.Fatal("forbidden discovery exposed metadata")
	}
	r = setup(t, nil)
	r.d.Audit = func(context.Context, Record) error { return ErrAudit }
	r.rebuild(t)
	contract, err = r.e.Discover(context.Background())
	if !errors.Is(err, ErrAudit) || !reflect.DeepEqual(contract, Contract{}) {
		t.Fatal("audit failure exposed metadata")
	}
	r = setup(t, func(res *rest.Resource) {
		f := res.Schema["slug"]
		f.Check = func(context.Context, any, rest.Doc) any { panic("discovery ran write validator") }
		res.Schema["slug"] = f
		res.Validate = func(context.Context, rest.Doc, rest.Doc, bool) (rest.Issues, error) {
			panic("discovery ran resource validation")
		}
	})
	first, err := r.e.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := first.Hash
	first.InputSchema["properties"].(map[string]any)["slug"].(map[string]any)["type"] = "number"
	first.ServerRules[0] = "modified"
	second, err := r.e.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Hash != before || second.InputSchema["properties"].(map[string]any)["slug"].(map[string]any)["type"] != "string" || second.ServerRules[0] == "modified" {
		t.Fatal("caller mutation changed shared discovery metadata")
	}
	hash := second.Hash
	second.Hash = ""
	data, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if hash != hex.EncodeToString(sum[:]) {
		t.Fatal("non-deterministic contract hash")
	}
	if strings.Contains(string(data), "reader") || strings.Contains(string(data), r.d.Scope) || strings.Contains(string(data), "_id") {
		t.Fatal("private caller/resource metadata exposed")
	}
	if r.a.reads != 0 || len(r.records) != 2 {
		t.Fatal("discovery read data or missed audit")
	}
}
func TestHTTPDiscoveryContractsValidateInvocation(t *testing.T) {
	r, h, token, _ := httpPilot(t)
	get := httptest.NewRequest("GET", HTTPPath, nil)
	get.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, get)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var contract Contract
	if err := json.Unmarshal(w.Body.Bytes(), &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Name != Name || contract.Hash == "" || contract.Limits.InputBytes != MaxInput || contract.Limits.OutputBytes != MaxOutput || contract.Limits.Results != 1 || len(contract.ServerRules) == 0 {
		t.Fatal("incomplete discovery")
	}
	if r.a.reads != 0 {
		t.Fatal("GET discovery read storage")
	}
	input := compileContract(t, contract.InputSchema)
	output := compileContract(t, contract.OutputSchema)
	if err := input.Validate(map[string]any{"slug": "go"}); err != nil {
		t.Fatal(err)
	}
	post := httptest.NewRequest("POST", HTTPPath, strings.NewReader(`{"slug":"go"}`))
	post.Header.Set("Authorization", "Bearer "+token)
	post.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, post)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var value any
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if err := output.Validate(value); err != nil {
		t.Fatal(err)
	}
	if len(r.records) != 2 {
		t.Fatal("discovery/invocation not independently audited")
	}
}
