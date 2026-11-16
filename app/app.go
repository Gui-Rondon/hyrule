// Package app bootstraps Hyrule services: configuration, database connections,
// authentication, REST resources, health endpoints and graceful shutdown.
//
// Describe a service with [Spec] and register resources in its Setup function.
// [Main] owns process signals and exits on failure; [Run] accepts a caller-owned
// context. [Build] returns an [http.Handler] using caller-owned dependencies,
// suitable for tests or composition with an existing HTTP router.
//
// MongoDB is the default backend. [Spec.Backend] can select Postgres or the
// experimental local SQLite backend. See the examples for complete programs.
// Some HTTP response conventions preserve compatibility with Eve, the Python
// REST framework from which Hyrule migrated; Werkzeug is its HTTP utility library.
package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/Gui-Rondon/hyrule/platform/auth"
	"github.com/Gui-Rondon/hyrule/platform/config"
	"github.com/Gui-Rondon/hyrule/platform/health"
	"github.com/Gui-Rondon/hyrule/platform/httppath"
	"github.com/Gui-Rondon/hyrule/platform/metrics"
	"github.com/Gui-Rondon/hyrule/platform/mongodb"
	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/platform/server"
	"github.com/Gui-Rondon/hyrule/platform/sqlite"
	"github.com/Gui-Rondon/hyrule/rest"
)

// Backend selects which database a Spec's Env wires up. The zero value,
// BackendMongo, keeps every existing service (which never sets this
// field) exactly as it behaved before Backend existed.
type Backend string

const (
	// BackendMongo wires Env.DB/mongodb.Connect, as every service did
	// before Backend existed -- also the zero value, so an unset
	// [Spec.Backend] means this.
	BackendMongo Backend = ""
	// BackendPostgres wires Env.Postgres/platform/postgres.Connect
	// instead. Dry run is implemented via postgres.Transactor.Rollback
	// -- no WriteGuard-equivalent exists (or is needed): every
	// production write already goes through Store.Querier(ctx), which
	// only ever runs inside the ambient transaction Rollback discards.
	BackendPostgres Backend = "postgres"
	// BackendSQLite is experimental local storage; no dry run or outbox.
	BackendSQLite Backend = "sqlite"
)

// validate rejects typos before either bootstrap can select a database.
func (b Backend) validate() error {
	switch b {
	case BackendMongo, BackendPostgres, BackendSQLite:
		return nil
	default:
		return fmt.Errorf("app: unknown backend %q (use BackendMongo, BackendPostgres or BackendSQLite)", b)
	}
}

// Spec describes a service.
type Spec struct {
	// Name is the APP_NAME in properties-api, e.g. "tags-api".
	Name string
	// Backend selects MongoDB (default), Postgres, or experimental SQLite.
	// See [BackendMongo], [BackendPostgres] and [BackendSQLite].
	Backend Backend
	// DefaultDB is used when building a URI from individual connection settings.
	// Mongo also falls back to it for a URI without a database. Specify the
	// database explicitly in Postgres URIs (pgx otherwise resolves its default).
	DefaultDB string
	Version   string
	// Setup registers the resources, custom endpoints and checkers.
	Setup func(ctx context.Context, env *Env) error
	// Messages selects legacy Python HTTP error wording. Nil uses
	// [rest.Werkzeug1]; [rest.Werkzeug0] preserves older client expectations.
	Messages rest.Messages
	// HTMLForbidden is rest.Options.HTMLForbidden (Eve 0.7 without 403
	// in STANDARD_ERRORS).
	HTMLForbidden bool
	// CacheControl and CacheExpires are Eve's global CACHE_CONTROL and
	// CACHE_EXPIRES (the home endpoint); resources set their own.
	CacheControl string
	CacheExpires int
	// Indexes is the service's <service>.indices.conf (go:embed); config-files
	// adds or replaces entries with mongo.indexes (legacy configuration compatibility). BackendMongo only.
	Indexes []byte
	// PostgresIndexes declares a BackendPostgres service's indexes,
	// ensured at boot the same moment Indexes/mongo.indexes are for
	// Mongo. Go-typed values, not text: platform/postgres.IndexSpec has
	// no .indices.conf-equivalent parser to mirror mongodb.ParseIndexes
	// with.
	PostgresIndexes []postgres.IndexSpec
	// URLPrefix is Eve's URL_PREFIX (e.g. "auth"): every route, /status and
	// /healthcheck included, is served under /<prefix>, and anything else is
	// a 404. Routes are registered without it, and the links leave it out,
	// as Eve's resource_link does.
	URLPrefix string
	// NoDryRun is why the service refuses to start in dry run, when it
	// cannot run without effects.
	NoDryRun string
}

// defaultDryRunTimeout limits each request in dry run (rest.Options)
// when DRY_RUN_TIMEOUT is not set: the mirror holds the client's next
// request until the mirrored one ends.
const defaultDryRunTimeout = 3 * time.Second

// defaultQueryTimeout limits each read when mongo.query_timeout is not set.
const defaultQueryTimeout = 10 * time.Second

// Env is what Setup receives.
type Env struct {
	// SQLite is set only for BackendSQLite.
	SQLite   *sql.DB
	SQLiteTx sqlite.Transactor
	Config   config.Config
	// DB is set only for [BackendMongo].
	DB *mongo.Database
	// Postgres is set only for [BackendPostgres].
	Postgres *pgxpool.Pool
	// PostgresTx is the Transactor already wired into API's Options --
	// a BackendPostgres Setup passes this same value to
	// restpostgres.NewAdapter, so a resource's own transaction (e.g. an
	// optimistic-concurrency retry) joins the same Rollback/pool
	// configuration as the request-level one, instead of a second,
	// independently-constructed Transactor silently diverging from it.
	// Zero value for BackendMongo.
	PostgresTx postgres.Transactor
	API        *rest.API
	Info       health.Info
	// DryRun: the writes are rolled back, the post-write hooks and the
	// background tasks do not run. A service whose routes have other
	// effects refuses them with RefuseInDryRun.
	DryRun bool

	checkers []health.Checker
	start    func(name string, task func(ctx context.Context) error)
	wrappers []func(http.Handler) http.Handler
}

// Store builds the rest.Store of a collection, for Setup to assign to
// rest.Resource.Store. DB stays available directly for anything that
// needs the raw *mongo.Database (health checks, indexes, a custom query).
// BackendMongo only -- a BackendPostgres Setup builds postgres.NewStore(e.Postgres, table)
// directly and assigns it to rest.Resource.Adapter via
// restpostgres.NewAdapter, since platform/postgres.Store does not
// implement [rest.Store]. For local experiments use [Env.SQLite] instead.
func (e *Env) Store(collection string) rest.Store {
	if e.DB == nil {
		panic("app: Env.Store is BackendMongo only -- use the selected backend adapter")
	}
	return mongodb.NewStore(e.DB.Collection(collection))
}

// Wrap adds a middleware around the whole API (e.g. logging a deprecated
// parameter).
func (e *Env) Wrap(mw func(http.Handler) http.Handler) { e.wrappers = append(e.wrappers, mw) }

// AddCheck adds a healthcheck checker (the database check is built in).
func (e *Env) AddCheck(c health.Checker) { e.checkers = append(e.checkers, c) }

// Go runs a task while [Run] serves, such as a worker publishing persisted events.
// Run cancels its context and waits for it before closing the database. [Build]
// does not start tasks unless [Deps.Start] is supplied. Dry run skips tasks
// because publishing events or updating worker leases would cause writes.
func (e *Env) Go(name string, task func(ctx context.Context) error) {
	if e.DryRun {
		slog.Info("dry run: background task not started", "task", name)
		return
	}
	if e.start != nil {
		e.start(name, task)
	}
}

// Deps are the external dependencies of Build.
type Deps struct {
	// SQLite is caller-owned; Build never closes it.
	SQLite *sql.DB
	Config config.Config
	// Mongo/DB are for BackendMongo; Run connects them via mongodb.Connect.
	Mongo *mongo.Client
	// DB overrides the database from the configuration (tests).
	DB *mongo.Database
	// Postgres is the pool for BackendPostgres; Run connects it via
	// platform/postgres.Connect.
	Postgres *pgxpool.Pool
	// Auth overrides the authenticator from the configuration (tests).
	Auth *auth.Authenticator
	// Start runs the tasks registered with Env.Go; nil skips them.
	Start func(name string, task func(ctx context.Context) error)
	// DryRun runs the service in dry run; Run sets it from DRY_RUN.
	DryRun bool
	// DryRunTimeout limits each request in dry run; Run sets it from
	// DRY_RUN_TIMEOUT, and 0 means defaultDryRunTimeout.
	DryRunTimeout time.Duration
	// WriteGuard watches the commands of Mongo in dry run; a write outside a
	// transaction fails /healthcheck. Required in dry run.
	WriteGuard *mongodb.WriteGuard
}

// Main runs the service until SIGINT/SIGTERM.
func Main(spec Spec) {
	server.SetupLogging()
	setMemoryLimit()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, spec); err != nil {
		slog.Error("service stopped", "app", spec.Name, "error", err)
		os.Exit(1)
	}
}

// Run loads the configuration (CONFIG_FILE or properties-api), connects to
// the database ([Spec.Backend]) and serves until ctx is done.
func Run(ctx context.Context, spec Spec) error {
	if err := spec.Backend.validate(); err != nil {
		return err
	}
	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var cfg config.Config
	var err error
	if spec.Backend == BackendSQLite && os.Getenv("CONFIG_FILE") == "" && os.Getenv("CONFIGSERVER_ADDRESS") == "" {
		cfg = config.Config{"auth": map[string]any{"enable": false}}
	} else {
		cfg, err = config.LoadFromEnv(startCtx, spec.Name)
	}
	if err != nil {
		return err
	}
	dryRun, err := DryRun()
	if err != nil {
		return err
	}
	var dryRunTimeout time.Duration
	if dryRun {
		if dryRunTimeout, err = DryRunTimeout(); err != nil {
			return err
		}
	}

	tasksCtx, stopTasks := context.WithCancel(ctx)
	var tasks sync.WaitGroup
	// the tasks stop before the database connection is closed (deferred below)
	defer tasks.Wait()
	defer stopTasks()
	start := func(name string, task func(ctx context.Context) error) {
		tasks.Go(func() {
			if err := task(tasksCtx); err != nil && tasksCtx.Err() == nil {
				slog.Error("background task stopped", "app", spec.Name, "task", name, "error", err)
			}
		})
	}

	if spec.Backend == BackendSQLite {
		if dryRun {
			return errors.New("app: SQLite does not support dry run")
		}
		path := cfg.String("sqlite.path", "hyrule.db")
		if value := os.Getenv("SQLITE_PATH"); value != "" {
			path = value
		}
		db, err := sqlite.Open(startCtx, path)
		if err != nil {
			return err
		}
		defer func() { stopTasks(); tasks.Wait(); db.Close() }()
		handler, err := Build(startCtx, spec, Deps{Config: cfg, SQLite: db, Start: start})
		if err != nil {
			return err
		}
		return server.Serve(ctx, handler)
	}
	if spec.Backend == BackendPostgres {
		pool, err := postgres.Connect(startCtx, cfg, spec.DefaultDB)
		if err != nil {
			return err
		}
		defer func() {
			stopTasks()
			tasks.Wait()
			pool.Close()
		}()
		handler, err := Build(startCtx, spec, Deps{Config: cfg, Postgres: pool, Start: start, DryRun: dryRun, DryRunTimeout: dryRunTimeout})
		if err != nil {
			return err
		}
		return server.Serve(ctx, handler)
	}

	var extra []*options.ClientOptions
	var guard *mongodb.WriteGuard
	if dryRun {
		// only transactions are rolled back: watch for writes outside one
		guard = &mongodb.WriteGuard{}
		extra = append(extra, options.Client().SetMonitor(guard.Monitor()))
	}
	client, db, err := mongodb.Connect(startCtx, cfg, spec.DefaultDB, extra...)
	if err != nil {
		return err
	}
	defer func() {
		stopTasks()
		tasks.Wait()
		client.Disconnect(context.Background())
	}()

	handler, err := Build(startCtx, spec, Deps{Config: cfg, Mongo: client, DB: db, Start: start, DryRun: dryRun, DryRunTimeout: dryRunTimeout, WriteGuard: guard})
	if err != nil {
		return err
	}
	return server.Serve(ctx, handler)
}

// Build wires the service and returns its HTTP handler.
func Build(ctx context.Context, spec Spec, deps Deps) (http.Handler, error) {
	if err := spec.Backend.validate(); err != nil {
		return nil, err
	}
	authn := deps.Auth
	if authn == nil {
		var err error
		if authn, err = AuthenticatorFromConfig(deps.Config); err != nil {
			return nil, err
		}
	}
	if err := checkNoDryRunKey(deps.Config); err != nil {
		return nil, err
	}
	dryRunTimeout := deps.DryRunTimeout
	if dryRunTimeout == 0 {
		dryRunTimeout = defaultDryRunTimeout
	}
	if spec.Backend == BackendSQLite {
		return buildSQLite(ctx, spec, deps, authn)
	}
	if spec.Backend == BackendPostgres {
		return buildPostgres(ctx, spec, deps, authn, dryRunTimeout)
	}
	return buildMongo(ctx, spec, deps, authn, dryRunTimeout)
}

// buildMongo is Build's BackendMongo path, as it existed before Backend
// did.
func buildMongo(ctx context.Context, spec Spec, deps Deps, authn *auth.Authenticator, dryRunTimeout time.Duration) (http.Handler, error) {
	tx, replicaSet, err := mongodb.TransactorFor(ctx, deps.Mongo)
	if err != nil {
		return nil, err
	}
	dryRun := deps.DryRun
	queryTimeout, err := time.ParseDuration(deps.Config.String("mongo.query_timeout", defaultQueryTimeout.String()))
	if err != nil {
		return nil, fmt.Errorf("mongo.query_timeout: %w", err)
	}
	indexes, err := declaredIndexes(spec, deps.Config)
	if err != nil {
		return nil, err
	}
	indexesMode, err := mongodb.IndexesModeFromConfig(deps.Config)
	if err != nil {
		return nil, err
	}
	if dryRun {
		switch {
		case spec.NoDryRun != "":
			return nil, fmt.Errorf("%s does not run in dry run: %s", spec.Name, spec.NoDryRun)
		case !replicaSet:
			return nil, errors.New("dry run needs a replica set: without transactions the writes cannot be rolled back")
		case deps.WriteGuard == nil:
			return nil, errors.New("dry run needs the WriteGuard on the Mongo client")
		}
		tx = mongodb.Transactor{Client: deps.Mongo, Rollback: true}
		// building an index is a write too
		if indexesMode == mongodb.IndexesCreate {
			indexesMode = mongodb.IndexesCheck
		}
	}

	info := health.LoadInfo(spec.Version)
	info.DryRun = dryRun
	env := &Env{
		Config: deps.Config,
		DB:     deps.DB,
		API: rest.New(rest.Options{
			Auth: authn, Transactor: tx, Messages: spec.Messages, HTMLForbidden: spec.HTMLForbidden,
			CacheControl: spec.CacheControl, CacheExpires: spec.CacheExpires,
			QueryTimeout: queryTimeout, DryRun: dryRun, DryRunTimeout: dryRunTimeout,
		}),
		Info:   info,
		DryRun: dryRun,
		start:  deps.Start,
	}
	env.checkers = append(env.checkers, health.Mongo(deps.DB))
	if dryRun {
		env.checkers = append(env.checkers, dryRunCheck(deps.WriteGuard))
	}
	if err := spec.Setup(ctx, env); err != nil {
		return nil, err
	}
	if _, err := mongodb.EnsureIndexes(ctx, deps.DB, indexes, indexesMode); err != nil {
		return nil, err
	}

	slog.Info("service ready", "app", spec.Name, "version", spec.Version, "branch", env.Info.Branch,
		"auth", authn.Enabled(), "transactions", replicaSet, "dry_run", dryRun)
	return finishBuild(spec, env, dryRun), nil
}

// buildPostgres is Build's BackendPostgres path.
func buildPostgres(ctx context.Context, spec Spec, deps Deps, authn *auth.Authenticator, dryRunTimeout time.Duration) (http.Handler, error) {
	if deps.Postgres == nil {
		return nil, errors.New("app: Deps.Postgres must not be nil for the Postgres backend")
	}
	if err := postgres.EnsureFunctions(ctx, deps.Postgres); err != nil {
		return nil, err
	}
	dryRun := deps.DryRun
	if dryRun && spec.NoDryRun != "" {
		return nil, fmt.Errorf("%s does not run in dry run: %s", spec.Name, spec.NoDryRun)
	}
	queryTimeout, err := time.ParseDuration(deps.Config.String("postgres.query_timeout", defaultQueryTimeout.String()))
	if err != nil {
		return nil, fmt.Errorf("postgres.query_timeout: %w", err)
	}
	indexesMode, err := postgres.IndexesModeFromConfig(deps.Config)
	if err != nil {
		return nil, err
	}
	if dryRun && indexesMode == postgres.IndexesCreate {
		// building an index is a write too
		indexesMode = postgres.IndexesCheck
	}

	tx := postgres.Transactor{Pool: deps.Postgres, Rollback: dryRun}
	info := health.LoadInfo(spec.Version)
	info.DryRun = dryRun
	env := &Env{
		Config:     deps.Config,
		Postgres:   deps.Postgres,
		PostgresTx: tx,
		API: rest.New(rest.Options{
			Auth: authn, Transactor: tx, Messages: spec.Messages, HTMLForbidden: spec.HTMLForbidden,
			CacheControl: spec.CacheControl, CacheExpires: spec.CacheExpires,
			QueryTimeout: queryTimeout, DryRun: dryRun, DryRunTimeout: dryRunTimeout,
		}),
		Info:   info,
		DryRun: dryRun,
		start:  deps.Start,
	}
	env.checkers = append(env.checkers, health.Postgres(deps.Postgres))
	if err := spec.Setup(ctx, env); err != nil {
		return nil, err
	}
	if _, err := postgres.EnsureIndexes(ctx, deps.Postgres, spec.PostgresIndexes, indexesMode); err != nil {
		return nil, err
	}

	slog.Info("service ready", "app", spec.Name, "version", spec.Version, "branch", env.Info.Branch,
		"auth", authn.Enabled(), "backend", "postgres", "dry_run", dryRun)
	return finishBuild(spec, env, dryRun), nil
}

// finishBuild registers /status and /healthcheck, applies Env.Wrap and
// Spec.URLPrefix, and wraps everything in /metrics -- the part of Build
// identical for every backend.
func finishBuild(spec Spec, env *Env, dryRun bool) http.Handler {
	// status.py registers /status for the first resource and eve-healthcheck
	// /healthcheck for every resource
	// probes and monitoring call them every few seconds: logged only when
	// they fail
	status := server.Quiet(health.Status(env.Info))
	hc := server.Quiet(health.NewHealthCheck(env.Info, env.checkers...))
	env.API.Handle("/status", status)
	env.API.Handle("/healthcheck", hc)
	for i, url := range env.API.ResourceURLs() {
		if i == 0 {
			env.API.Handle("/"+url+"/status", status)
		}
		env.API.Handle("/"+url+"/healthcheck", hc)
	}

	var handler http.Handler = env.API
	for _, mw := range env.wrappers {
		handler = mw(handler)
	}
	if prefix := httppath.NormalizePrefix(spec.URLPrefix); prefix != "" {
		handler = stripURLPrefix(prefix, handler)
	}
	// outermost: /metrics stays at the root whatever the URL prefix, and
	// every request is measured
	return metrics.New(spec.Name, dryRun).Handler(handler)
}

// stripURLPrefix serves next under prefix; any other path is the Eve 404.
func stripURLPrefix(prefix string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tail, ok := httppath.StripPrefix(r.URL.Path, prefix)
		if !ok {
			rest.WriteError(w, r, rest.ErrNotFound)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = tail
		r2.URL.RawPath = ""
		next.ServeHTTP(w, r2)
	})
}

// declaredIndexes merges the service file with mongo.indexes.
func declaredIndexes(spec Spec, cfg config.Config) ([]mongodb.IndexSpec, error) {
	file, err := mongodb.ParseIndexes(bytes.NewReader(spec.Indexes))
	if err != nil {
		return nil, fmt.Errorf("%s.indices.conf: %w", spec.Name, err)
	}
	return mongodb.MergeIndexes(file, cfg)
}

// WarnEmbedded logs requests using the legacy embedded query parameter.
// Python Eve used an extension to fetch related resources over HTTP. Hyrule ignores it;
// the log tells who still sends it (path, user agent, origin).
func WarnEmbedded(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("embedded") {
			slog.Warn("eve: Embedded is deprecated (ignored)", "path", r.URL.Path,
				"embedded", r.URL.Query().Get("embedded"), "user_agent", r.UserAgent(),
				"forwarded_for", r.Header.Get("X-Forwarded-For"), "remote", r.RemoteAddr)
		}
		next.ServeHTTP(w, r)
	})
}

// ElasticsearchCheck is the Elasticsearch checker of the Eve APIs; name is
// the Python function name shown in /healthcheck ("elasticsearch_check" in
// tags-api, "elasticearch_healthcheck" in the Eve-Elastic APIs).
func ElasticsearchCheck(name string, c interface {
	Check(ctx context.Context) (bool, string)
}) health.Checker {
	return health.Checker{Name: name, Check: c.Check}
}

// AuthenticatorFromConfig reads the keys of auth_settings.py and
// azure_settings.py.
func AuthenticatorFromConfig(cfg config.Config) (*auth.Authenticator, error) {
	authCfg := cfg.Sub("auth")
	azureCfg := cfg.Sub("azure")

	var azure *auth.AzureVerifier
	if issuer := azureCfg.String("issuer", ""); issuer != "" {
		azure = auth.NewAzureVerifier(azureCfg.String("tenant", ""), issuer, azureCfg.Strings("audiences"))
	} else {
		slog.Warn("azure not configured, only legacy tokens are accepted")
	}
	return auth.New(auth.Config{
		Enabled:       authCfg.Bool("enable", true),
		CookieName:    authCfg.String("cookie_name", "token"),
		PublicKeyFile: authCfg.String("public_key_file", ""),
	}, azure)
}
