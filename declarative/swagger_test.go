package declarative_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gui-Rondon/hyrule/declarative"
	"github.com/Gui-Rondon/hyrule/platform/mongodb"
	"github.com/Gui-Rondon/hyrule/rest"
)

func TestSwaggerYAMLDefaultsAndAliases(t *testing.T) {
	for _, tc := range []struct {
		field            string
		hidden, conflict bool
	}{
		{}, // No field: documentation must be enabled by default.
		{"disableSwagger: false", false, false},
		{"disableSwagger: true", true, false},
		{"disable_swagger: false", false, false},
		{"disable_swagger: true", true, false},
		{"disableSwagger: true\n    disable_swagger: true", true, false},
		{"disableSwagger: false\n    disable_swagger: false", false, false},
		{"disableSwagger: true\n    disable_swagger: false", false, true},
		{"disableSwagger: false\n    disable_swagger: true", false, true},
	} {
		t.Run(tc.field, func(t *testing.T) {
			f, err := declarative.Parse(strings.NewReader("resources:\n  - name: tags\n    collection: tags\n    " + tc.field + "\n    schema:\n      name: {type: string, required: true}\n"))
			if err != nil {
				t.Fatal(err)
			}
			r, err := f.Resources[0].Build(mongodb.Store{})
			if tc.conflict {
				if err == nil || !strings.Contains(err.Error(), "conflict") {
					t.Fatalf("conflict=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.DisableSwagger != tc.hidden {
				t.Fatalf("DisableSwagger=%v", r.DisableSwagger)
			}
			a := rest.New(rest.Options{})
			a.Register(r)
			w := httptest.NewRecorder()
			a.ServeHTTP(w, httptest.NewRequest("GET", "/api/openapi.json", nil))
			var doc struct {
				Paths      map[string]any
				Components struct{ Schemas map[string]any }
			}
			if err = json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			if (doc.Paths["/tags"] == nil) != tc.hidden {
				t.Fatalf("default/override not reflected in docs: %s", w.Body.String())
			}
			if tc.hidden && len(doc.Components.Schemas) != 0 {
				t.Fatal("hidden schema leaked")
			}
		})
	}
}
