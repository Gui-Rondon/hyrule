package declarative_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Gui-Rondon/hyrule/behaviors/events"
	"github.com/Gui-Rondon/hyrule/declarative"
	"github.com/Gui-Rondon/hyrule/outbox"
	"github.com/Gui-Rondon/hyrule/platform/mongodb"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/testkit"
)

const tagsYAML = `
resources:
  - name: tags
    collection: tags
    additional_lookup:
      field: slug
      pattern: '^[\w-]+$'
    resource_methods: [get, post]
    item_methods: [get, patch, put]
    public_methods: [get]
    public_item_methods: [get]
    schema:
      name:
        type: string
        required: true
      slug:
        type: string
        required: true
        unique: true
      enabled:
        type: boolean
        default: true
`

func TestParseAndBuild(t *testing.T) {
	file, err := declarative.Parse(strings.NewReader(tagsYAML))
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Resources) != 1 {
		t.Fatalf("resources = %d", len(file.Resources))
	}
	r := file.Resources[0]
	if r.Name != "tags" || r.Collection != "tags" {
		t.Errorf("name/collection = %q/%q", r.Name, r.Collection)
	}

	res, err := r.Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "tags" {
		t.Errorf("built name = %q", res.Name)
	}
	if res.AdditionalLookup == nil || res.AdditionalLookup.Field != "slug" || !res.AdditionalLookup.Pattern.MatchString("go-lang") {
		t.Errorf("additional lookup = %v", res.AdditionalLookup)
	}
	if !slicesEqual(res.ResourceMethods, []string{http.MethodGet, http.MethodPost}) {
		t.Errorf("resource methods = %v", res.ResourceMethods)
	}
	if !slicesEqual(res.ItemMethods, []string{http.MethodGet, http.MethodPatch, http.MethodPut}) {
		t.Errorf("item methods = %v", res.ItemMethods)
	}
	name, ok := res.Schema["name"]
	if !ok || name.Type != rest.TypeString || !name.Required {
		t.Errorf("name field = %+v", name)
	}
	slug, ok := res.Schema["slug"]
	if !ok || !slug.Unique {
		t.Errorf("slug field = %+v", slug)
	}
	enabled, ok := res.Schema["enabled"]
	if !ok || enabled.Type != rest.TypeBoolean || enabled.Default != true {
		t.Errorf("enabled field = %+v", enabled)
	}
}

func TestNestedSchemaAndItems(t *testing.T) {
	const y = `
resources:
  - name: posts
    collection: posts
    schema:
      tags:
        type: list
        items:
          type: string
      seo:
        type: dict
        schema:
          title:
            type: string
`
	file, err := declarative.Parse(strings.NewReader(y))
	if err != nil {
		t.Fatal(err)
	}
	res, err := file.Resources[0].Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	tags := res.Schema["tags"]
	if tags.Type != rest.TypeList || tags.Items == nil || tags.Items.Type != rest.TypeString {
		t.Errorf("tags field = %+v", tags)
	}
	seo := res.Schema["seo"]
	if seo.Type != rest.TypeDict || seo.Schema == nil || seo.Schema["title"].Type != rest.TypeString {
		t.Errorf("seo field = %+v", seo)
	}
}

func TestInvalidRegexFails(t *testing.T) {
	const y = `
resources:
  - name: x
    schema:
      f:
        type: string
        regex: '[invalid'
`
	file, err := declarative.Parse(strings.NewReader(y))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Resources[0].Build(nil); err == nil {
		t.Error("invalid regex accepted")
	}
}

func TestUnknownTypeFails(t *testing.T) {
	const y = `
resources:
  - name: x
    schema:
      f:
        type: not-a-real-type
`
	file, err := declarative.Parse(strings.NewReader(y))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Resources[0].Build(nil); err == nil {
		t.Error("unknown type accepted")
	}
}

// A resource built from YAML registers and runs exactly like a hand-written
// one: the conditional outbox primitive (Fase 3) attaches in Go the same
// way regardless of where the Resource/Schema came from.
func TestBuiltResourceComposesWithConditionalOutbox(t *testing.T) {
	const y = `
resources:
  - name: posts
    collection: posts
    item_methods: [get, patch]
    schema:
      status:
        type: string
`
	db := testkit.Mongo(t)
	tx, replicaSet, err := mongodb.TransactorFor(context.Background(), db.Client())
	if err != nil {
		t.Fatal(err)
	}
	if !replicaSet {
		t.Skip("Mongo is not a replica set")
	}
	file, err := declarative.Parse(strings.NewReader(y))
	if err != nil {
		t.Fatal(err)
	}
	box := outbox.New(db)
	cond := outbox.OnFieldChange("status", []string{"published"}, func(doc map[string]any) outbox.Event {
		return outbox.Event{Topic: "posts-to-publish", Key: "k"}
	})
	res, err := file.Resources[0].Build(mongodb.NewStore(db.Collection("posts")))
	if err != nil {
		t.Fatal(err)
	}
	res.Hooks.Use(events.ConditionalHooks(box, cond))
	svc := rest.NewService(res, tx)
	ctx := context.Background()
	count := func() int64 {
		n, _ := box.Collection().CountDocuments(ctx, bson.M{})
		return n
	}

	docs, err := svc.Insert(ctx, []rest.Doc{{"status": "draft"}}, rest.WriteOptions{})
	if err != nil || count() != 0 {
		t.Fatalf("insert draft: %v, events %d", err, count())
	}
	lookup := bson.M{"_id": docs[0]["_id"]}
	if _, err := svc.Patch(ctx, lookup, rest.Doc{"status": "published"}, rest.WriteOptions{SkipConcurrency: true}); err != nil || count() != 1 {
		t.Fatalf("patch to published: %v, events %d", err, count())
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
