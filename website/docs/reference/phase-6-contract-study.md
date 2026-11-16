---
sidebar_position: 22
---

# Phase 6 — contract study

**Date:** 2026-11-16. **Source baseline:**
`970b7f141a4eb7eb0865865191acd3d4ee53a9b7`, plus the pending documentation
changes. **Result:** study completed; contract corrections and decision acceptance
remain open. ADR-0007 remains **Proposed**. The original study changed no runtime code; the exporter follow-up is recorded below.

## Recommendation

Keep code generation deferred. Prioritize PATCH contract correctness and explicit
server-only constraints. The equivalent YAML/Go fixture compiled to equal runtime
schemas; generation would not repair divergence between the OpenAPI exporter and
runtime validation. Two existing examples do not demonstrate recurring DTO
maintenance cost or justify a production generator.

This is correctness evidence, not a throughput or productivity benchmark. There
is no measured time saving, real AI consumer or approved authorization policy.
The study does not satisfy every Phase 6 completion criterion: contract parity
still has unresolved counterexamples.

## Method and limits

- YAML source: `examples/standalone-tags-api/resources.yaml`, read directly,
  compiled with `declarative.Parse` and `Resource.Build(nil)`: three top-level
  fields, two required, one unique and one default. YAML remains its authoring
  source; the Go setup adds the audit behavior separately.
- Go source: `commentsSchema` extracted verbatim from
  `examples/blog-apis/services/comments-api/comments.go`: four top-level fields,
  three required and one default. Go remains its authoring source. The harness
  does not import or start the application's `main` package.
- These examples do not cover nested objects, lists, nullable required fields or
  callbacks. An explicitly synthetic eight-field `probe` fixture covers those
  cases. Its YAML and manually equivalent Go schemas compare equal using
  `reflect.DeepEqual` before adding Go-only callbacks. This duplicate fixture is
  study instrumentation, not a second production schema to maintain.
- All three resources use isolated in-memory SQLite databases and the real REST
  handler, `Service` and `restsqlite` adapter. HTTP requests run in-process through
  `httptest`; no TCP listener, Mongo, Postgres, Pub/Sub or production database is
  used. The original tags audit hook, comments outbox, auth and bootstrap are not
  exercised. This validates the selected schema, not either service end to end.
- The harness fetches `/api/openapi.json`, compiles the generated Input/Patch
  component using `jsonschema/v6`, then validates the same raw JSON passed to
  HTTP. Successful mutations are read back to check defaults/deletion. PATCH uses
  the latest concrete ETag and `application/merge-patch+json`.
- The harness enables GET/PATCH/PUT item methods and merge-patch content-type
  enforcement uniformly for the experiment. These transport choices are not a
  claim about the unchanged original example configuration.
- JSON Schema defaults are annotations, not normalizers. State-dependent checks
  and extension annotations are not automatically executed by `jsonschema/v6`.

## Observed results

There are 21 mutation cases: 15 agree on acceptance/rejection; six are accepted by
JSON Schema and rejected by HTTP. Eight successful mutations were read back.
Rows run sequentially per resource; rejected writes leave the last successful
state/ETag in place. All mismatches returned 422 with validation issues, not a
missing/stale ETag error.

| Resource | Case | Raw JSON | JSON Schema | HTTP |
|---|---|---|---|---|
| tags | `required missing` | `{}` | reject | 422 |
| tags | `required null` | `{"name":null,"slug":"x"}` | reject | 422 |
| tags | `default applied` | `{"name":"First","slug":"first"}` | accept | 201 |
| tags | `unknown field` | `{"extra":true,"name":"Other","slug":"other"}` | reject | 422 |
| tags | `duplicate unique` | `{"name":"Duplicate","slug":"first"}` | accept | 422 |
| tags | `patch omit required` | `{"enabled":false}` | accept | 200 |
| tags | `patch remove required` | `{"name":null}` | reject | 422 |
| comments | `required missing` | `{}` | reject | 422 |
| comments | `default applied` | `{"author_name":"A","body":"hello","post_id":"p"}` | accept | 201 |
| comments | `status unconstrained` | `{"status":"not-a-real-status"}` | accept | 200 |
| comments | `patch omit required` | `{"body":"edited"}` | accept | 200 |
| probe | `nullable create` | `{"labels":["a"],"name":"one","optional":null,"profile":{"label":"L"},"required_null":null,"rows":[{"label":"R"}]}` | accept | 201 |
| probe | `list wrong item` | `{"labels":[12]}` | reject | 422 |
| probe | `nested required omitted` | `{"profile":{}}` | accept | 422 |
| probe | `list object required omitted` | `{"rows":[{}]}` | accept | 422 |
| probe | `field callback` | `{"checked":"deny"}` | accept | 422 |
| probe | `resource callback` | `{"checked":"resource-deny"}` | accept | 422 |
| probe | `required nullable deletion` | `{"required_null":null}` | accept | 422 |
| probe | `optional null deletion` | `{"optional":null}` | accept | 200 |
| probe | `nullable create omitted required` | `{"name":"two"}` | reject | 422 |
| probe | `nested default` | `{"name":"two","profile":{"label":"L"},"required_null":"v"}` | accept | 201 |

Defaults were persisted as `tags.enabled=true`, `comments.status="pending"`,
`probe.enabled=true` and `probe.profile.active=true`. Omitting required fields
at the top level of PATCH preserved their previous values. Optional nullable
`null` was stored on POST but removed its key on RFC 7396 PATCH. A required
nullable value was allowed on POST; PATCH `null` attempted deletion and failed.

The comments schema accepts `status="not-a-real-status"` both in OpenAPI and
HTTP. Its definition has a default, not an `Allowed` enumeration. This is agreement
with the existing contract, not proof that arbitrary status values meet business
requirements; the owning application must decide whether to constrain them.

## Findings and follow-up

### C6-01 — Nested required fields are relaxed too far in PATCH

**Reproduced contract mismatch.** With a previously stored
`profile={"label":"L","active":true}`, PATCH `{"profile":{}}` is accepted by
the generated Patch schema but returns 422 with a required `profile.label` issue.
Likewise, `{"rows":[{}]}` is schema-valid but fails for missing item `label`.

`rest/openapi.go` passes `partial=true` recursively to dict fields and array
items. `rest/schema.go`'s `validateValue` validates supplied nested dicts with
`partial=false`, including object list items. This difference exists before the
SQLite merge step: the handler's validator rejects the submitted nested value.
The study does not claim it ran the Mongo/Postgres integration paths.

**Recommended correction:** preserve top-level PATCH optionality while matching
actual nested validation. At minimum, list replacement items must retain their
required fields. Add regression cases comparing the generated component to real
validation. Decide explicitly whether the nested-object behavior itself should
change; matching today's contract is the smaller compatibility-preserving choice.
No exporter or runtime correction is implemented in this study.

### C6-02 — Nullable does not describe merge-patch deletion

**Reproduced backend/operation constraint.** A required nullable string accepts
`null` in the component and on POST, but HTTP PATCH returns 422 because RFC 7396
removes the key and `ValidatesMergedDocument()` requires its presence.
An optional nullable field is removed by the same PATCH and succeeds.

**Recommended correction:** document input versus persisted-state semantics by
operation and backend. Consider operation-specific schema/annotations for
merge-patch deletion. Do not emit one universal PATCH null rule: Mongo's legacy
merge semantics differ. The current HTTP behavior should not be weakened to fit
a generic JSON Schema.

### C6-03 — Uniqueness is a server-state constraint

**Expected approximation, reproduced.** Duplicate `tags.slug` passes JSON Schema
but returns 422. `x-hyrule-unique=true` already identifies the rule. JSON Schema
validation alone cannot query existing data or close concurrent insert races.

**Follow-up:** retain the extension and document its server-side nature. Preserve
adapter/database enforcement; generation is not a solution to uniqueness.

### C6-04 — Custom callbacks are server-only

**Expected approximation, reproduced.** `checked="deny"` fails `Field.Check`;
`checked="resource-deny"` fails `Resource.Validate`. Both strings satisfy the
structural schema. `Field.Check` has `x-hyrule-custom-validation=true`; resource-wide
validation has only the generic operation description warning that callbacks and
hooks may add rules, not a corresponding resource-specific marker.

**Recommended correction:** consider an explicit neutral marker for
`Resource.Validate`, and clearly label validation as structural versus authoritative
server validation. Do not serialize Go callbacks or generate client validators
that claim to reproduce them.

### C6-05 — YAML diagnostics can improve before generation

An initial synthetic list fixture used `schema: {type: string}` where Hyrule
requires `items: {type: string}`. Parse failed with line-numbered errors such as
`cannot unmarshal !!str ... into declarative.Field`. This was an invalid harness
fixture, not a valid-resource failure. Correcting it to the documented `items`
syntax allowed the equality check and all cases to run.

**Follow-up:** consider diagnostics naming the resource/field and expected
`items` syntax. The existing YAML parser already gives a location; a new schema
language or generator is not needed merely to improve that message.

## DX evidence and codegen gate

The two real sources own seven top-level field definitions total. Neither source
in this sample also maintains a typed request DTO with the same validation rules.
The probe duplicates eight top-level definitions solely to test equivalence.
The framework's `declarative.Field`/`rest.Field` mapping is implementation
maintenance, not observed repeated consumer DTO work.

There is no sampled schema edit history, measured edit time or multiple independent
consumers repeating DTOs. Do not infer a productivity percentage from field counts.
Revisit generation only when a consumer supplies repeated changes, the duplicated
artifacts and an authoring direction whose maintenance savings can be measured.
Do not generate both YAML and Go as editable sources.

## Verification and next increment

Existing targeted tests passed with Go 1.27.2:

```sh
go test ./declarative ./rest -run 'Test(ParseAndBuild|NestedSchemaAndItems|FileBuild_|Swagger)' -count=1
```

The external harness completed 21 cases with the real SQLite backend and generated
contracts. No permanent test or production code was added. A focused future change
should address C6-01 with meaningful regression tests, document C6-02–04, and then
rerun the study before declaring contract comparison validated. Accepting ADR-0007
and identifying the first real tool consumer remain separate decisions.

## C6-01 follow-up correction — 2026-11-16

The exporter now builds the PATCH schema with full field rules and removes only
root-level `required`. Nested objects and list items retain their required fields.
Runtime validation and response schemas are unchanged. The results above preserve
the original pre-fix measurement.

The permanent `TestSwaggerPatchNestedRequiredMatchesValidation` compares the actual
published OpenAPI component with `ValidateDictPartial` across 12 cases, including
deeper objects, nested lists, null, defaults and omitted root fields. Before the
fix, four subcases failed; afterward, they passed. The 21-case rerun confirmed both
C6-01 cases are now rejected by JSON Schema, matching HTTP 422. Exactly four
expected differences remain: uniqueness, two callbacks and required nullable
deletion through merge patch (C6-02–04).
`go test ./rest ./declarative ./restsqlite -count=1` also passed. Suites requiring
external connection variables may skip their cases; this does not claim
Mongo/Postgres integration coverage.

ADR acceptance and consumer decisions remain open; fixing the exporter does not
implement code generation, a tool catalog or MCP.

## C6-04 follow-up — 2026-11-16

The exporter now marks `Resource.Validate` with
`x-hyrule-custom-validation: true` at the object level in Input and Patch.
`Field.Check` remains marked at the property level. Response components do not
gain the resource marker. Tests cover no callbacks, field-only, resource-only
and both, and verify that contract generation and structural validation never
execute callbacks.

This improves contract clarity without eliminating the four expected harness
differences: the server remains authoritative for uniqueness, callbacks and
required-key deletion through merge patch. C6-02–03 are documented limits;
C6-05 remains a DX suggestion, without YAML parser changes in this round.
Codegen remains deferred; ADR acceptance and an AI consumer remain open.

## Reproduction harness

From the repository root, save the following Go block as
`/tmp/hyrule-phase6-study/main.go` (create the directory first). Extract the
unchanged Go source definition and run both files using the root module's pinned
dependencies:

```sh
mkdir -p /tmp/hyrule-phase6-study
python3 - <<'EXTRACT'
from pathlib import Path
s = Path('examples/blog-apis/services/comments-api/comments.go').read_text()
a = s.index('var commentsSchema = rest.Schema{')
b = s.index('\n}', a) + 2
Path('/tmp/hyrule-phase6-study/comments.go').write_text(
    'package main\nimport "github.com/Gui-Rondon/hyrule/rest"\n' + s[a:b] + '\n')
EXTRACT
go run /tmp/hyrule-phase6-study/main.go /tmp/hyrule-phase6-study/comments.go
```

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"

	"github.com/Gui-Rondon/hyrule/declarative"
	"github.com/Gui-Rondon/hyrule/platform/sqlite"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restsqlite"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type result struct {
	Resource, Case, Method string
	Payload                any
	SchemaAccept           bool
	HTTP                   int
	Body                   any
}

var results []result

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func decode(s string) map[string]any {
	var m map[string]any
	must(json.Unmarshal([]byte(s), &m))
	return m
}
func request(api *rest.API, method, path, payload, etag string) (int, map[string]any) {
	r := httptest.NewRequest(method, path, strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/merge-patch+json")
	if etag != "" {
		r.Header.Set("If-Match", `"`+etag+`"`)
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	var out map[string]any
	must(json.Unmarshal(w.Body.Bytes(), &out))
	return w.Code, out
}
func schemaFor(doc map[string]any, resource, kind string) *jsonschema.Schema {
	paths := doc["paths"].(map[string]any)
	op := paths["/"+resource].(map[string]any)["post"].(map[string]any)
	ref := op["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["oneOf"].([]any)[0].(map[string]any)["$ref"].(string)
	ref = strings.TrimSuffix(ref, "Input") + kind
	c := jsonschema.NewCompiler()
	must(c.AddResource("urn:study", doc))
	s, err := c.Compile("urn:study" + ref)
	must(err)
	return s
}
func run(res *rest.Resource, cases []struct{ name, method, body string }) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:")
	must(err)
	defer db.Close()
	adapter, err := restsqlite.NewAdapter(ctx, db, res.Name, res)
	must(err)
	res.Adapter = adapter
	res.ItemMethods = []string{"GET", "PATCH", "PUT"}
	res.RequireMergePatchContentType = true
	api := rest.New(rest.Options{Transactor: sqlite.Transactor{DB: db}})
	api.Register(res)
	_, doc := request(api, "GET", "/api/openapi.json", "", "")
	b, _ := json.MarshalIndent(doc, "", "  ")
	must(os.WriteFile("/tmp/hyrule-phase6-study/"+res.Name+"-openapi.json", b, 0600))
	input, patch := schemaFor(doc, res.Name, "Input"), schemaFor(doc, res.Name, "Patch")
	var id, etag string
	for _, tc := range cases {
		path := "/" + res.Name
		s := input
		if tc.method == "PATCH" {
			path += "/" + id
			s = patch
		}
		payload := decode(tc.body)
		accepted := s.Validate(payload) == nil
		code, body := request(api, tc.method, path, tc.body, etag)
		results = append(results, result{res.Name, tc.name, tc.method, payload, accepted, code, body})
		if code == 201 || code == 200 {
			if v, ok := body["_id"].(string); ok {
				id = v
			}
			if v, ok := body["_etag"].(string); ok {
				etag = v
			}
		}
		if code == 201 || code == 200 {
			status, saved := request(api, "GET", "/"+res.Name+"/"+id, "", "")
			if status != 200 {
				panic("readback failed")
			}
			results = append(results, result{Resource: res.Name, Case: tc.name + " persisted", Method: "GET", HTTP: 200, Body: saved})
		}
	}
}

const extendedYAML = `resources:
- name: probe
  collection: probe
  resource_methods: [get, post]
  item_methods: [get, patch, put]
  schema:
    name: {type: string, required: true, unique: true}
    enabled: {type: boolean, default: true}
    optional: {type: string, nullable: true}
    required_null: {type: string, required: true, nullable: true}
    profile:
      type: dict
      schema:
        label: {type: string, required: true}
        active: {type: boolean, default: true}
    rows:
      type: list
      items:
        type: dict
        schema:
          label: {type: string, required: true}
    labels:
      type: list
      items: {type: string}
    checked: {type: string}
`

func main() {
	// Verify that the separately compiled Go definition was extracted unchanged.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "examples/blog-apis/services/comments-api/comments.go", nil, 0)
	must(err)
	var actual string
	ast.Inspect(f, func(n ast.Node) bool {
		if v, ok := n.(*ast.ValueSpec); ok && len(v.Names) == 1 && v.Names[0].Name == "commentsSchema" {
			var b bytes.Buffer
			must(format.Node(&b, fset, v.Values[0]))
			actual = b.String()
		}
		return true
	})
	must(os.WriteFile("/tmp/hyrule-phase6-study/comments-schema-source.txt", []byte(actual), 0600))
	fmt.Println("Go comments schema fields:", len(commentsSchema))
	data, err := os.ReadFile("examples/standalone-tags-api/resources.yaml")
	must(err)
	file, err := declarative.Parse(bytes.NewReader(data))
	must(err)
	tags, err := file.Resources[0].Build(nil)
	must(err)
	run(tags, []struct{ name, method, body string }{
		{"required missing", "POST", `{}`}, {"required null", "POST", `{"name":null,"slug":"x"}`},
		{"default applied", "POST", `{"name":"First","slug":"first"}`},
		{"unknown field", "POST", `{"name":"Other","slug":"other","extra":true}`},
		{"duplicate unique", "POST", `{"name":"Duplicate","slug":"first"}`},
		{"patch omit required", "PATCH", `{"enabled":false}`},
		{"patch remove required", "PATCH", `{"name":null}`},
	})
	run(&rest.Resource{Name: "comments", Schema: commentsSchema, ResourceMethods: []string{"GET", "POST"}}, []struct{ name, method, body string }{
		{"required missing", "POST", `{}`}, {"default applied", "POST", `{"post_id":"p","author_name":"A","body":"hello"}`},
		{"status unconstrained", "PATCH", `{"status":"not-a-real-status"}`},
		{"patch omit required", "PATCH", `{"body":"edited"}`},
	})
	file, err = declarative.Parse(strings.NewReader(extendedYAML))
	must(err)
	probe, err := file.Resources[0].Build(nil)
	must(err)
	reference := rest.Schema{
		"name": {Type: rest.TypeString, Required: true, Unique: true}, "enabled": {Type: rest.TypeBoolean, Default: true},
		"optional": {Type: rest.TypeString, Nullable: true}, "required_null": {Type: rest.TypeString, Required: true, Nullable: true},
		"profile": {Type: rest.TypeDict, Schema: rest.Schema{"label": {Type: rest.TypeString, Required: true}, "active": {Type: rest.TypeBoolean, Default: true}}},
		"rows":    {Type: rest.TypeList, Items: &rest.Field{Type: rest.TypeDict, Schema: rest.Schema{"label": {Type: rest.TypeString, Required: true}}}},
		"labels":  {Type: rest.TypeList, Items: &rest.Field{Type: rest.TypeString}}, "checked": {Type: rest.TypeString},
	}
	equal := reflect.DeepEqual(probe.Schema, reference)
	fmt.Println("Extended YAML/Go runtime schemas equal:", equal)
	if !equal {
		panic("schema mismatch")
	}
	field := probe.Schema["checked"]
	field.Check = func(_ context.Context, v any, _ map[string]any) any {
		if v == "deny" {
			return "blocked by Field.Check"
		}
		return nil
	}
	probe.Schema["checked"] = field
	probe.Validate = func(_ context.Context, doc, original rest.Doc, partial bool) (rest.Issues, error) {
		if doc["checked"] == "resource-deny" {
			return rest.Issues{"checked": "blocked by Resource.Validate"}, nil
		}
		return nil, nil
	}
	run(probe, []struct{ name, method, body string }{
		{"nullable create", "POST", `{"name":"one","required_null":null,"optional":null,"profile":{"label":"L"},"rows":[{"label":"R"}],"labels":["a"]}`},
		{"list wrong item", "PATCH", `{"labels":[12]}`},
		{"nested required omitted", "PATCH", `{"profile":{}}`},
		{"list object required omitted", "PATCH", `{"rows":[{}]}`},
		{"field callback", "PATCH", `{"checked":"deny"}`},
		{"resource callback", "PATCH", `{"checked":"resource-deny"}`},
		{"required nullable deletion", "PATCH", `{"required_null":null}`},
		{"optional null deletion", "PATCH", `{"optional":null}`},
		{"nullable create omitted required", "POST", `{"name":"two"}`},
		{"nested default", "POST", `{"name":"two","required_null":"v","profile":{"label":"L"}}`},
	})
	output, err := json.MarshalIndent(results, "", "  ")
	must(err)
	must(os.WriteFile("/tmp/hyrule-phase6-study/results.json", output, 0600))
	for _, r := range results {
		if r.Method != "GET" {
			fmt.Printf("%s | %s | schema=%t | HTTP=%d\n", r.Resource, r.Case, r.SchemaAccept, r.HTTP)
		}
	}
}
```
