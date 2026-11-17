package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/Gui-Rondon/hyrule/platform/auth"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func docsRequest(a http.Handler, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
	return w
}
func docsJSON(t *testing.T, a http.Handler) map[string]any {
	t.Helper()
	w := docsRequest(a, "/api/openapi.json")
	if w.Code != 200 {
		t.Fatalf("docs status=%d: %s", w.Code, w.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}
func docsMap(v any) map[string]any { return v.(map[string]any) }
func docsOperation(doc map[string]any, route, method string) map[string]any {
	return docsMap(docsMap(docsMap(doc["paths"])[route])[method])
}

// Validate against the official OpenAPI 3.1 document schema, then compile
// each component as JSON Schema 2020-12 (the document schema leaves Schema
// Objects open). All schemas are local; the tests never fetch URLs.
func validateOpenAPI(t *testing.T, doc map[string]any) {
	t.Helper()
	data, err := os.ReadFile("testdata/openapi-3.1-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var meta any
	if err = json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	const schemaURL = "https://spec.openapis.org/oas/3.1/schema/2022-10-07"
	if err = c.AddResource(schemaURL, meta); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Validate(doc); err != nil {
		t.Fatalf("invalid OpenAPI: %v", err)
	}
	c = jsonschema.NewCompiler()
	if err = c.AddResource("urn:hyrule:test", doc); err != nil {
		t.Fatal(err)
	}
	for key := range docsMap(docsMap(doc["components"])["schemas"]) {
		if _, err = c.Compile("urn:hyrule:test#/components/schemas/" + key); err != nil {
			t.Fatalf("invalid component %s: %v", key, err)
		}
	}
	// Resolve every generated component reference and check operation IDs.
	ids := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if ref, ok := x["$ref"].(string); ok {
				key := strings.TrimPrefix(ref, "#/components/schemas/")
				if docsMap(docsMap(doc["components"])["schemas"])[key] == nil {
					t.Errorf("unresolved reference %s", ref)
				}
			}
			if id, ok := x["operationId"].(string); ok {
				if ids[id] {
					t.Errorf("duplicate operationId %s", id)
				}
				ids[id] = true
			}
			for _, value := range x {
				walk(value)
			}
		case []any:
			for _, value := range x {
				walk(value)
			}
		}
	}
	walk(doc)
}

func TestSwaggerEnabledByDefaultAndHiddenResourcesRemainActive(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprint(disabled), func(t *testing.T) {
			a := New(Options{})
			r := &Resource{Name: "articles", Adapter: &fakeRetryAdapter{doc: Doc{"_id": "fake-id", "_etag": "1", "name": "Link"}}, Schema: Schema{"name": {Type: TypeString, Required: true}}}
			if disabled {
				r.DisableSwagger = true
			} // Default case deliberately omits it.
			a.Register(r)
			doc := docsJSON(t, a)
			validateOpenAPI(t, doc)
			paths := docsMap(doc["paths"])
			schemas := docsMap(docsMap(doc["components"])["schemas"])
			if disabled {
				if len(paths) != 0 || len(schemas) != 0 {
					t.Fatalf("hidden resource leaked: %v", doc)
				}
			} else if paths["/articles"] == nil || paths["/articles/{lookup}"] == nil || len(schemas) == 0 {
				t.Fatalf("default resource missing: %v", doc)
			}
			if w := docsRequest(a, "/articles/fake-id"); w.Code != 200 || !strings.Contains(w.Body.String(), "Link") {
				t.Fatalf("resource read changed: %d %s", w.Code, w.Body.String())
			}
			// Hiding docs does not remove a resource from the API home listing.
			if w := docsRequest(a, "/"); !strings.Contains(w.Body.String(), "articles") {
				t.Fatal("home listing changed")
			}
		})
	}
}

func TestSwaggerHTTPAndProxyRelativeURLs(t *testing.T) {
	a := New(Options{})
	for _, route := range []string{"/api/docs", "/api/docs/", "/api/openapi.json", "/api/docs/assets/swagger-ui-bundle.js", "/api/docs/assets/swagger-ui.css"} {
		w := docsRequest(a, route)
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("%s: %d", route, w.Code)
		}
		if strings.Contains(route, "assets") && strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("asset is HTML: %s", route)
		}
	}
	if w := docsRequest(a, "/api/docs/assets/missing.js"); w.Code != 404 {
		t.Fatalf("unknown asset=%d", w.Code)
	}
	html := docsRequest(a, "/api/docs").Body.String()
	for _, want := range []string{"validatorUrl: null", "../openapi.json", "assets/swagger-ui-bundle.js", "window.location.pathname"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing UI config %s", want)
		}
	}
	if strings.Contains(html, "https://") {
		t.Fatal("UI requires remote assets")
	}
	for _, route := range []string{"/swagger", "/swagger/"} {
		w := docsRequest(a, route)
		external, _ := url.Parse("https://example.test/prefix" + route)
		location, err := url.Parse(w.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		if w.Code != 307 || external.ResolveReference(location).Path != "/prefix/api/docs" {
			t.Fatalf("bad proxy redirect: %s %d %s", route, w.Code, location)
		}
	}
	specURL, _ := url.Parse("https://example.test/prefix/api/openapi.json")
	serverURL, _ := url.Parse(docsMap(docsJSON(t, a)["servers"].([]any)[0])["url"].(string))
	if specURL.ResolveReference(serverURL).Path != "/prefix/" {
		t.Fatal("server URL loses prefix")
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("POST", "/api/docs", nil))
	if w.Code < 400 {
		t.Fatalf("docs POST unexpectedly succeeded: %d", w.Code)
	}
	validateOpenAPI(t, docsJSON(t, a))
}

func TestSwaggerContractAndSchemas(t *testing.T) {
	min, max := 1.0, 10.0
	a := New(Options{})
	a.Register(&Resource{Name: "articles", URL: "v1/articles", Adapter: &mongoAdapter{}, SoftDelete: true,
		ResourceMethods: []string{"GET", "POST", "DELETE"}, ItemMethods: []string{"GET", "PATCH", "PUT", "DELETE", "POST"}, RequireMergePatchContentType: true,
		AdditionalLookup: &AdditionalLookup{Field: "slug", Pattern: regexp.MustCompile(`^[\w-]+$`)},
		Schema: Schema{
			"name":    {Type: TypeString, Required: true, MinLength: 2, MaxLength: 30, Regex: regexp.MustCompile(`^\w+$`), Unique: true},
			"state":   {Type: TypeString, Allowed: []any{"draft", "public"}, Nullable: true},
			"tags":    {Type: TypeList, Items: &Field{Type: TypeString}, Allowed: []any{"go"}, NotEmpty: true, MaxLength: 4},
			"score":   {Type: TypeNumber, Min: &min, Max: &max},
			"enabled": {Type: TypeBoolean, Required: true, Default: true},
			"parent":  {Type: TypeObjectID}, "date": {Type: TypeDatetime},
			"profile": {Type: TypeDict, Schema: Schema{"title": {Type: TypeString, Required: true}}},
			"untyped": {}, "integer": {Type: TypeInteger}, "empty": {Type: TypeString, AllowEmpty: true, MinLength: 3, Allowed: []any{"x"}},
			"checked": {Type: TypeString, Check: func(context.Context, any, Doc) any { return nil }},
		}})
	doc := docsJSON(t, a)
	validateOpenAPI(t, doc)
	paths := docsMap(doc["paths"])
	if len(docsMap(paths["/v1/articles"])) != 2 || len(docsMap(paths["/v1/articles/{lookup}"])) != 4 {
		t.Fatal("unsupported methods documented")
	}
	get := docsOperation(doc, "/v1/articles", "get")
	params := get["parameters"].([]any)
	names := map[string]bool{}
	for _, p := range params {
		names[docsMap(p)["name"].(string)] = true
	}
	for _, name := range []string{"where", "sort", "projection", "page", "max_results", "show_deleted"} {
		if !names[name] {
			t.Errorf("missing %s", name)
		}
	}
	item := docsOperation(doc, "/v1/articles/{lookup}", "get")
	if !strings.Contains(docsMap(item["parameters"].([]any)[0])["description"].(string), "slug") || docsMap(item["responses"])["304"] == nil {
		t.Fatal("lookup or conditional reads missing")
	}
	patch := docsOperation(doc, "/v1/articles/{lookup}", "patch")
	responses := docsMap(patch["responses"])
	for _, status := range []string{"200", "412", "428", "415", "422"} {
		if responses[status] == nil {
			t.Errorf("missing PATCH %s", status)
		}
	}
	content := docsMap(docsMap(patch["requestBody"])["content"])
	if content[mergePatchContentType] == nil || content["application/json"] != nil {
		t.Fatal("wrong strict PATCH content types")
	}
	schemas := docsMap(docsMap(doc["components"])["schemas"])
	for key, v := range schemas {
		s := docsMap(v)
		props := docsMap(s["properties"])
		switch {
		case strings.HasSuffix(key, "Input"):
			if !reflect.DeepEqual(s["required"], []any{"name"}) {
				t.Fatalf("input required=%v", s["required"])
			}
			if props["_id"] != nil {
				t.Fatal("output metadata in input")
			}
			if docsMap(props["score"])["minimum"] != 1.0 || docsMap(props["score"])["maximum"] != 10.0 {
				t.Fatal("numeric bounds lost")
			}
			if !reflect.DeepEqual(docsMap(props["state"])["type"], []any{"string", "null"}) || !reflect.DeepEqual(docsMap(props["state"])["enum"], []any{"draft", "public", nil}) {
				t.Fatal("nullable enum lost")
			}
			if docsMap(props["name"])["x-hyrule-regex"] == nil || docsMap(props["name"])["x-hyrule-unique"] != true {
				t.Fatal("server rules lost")
			}
			tags := docsMap(props["tags"])
			if tags["minItems"] != 1.0 || tags["maxItems"] != 4.0 || docsMap(tags["items"])["enum"] == nil {
				t.Fatal("list rules lost")
			}
		case strings.HasSuffix(key, "Patch"):
			if s["required"] != nil {
				t.Fatal("PATCH requires omitted root fields")
			}
			if !reflect.DeepEqual(docsMap(props["profile"])["required"], []any{"title"}) {
				t.Fatal("PATCH omits required fields in supplied nested objects")
			}
		case strings.HasSuffix(key, "Document"):
			if props["_id"] == nil || props["_deleted"] == nil || props["_etag"] == nil {
				t.Fatal("response metadata missing")
			}
			if docsMap(props["profile"])["required"] != nil {
				t.Fatal("response schema unexpectedly requires nested input fields")
			}
		}
	}
	post := docsOperation(doc, "/v1/articles", "post")
	if !strings.Contains(fmt.Sprint(post["requestBody"]), "oneOf") || !strings.Contains(fmt.Sprint(docsMap(post["responses"])["201"]), "_items") {
		t.Fatal("batch POST missing")
	}
	if _, ok := docsOperation(doc, "/v1/articles/{lookup}", "delete")["requestBody"]; ok {
		t.Fatal("DELETE has body")
	}
}

func TestSwaggerAuthenticationAndPaginationVariants(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, pagination := range []Pagination{{}, {Disabled: true}, {OptimizeForSpeed: true}} {
			authn, err := auth.New(auth.Config{Enabled: enabled}, nil)
			if err != nil {
				t.Fatal(err)
			}
			a := New(Options{Auth: authn})
			a.Register(&Resource{Name: "items", Adapter: &mongoAdapter{}, PublicMethods: []string{"GET"}, Pagination: pagination, ResourceMethods: []string{"GET", "POST"}, ItemMethods: []string{"GET", "PATCH"}})
			doc := docsJSON(t, a)
			validateOpenAPI(t, doc)
			list, post, item := docsOperation(doc, "/items", "get"), docsOperation(doc, "/items", "post"), docsOperation(doc, "/items/{lookup}", "get")
			if len(list["security"].([]any)) != 0 {
				t.Fatal("public GET protected")
			}
			if (len(post["security"].([]any)) > 0) != enabled || (len(item["security"].([]any)) > 0) != enabled {
				t.Fatal("operation security incorrect")
			}
			patch := docsOperation(doc, "/items/{lookup}", "patch")
			content := docsMap(docsMap(patch["requestBody"])["content"])
			if content["application/json"] == nil || content[mergePatchContentType] == nil {
				t.Fatal("permissive PATCH types missing")
			}
			raw := fmt.Sprint(list)
			if strings.Contains(raw, "name:page") == pagination.Disabled {
				t.Fatal("pagination parameters incorrect")
			}
			if strings.Contains(raw, "_meta") == pagination.Disabled {
				t.Fatal("pagination envelope incorrect")
			}
			if strings.Contains(raw, "X-Total-Count") == (pagination.OptimizeForSpeed && !pagination.Disabled) {
				t.Fatal("count header incorrect")
			}
			if docsRequest(a, "/api/docs").Code != 200 {
				t.Fatal("docs protected")
			}
		}
	}
}

func TestSwaggerAdditionalLookupAndStableIdentifiers(t *testing.T) {
	a := New(Options{})
	for _, r := range []*Resource{
		{Name: "a/b", URL: "one", Adapter: &mongoAdapter{}, ItemMethods: []string{}, AdditionalLookup: &AdditionalLookup{Field: "slug", Pattern: regexp.MustCompile(`.+`)}},
		{Name: "a_b", URL: "two", Adapter: &mongoAdapter{}, ResourceMethods: []string{}, ItemMethods: []string{}},
	} {
		a.Register(r)
	}
	a.Handle("GET /custom", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	doc := docsJSON(t, a)
	validateOpenAPI(t, doc)
	item := docsOperation(doc, "/one/{lookup}", "get")
	if !strings.Contains(docsMap(item["parameters"].([]any)[0])["description"].(string), "not IDs") {
		t.Fatal("lookup-only GET undocumented")
	}
	if docsMap(doc["paths"])["/custom"] != nil || docsMap(doc["paths"])["/two"] != nil {
		t.Fatal("non-resource or disabled method in spec")
	}
	before := docsRequest(a, "/api/openapi.json").Body.String()
	if after := docsRequest(a, "/api/openapi.json").Body.String(); after != before {
		t.Fatal("unstable document")
	}
}

func TestSwaggerReservedRouteCollisions(t *testing.T) {
	for _, route := range []string{"/api/docs", "/api/docs/assets/x", "GET /api/openapi.json", "POST /swagger", "/api/{lookup}", "/api/{rest...}", "example.test/api/docs", "GET /api/%64ocs"} {
		t.Run(route, func(t *testing.T) {
			a := New(Options{})
			defer func() {
				if p := recover(); p == nil || !strings.Contains(fmt.Sprint(p), "reserved Swagger") {
					t.Errorf("missing clear collision: %v", p)
				}
			}()
			a.Handle(route, http.NotFoundHandler())
		})
	}
	for _, name := range []string{"api", "swagger", "api/docs"} {
		t.Run("resource "+name, func(t *testing.T) {
			a := New(Options{})
			defer func() {
				if recover() == nil {
					t.Error("resource collision accepted")
				}
				if len(a.services) != 0 {
					t.Error("partial registration")
				}
			}()
			a.Register(&Resource{Name: name, Adapter: &mongoAdapter{}})
		})
	}
	// Parent handlers and ordinary custom routes still work.
	a := New(Options{})
	a.Handle("/api/", http.NotFoundHandler())
	a.Handle("GET /items/{id}/publish", http.NotFoundHandler())
	if docsRequest(a, "/api/docs").Code != 200 {
		t.Fatal("parent handler shadows docs")
	}
	for _, pattern := range []string{"", "/api/docs/assets/{file}", "/a/b/c"} {
		_ = swaggerPatternMatches(pattern, "/a")
	}
	checkSwaggerRoute("")
}

// Exercise accepted/rejected payloads, rather than only schema shape.
func TestSwaggerFieldSchemaMatchesEmptyAndNullRules(t *testing.T) {
	for _, tc := range []struct {
		name           string
		field          Field
		valid, invalid []any
	}{
		{"string", Field{Type: TypeString, AllowEmpty: true, MinLength: 3, Allowed: []any{"long"}}, []any{"", "long"}, []any{"x", "other", nil}},
		{"list", Field{Type: TypeList, AllowEmpty: true, MinLength: 2, Items: &Field{Type: TypeString}, Allowed: []any{"a"}}, []any{[]any{}, []any{"a", "a"}}, []any{[]any{"a"}, []any{"x", "x"}}},
		{"notEmpty wins", Field{Type: TypeString, AllowEmpty: true, NotEmpty: true}, []any{"x"}, []any{""}},
		{"untyped", Field{}, []any{"x", true, 1.0}, []any{nil}},
		{"nullable untyped", Field{Nullable: true}, []any{nil, "x"}, nil},
		{"dict without schema", Field{Type: TypeDict}, []any{map[string]any{"anything": true}}, []any{"x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := jsonschema.NewCompiler()
			if err := c.AddResource("urn:field", openAPIField(tc.field, false)); err != nil {
				t.Fatal(err)
			}
			s, err := c.Compile("urn:field")
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range tc.valid {
				if err = s.Validate(v); err != nil {
					t.Errorf("rejected %#v: %v", v, err)
				}
			}
			for _, v := range tc.invalid {
				if err = s.Validate(v); err == nil {
					t.Errorf("accepted %#v", v)
				}
			}
		})
	}
}

// PATCH only relaxes required fields at the document root. Supplied nested
// objects and replacement list items still go through full runtime validation.
func TestSwaggerPatchNestedRequiredMatchesValidation(t *testing.T) {
	schema := Schema{
		"name": {Type: TypeString, Required: true},
		"profile": {Type: TypeDict, Required: true, Nullable: true, Schema: Schema{
			"label":  {Type: TypeString, Required: true},
			"active": {Type: TypeBoolean, Required: true, Default: true},
			"detail": {Type: TypeDict, Schema: Schema{
				"code": {Type: TypeString, Required: true},
			}},
		}},
		"rows": {Type: TypeList, Items: &Field{Type: TypeDict, Schema: Schema{
			"label": {Type: TypeString, Required: true},
		}}},
		"matrix": {Type: TypeList, Items: &Field{Type: TypeList, Items: &Field{
			Type: TypeDict, Schema: Schema{"label": {Type: TypeString, Required: true}},
		}}},
	}
	a := New(Options{})
	a.Register(&Resource{Name: "items", Adapter: &mongoAdapter{}, Schema: schema,
		ResourceMethods: []string{"GET", "POST"}, ItemMethods: []string{"GET", "PATCH"}})
	doc := docsJSON(t, a)
	validateOpenAPI(t, doc)
	operation := docsOperation(doc, "/items/{lookup}", "patch")
	content := docsMap(docsMap(operation["requestBody"])["content"])
	ref := docsMap(docsMap(content["application/json"])["schema"])["$ref"].(string)
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("urn:patch-regression", doc); err != nil {
		t.Fatal(err)
	}
	contract, err := compiler.Compile("urn:patch-regression" + ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"omit root required", `{}`, true},
		{"root partial update", `{"name":"new"}`, true},
		{"missing nested required", `{"profile":{}}`, false},
		{"required with default omitted", `{"profile":{"label":"L"}}`, true},
		{"nullable object", `{"profile":null}`, true},
		{"missing deep required", `{"profile":{"label":"L","detail":{}}}`, false},
		{"valid deep object", `{"profile":{"label":"L","detail":{"code":"C"}}}`, true},
		{"missing list item required", `{"rows":[{}]}`, false},
		{"valid list item", `{"rows":[{"label":"R"}]}`, true},
		{"empty list", `{"rows":[]}`, true},
		{"missing nested list item required", `{"matrix":[[{}]]}`, false},
		{"valid nested list item", `{"matrix":[[{"label":"M"}]]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload map[string]any
			if err := json.Unmarshal([]byte(tc.body), &payload); err != nil {
				t.Fatal(err)
			}
			contractErr := contract.Validate(payload)
			issues := ValidateDictPartial(context.Background(), schema, payload, false)
			if got := len(issues) == 0; got != tc.valid {
				t.Fatalf("runtime acceptance=%t, want %t: %v", got, tc.valid, issues)
			}
			if got := contractErr == nil; got != tc.valid {
				t.Errorf("contract acceptance=%t, want %t (runtime issues=%v): %v", got, tc.valid, issues, contractErr)
			}
		})
	}
}

func TestSwaggerDistinguishesFieldAndResourceValidation(t *testing.T) {
	for _, tc := range []struct {
		name            string
		field, resource bool
	}{
		{"structural only", false, false},
		{"field callback", true, false},
		{"resource callback", false, true},
		{"both callbacks", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			field := Field{Type: TypeString, Required: true}
			if tc.field {
				field.Check = func(context.Context, any, Doc) any {
					calls++
					return "server-only rejection"
				}
			}
			res := &Resource{Name: "items", Adapter: &mongoAdapter{}, Schema: Schema{"name": field},
				ResourceMethods: []string{"GET", "POST"}, ItemMethods: []string{"GET", "PATCH", "PUT"}}
			if tc.resource {
				res.Validate = func(context.Context, Doc, Doc, bool) (Issues, error) {
					calls++
					return Issues{"name": "server-only rejection"}, nil
				}
			}
			a := New(Options{})
			a.Register(res)
			doc := docsJSON(t, a)
			validateOpenAPI(t, doc)
			compiler := jsonschema.NewCompiler()
			if err := compiler.AddResource("urn:custom-validation", doc); err != nil {
				t.Fatal(err)
			}
			for key, value := range docsMap(docsMap(doc["components"])["schemas"]) {
				schema := docsMap(value)
				input := strings.HasSuffix(key, "Input") || strings.HasSuffix(key, "Patch")
				_, marked := schema["x-hyrule-custom-validation"]
				if marked != (input && tc.resource) {
					t.Errorf("%s: document-wide validation marker=%t", key, marked)
				}
				if !input {
					continue
				}
				property := docsMap(docsMap(schema["properties"])["name"])
				_, fieldMarked := property["x-hyrule-custom-validation"]
				if fieldMarked != tc.field {
					t.Errorf("%s: field validation marker=%t", key, fieldMarked)
				}
				contract, err := compiler.Compile("urn:custom-validation#/components/schemas/" + key)
				if err != nil {
					t.Fatal(err)
				}
				// Extensions describe server rules; structural validation must
				// neither claim to enforce nor execute those callbacks.
				if err := contract.Validate(map[string]any{"name": "valid structurally"}); err != nil {
					t.Errorf("%s: structural validation rejected valid input: %v", key, err)
				}
			}
			if calls != 0 {
				t.Fatalf("contract generation/validation invoked server callbacks %d times", calls)
			}
		})
	}
}
