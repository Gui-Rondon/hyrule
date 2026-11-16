package main

import (
	"context"
	"errors"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/declarative"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restpostgres"
	"github.com/Gui-Rondon/hyrule/restsqlite"
)

// mongoBackend, postgresBackend and sqliteBackend are the
// declarative.Backend values hyrule-serve registers -- one per
// app.Backend this binary already supports. Each wraps *app.Env, which
// carries exactly one live backend connection per process (the other
// fields stay nil/zero); a resources.yaml resource whose Backend
// doesn't match the process's app.Spec.Backend fails Attach with a
// clear error, never a panic.
var (
	_ declarative.Backend = mongoBackend{}
	_ declarative.Backend = postgresBackend{}
	_ declarative.Backend = sqliteBackend{}
)

type mongoBackend struct{ env *app.Env }

func (b mongoBackend) Attach(_ context.Context, res *rest.Resource, source string) error {
	if b.env.DB == nil {
		return errors.New(`hyrule-serve: backend "mongo" needs BACKEND=mongo (or unset)`)
	}
	res.Store = b.env.Store(source)
	return nil
}

type postgresBackend struct{ env *app.Env }

func (b postgresBackend) Attach(_ context.Context, res *rest.Resource, source string) error {
	if b.env.Postgres == nil {
		return errors.New(`hyrule-serve: backend "postgres" needs BACKEND=postgres`)
	}
	adapter, err := restpostgres.NewAdapter(postgres.NewStore(b.env.Postgres, source), b.env.PostgresTx, restpostgres.Options{
		Schema:     res.Schema,
		SoftDelete: res.SoftDelete,
		Retry:      rest.DefaultRetryOptions(),
	})
	if err != nil {
		return err
	}
	res.Adapter = adapter
	return nil
}

type sqliteBackend struct{ env *app.Env }

func (b sqliteBackend) Attach(ctx context.Context, res *rest.Resource, source string) error {
	if b.env.SQLite == nil {
		return errors.New(`hyrule-serve: backend "sqlite" needs BACKEND=sqlite`)
	}
	adapter, err := restsqlite.NewAdapter(ctx, b.env.SQLite, source, res)
	if err != nil {
		return err
	}
	res.Adapter = adapter
	return nil
}

// backendRegistry builds the declarative.Registry for env -- every
// app.Backend this binary knows about, regardless of which one env is
// actually wired to (an unmatched resource fails at Attach, not here).
func backendRegistry(env *app.Env) declarative.Registry {
	return declarative.Registry{
		"mongo":    mongoBackend{env},
		"postgres": postgresBackend{env},
		"sqlite":   sqliteBackend{env},
	}
}
