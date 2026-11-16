package declarative

import (
	"context"
	"fmt"

	"github.com/Gui-Rondon/hyrule/rest"
)

// Backend attaches the storage seam (Store or Adapter) a Resource needs,
// given the resource's declared source (Resource.Collection). Each
// concrete Backend lives outside this package -- it needs a live
// connection (a *mongo.Database, a *pgxpool.Pool, a *sql.DB...), which
// declarative deliberately never imports; this package only knows the
// registry by name.
type Backend interface {
	Attach(ctx context.Context, res *rest.Resource, source string) error
}

// Registry maps a Resource.Backend name to the Backend that attaches it.
// "" and "mongo" are synonyms, so a resources.yaml written before
// Resource.Backend existed resolves the same way it always did.
type Registry map[string]Backend

// Build resolves every Resource in f through reg -- backend-agnostic
// compilation (Resource.build) first, then dispatch by name -- returning
// ready-to-register *rest.Resource values in file order. The caller still
// does env.API.Register(res) for each.
func (f *File) Build(ctx context.Context, reg Registry) ([]*rest.Resource, error) {
	resources := make([]*rest.Resource, 0, len(f.Resources))
	for _, r := range f.Resources {
		res, err := r.build()
		if err != nil {
			return nil, err
		}
		name := r.Backend
		if name == "" {
			name = "mongo"
		}
		b, ok := reg[name]
		if !ok {
			return nil, fmt.Errorf("declarative: resource %q: unknown backend %q", r.Name, name)
		}
		if err := b.Attach(ctx, res, r.Collection); err != nil {
			return nil, fmt.Errorf("declarative: resource %q: %w", r.Name, err)
		}
		resources = append(resources, res)
	}
	return resources, nil
}
