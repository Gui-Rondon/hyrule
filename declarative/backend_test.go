package declarative_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Gui-Rondon/hyrule/declarative"
	"github.com/Gui-Rondon/hyrule/rest"
)

// fakeBackend records every source it was asked to Attach, and fails
// when failOn matches the source -- enough to exercise File.Build's
// dispatch logic without any real database.
type fakeBackend struct {
	attached []string
	failOn   string
}

func (b *fakeBackend) Attach(_ context.Context, _ *rest.Resource, source string) error {
	if source == b.failOn {
		return errors.New("boom")
	}
	b.attached = append(b.attached, source)
	return nil
}

func parseFile(t *testing.T, yaml string) *declarative.File {
	t.Helper()
	f, err := declarative.Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return f
}

func TestFileBuild_DefaultBackendIsMongo(t *testing.T) {
	f := parseFile(t, `
resources:
  - name: tags
    collection: tags
    schema:
      name: {type: string, required: true}
`)
	mongo := &fakeBackend{}
	resources, err := f.Build(context.Background(), declarative.Registry{"mongo": mongo})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(resources) != 1 || resources[0].Name != "tags" {
		t.Fatalf("resources = %+v", resources)
	}
	if got := mongo.attached; len(got) != 1 || got[0] != "tags" {
		t.Fatalf("mongo.attached = %v", got)
	}
}

func TestFileBuild_ExplicitBackend(t *testing.T) {
	f := parseFile(t, `
resources:
  - name: items
    collection: items
    backend: postgres
    schema:
      name: {type: string, required: true}
`)
	mongo, postgres := &fakeBackend{}, &fakeBackend{}
	_, err := f.Build(context.Background(), declarative.Registry{"mongo": mongo, "postgres": postgres})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(mongo.attached) != 0 {
		t.Fatalf("mongo should not have been used: %v", mongo.attached)
	}
	if got := postgres.attached; len(got) != 1 || got[0] != "items" {
		t.Fatalf("postgres.attached = %v", got)
	}
}

func TestFileBuild_UnknownBackend(t *testing.T) {
	f := parseFile(t, `
resources:
  - name: items
    collection: items
    backend: cassandra
`)
	_, err := f.Build(context.Background(), declarative.Registry{"mongo": &fakeBackend{}})
	if err == nil || !strings.Contains(err.Error(), `unknown backend "cassandra"`) {
		t.Fatalf("Build error = %v", err)
	}
}

func TestFileBuild_AttachErrorIsWrappedWithResourceName(t *testing.T) {
	f := parseFile(t, `
resources:
  - name: items
    collection: items
`)
	_, err := f.Build(context.Background(), declarative.Registry{"mongo": &fakeBackend{failOn: "items"}})
	if err == nil || !strings.Contains(err.Error(), `resource "items"`) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Build error = %v", err)
	}
}
