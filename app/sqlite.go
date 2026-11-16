package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Gui-Rondon/hyrule/platform/auth"
	"github.com/Gui-Rondon/hyrule/platform/health"
	"github.com/Gui-Rondon/hyrule/platform/sqlite"
	"github.com/Gui-Rondon/hyrule/rest"
)

func buildSQLite(ctx context.Context, spec Spec, deps Deps, authn *auth.Authenticator) (http.Handler, error) {
	if deps.SQLite == nil {
		return nil, errors.New("app: Deps.SQLite must not be nil for SQLite")
	}
	if deps.DryRun {
		return nil, errors.New("app: SQLite does not support dry run")
	}
	if len(spec.Indexes) > 0 || len(spec.PostgresIndexes) > 0 {
		return nil, errors.New("app: SQLite does not support Mongo/Postgres index configuration")
	}
	for _, key := range []string{"outbox", "mongo", "postgres", "pubsub", "redis"} {
		if _, ok := deps.Config[key]; ok {
			return nil, errors.New("app: SQLite does not support configuration section " + key)
		}
	}
	timeout, err := time.ParseDuration(deps.Config.String("sqlite.query_timeout", defaultQueryTimeout.String()))
	if err != nil {
		return nil, err
	}
	tx := sqlite.Transactor{DB: deps.SQLite}
	env := &Env{Config: deps.Config, SQLite: deps.SQLite, SQLiteTx: tx, Info: health.LoadInfo(spec.Version), start: deps.Start}
	env.API = rest.New(rest.Options{Auth: authn, Transactor: tx, Messages: spec.Messages, HTMLForbidden: spec.HTMLForbidden, CacheControl: spec.CacheControl, CacheExpires: spec.CacheExpires, QueryTimeout: timeout})
	env.AddCheck(health.Checker{Name: "sqlite", Check: func(ctx context.Context) (bool, string) {
		if err := deps.SQLite.PingContext(ctx); err != nil {
			return false, err.Error()
		}
		return true, "connected"
	}})
	if err := spec.Setup(ctx, env); err != nil {
		return nil, err
	}
	return finishBuild(spec, env, false), nil
}
