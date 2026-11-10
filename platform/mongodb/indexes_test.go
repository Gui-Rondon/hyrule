package mongodb

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/Gui-Rondon/hyrule/platform/config"
)

const sampleConf = `
# coleção   chaves                        opções
settings    product:1                     unique
articles    _updated:-1
articles    classification_id:1,_updated:-1   name=by_classification
articles    friendly_title:1              unique sparse
sessions    created:1                     ttl=3600
search      headline:text
`

func TestParseIndexes(t *testing.T) {
	specs, err := ParseIndexes(strings.NewReader(sampleConf))
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 6 {
		t.Fatalf("specs = %d", len(specs))
	}
	if s := specs[0]; s.Collection != "settings" || !s.Unique || !reflect.DeepEqual(s.Keys, bson.D{{Key: "product", Value: 1}}) || s.Name != "product_1" {
		t.Errorf("settings = %+v", s)
	}
	if s := specs[2]; s.Name != "by_classification" || !reflect.DeepEqual(s.Keys, bson.D{{Key: "classification_id", Value: 1}, {Key: "_updated", Value: -1}}) {
		t.Errorf("compound = %+v", s)
	}
	if s := specs[1]; s.Name != "_updated_-1" {
		t.Errorf("default name = %q", s.Name)
	}
	if s := specs[3]; !s.Unique || !s.Sparse {
		t.Errorf("options = %+v", s)
	}
	if s := specs[4]; s.TTL == nil || *s.TTL != 3600 {
		t.Errorf("ttl = %+v", s)
	}
	if s := specs[5]; !reflect.DeepEqual(s.Keys, bson.D{{Key: "headline", Value: "text"}}) {
		t.Errorf("text = %+v", s)
	}
}

func TestParseIndexesErrors(t *testing.T) {
	for _, line := range []string{
		"settings",                      // no keys
		"settings product",              // no direction
		"settings product:2",            // bad direction
		"settings product:1 unico",      // unknown option
		"settings product:1 ttl=abc",    // bad ttl
		"settings product:1,product:-1", // repeated field
	} {
		if _, err := ParseIndexes(strings.NewReader(line)); err == nil {
			t.Errorf("%q: expected error", line)
		} else if !strings.Contains(err.Error(), "line 1") {
			t.Errorf("%q: error without line: %v", line, err)
		}
	}
}

// config-files can add indexes without a new image (mongo.indexes) and
// replace one of the file by name.
func TestIndexesFromConfig(t *testing.T) {
	file, _ := ParseIndexes(strings.NewReader("settings product:1 unique\nsettings _updated:-1"))
	cfg := config.Config{"mongo": map[string]any{"indexes": []any{
		"settings _updated:-1 name=_updated_-1 sparse",
		"settings site:1",
	}}}
	specs, err := MergeIndexes(file, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range specs {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"product_1", "_updated_-1", "site_1"}) || !specs[1].Sparse {
		t.Errorf("merged = %+v", specs)
	}
	if _, err := MergeIndexes(nil, config.Config{"mongo": map[string]any{"indexes": []any{"x"}}}); err == nil {
		t.Error("invalid config line must fail")
	}
}

func TestEnsureIndexes(t *testing.T) {
	uri := testURI(t)
	ctx := context.Background()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(ctx)
	db := client.Database("indexes_test")
	defer db.Drop(ctx)
	db.Drop(ctx)

	specs, _ := ParseIndexes(strings.NewReader(sampleConf))

	// check only reports what is missing and creates nothing
	missing, err := EnsureIndexes(ctx, db, specs, IndexesCheck)
	if err != nil || len(missing) != len(specs) {
		t.Fatalf("check: missing=%v err=%v", missing, err)
	}
	if names := indexNames(t, db, "settings"); len(names) != 0 {
		t.Errorf("check created %v", names)
	}

	if missing, err = EnsureIndexes(ctx, db, specs, IndexesCreate); err != nil || len(missing) != 0 {
		t.Fatalf("create: missing=%v err=%v", missing, err)
	}
	if names := indexNames(t, db, "articles"); !reflect.DeepEqual(names, []string{"_id_", "_updated_-1", "by_classification", "friendly_title_1"}) {
		t.Errorf("articles indexes = %v", names)
	}
	// idempotent, and nothing is missing afterwards
	if missing, err = EnsureIndexes(ctx, db, specs, IndexesCreate); err != nil || len(missing) != 0 {
		t.Errorf("second run: missing=%v err=%v", missing, err)
	}
	if missing, _ = EnsureIndexes(ctx, db, specs, IndexesCheck); len(missing) != 0 {
		t.Errorf("check after create: %v", missing)
	}

	// an index already built under another name (e.g. by Eve) is kept: the
	// conflict is logged, not fatal
	db.Collection("legacy").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "slug", Value: 1}}, Options: options.Index().SetName("slug_eve")})
	other, _ := ParseIndexes(strings.NewReader("legacy slug:1"))
	if _, err := EnsureIndexes(ctx, db, other, IndexesCreate); err != nil {
		t.Errorf("conflict must not fail: %v", err)
	}

	if missing, err := EnsureIndexes(ctx, db, specs, IndexesOff); err != nil || missing != nil {
		t.Errorf("off: %v %v", missing, err)
	}
}

func TestIndexesMode(t *testing.T) {
	cases := map[string]IndexesMode{"": IndexesCreate, "create": IndexesCreate, "check": IndexesCheck, "off": IndexesOff}
	for v, want := range cases {
		got, err := IndexesModeFromConfig(config.Config{"mongo": map[string]any{"indexes_mode": v}})
		if err != nil || got != want {
			t.Errorf("%q = %v %v", v, got, err)
		}
	}
	if _, err := IndexesModeFromConfig(config.Config{"mongo": map[string]any{"indexes_mode": "x"}}); err == nil {
		t.Error("unknown mode must fail")
	}
}

func indexNames(t *testing.T, db *mongo.Database, coll string) []string {
	t.Helper()
	cur, err := db.Collection(coll).Indexes().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []struct {
		Name string `bson:"name"`
	}
	cur.All(context.Background(), &out)
	names := []string{}
	for _, o := range out {
		names = append(names, o.Name)
	}
	return names
}
