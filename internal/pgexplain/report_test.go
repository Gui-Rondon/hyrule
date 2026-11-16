package pgexplain

import (
	"encoding/json"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"os"
	"path/filepath"
	"testing"
)

func TestCollectExactNamesAndBitmap(t *testing.T) {
	root := Node{Type: "Bitmap Heap Scan", Plans: []Node{{Type: "Bitmap Index Scan", Index: "expected_other"}, {Type: "BitmapAnd", Plans: []Node{{Type: "Bitmap Index Scan", Index: "expected"}}}}}
	indexes, scans := Collect(root)
	if len(indexes) != 2 || indexes[0] != "expected" || indexes[1] != "expected_other" || len(scans) != 2 {
		t.Fatalf("indexes=%v scans=%v", indexes, scans)
	}
	indexes, _ = Collect(Node{Type: "Index Scan", Index: "expected_other"})
	if len(indexes) != 1 || indexes[0] == "expected" {
		t.Fatalf("unrelated index counted as expected: %v", indexes)
	}
}
func TestInventoryRequiresEveryDeclaration(t *testing.T) {
	a := postgres.IndexSpec{Table: "comments", Fields: []postgres.IndexField{{Field: "post_id"}}}
	b := postgres.IndexSpec{Table: "comments", Fields: []postgres.IndexField{{Field: "status"}}}
	cases := []Case{{Name: "lookup", Index: a}}
	if err := Inventory([]postgres.IndexSpec{a, b}, cases); err == nil {
		t.Fatal("new index silently omitted")
	}
	if err := Inventory([]postgres.IndexSpec{a}, append(cases, cases...)); err == nil {
		t.Fatal("duplicate scenario accepted")
	}
	if err := Inventory([]postgres.IndexSpec{b}, cases); err == nil {
		t.Fatal("undeclared index accepted")
	}
	if err := Inventory([]postgres.IndexSpec{a}, cases); err != nil {
		t.Fatal(err)
	}
}

// A failed artifact write must never leave a success marker for tooling.
func TestSaveDoesNotPublishCompleteBeforeArtifacts(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "summary.md"), 0755); err != nil {
		t.Fatal(err)
	}
	r := &Report{Complete: true}
	if err := r.Save(dir); err == nil {
		t.Fatal("expected summary write failure")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot Report
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Complete {
		t.Fatal("failed artifact write published complete=true")
	}
	if err := os.Remove(filepath.Join(dir, "summary.md")); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(dir); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil || !snapshot.Complete {
		t.Fatalf("successful publication incomplete: %v", err)
	}
}
