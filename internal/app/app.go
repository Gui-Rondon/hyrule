// Package app is the bootstrap shared by the services: configuration,
// Mongo, authentication, the Eve API, /status and /healthcheck.
//
// A service only describes its resources:
//
//	func main() {
//		app.Main(app.Spec{Name: "tags-api", DefaultDB: "tags", Version: version, Setup: setup})
//	}
//
// Build does the same without the process concerns, so tests can create the
// whole service against a test database.
package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/Gui-Rondon/hyrule/internal/evecompat"
	"github.com/Gui-Rondon/hyrule/internal/platform/auth"
	"github.com/Gui-Rondon/hyrule/internal/platform/config"
	"github.com/Gui-Rondon/hyrule/internal/platform/health"
	"github.com/Gui-Rondon/hyrule/internal/platform/metrics"
	"github.com/Gui-Rondon/hyrule/internal/platform/mongodb"
	"github.com/Gui-Rondon/hyrule/internal/platform/server"
)

// Spec describes a service.
type Spec struct {
	// Name is the APP_NAME in properties-api, e.g. "tags-api".
	Name string
	// DefaultDB is used when the Mongo URI has no database.
	DefaultDB string
	Version   string
	// Setup registers the resources, custom endpoints and checkers.
	Setup func(ctx context.Context, env *Env) error
	// Messages are the default error texts: nil (Werkzeug1), or
	// evecompat.Werkzeug0 for an API that really runs Werkzeug 0.11.
	Messages evecompat.Messages
	// HTMLForbidden is evecompat.Options.HTMLForbidden (Eve 0.7 without 403
	// in STANDARD_ERRORS).
	HTMLForbidden bool
	// CacheControl and CacheExpires are Eve's global CACHE_CONTROL and
	// CACHE_EXPIRES (the home endpoint); resources set their own.
	CacheControl string
	CacheExpires int
	// Indexes is the service's <service>.indices.conf (go:embed); config-files
	// adds or replaces entries with mongo.indexes (D-27).
	Indexes []byte
	// URLPrefix is Eve's URL_PREFIX (e.g. "auth"): every route, /status and
	// /healthcheck included, is served under /<prefix>, and anything else is
	// a 404. Routes are registered without it, and the links leave it out,
	// as Eve's resource_link does.
	URLPrefix string
	// NoDryRun is why the service refuses to start in dry run, when it
	// cannot run without effects (docs/dry-run.md).
	NoDryRun string
}

// defaultDryRunTimeout limits each request in dry run (evecompat.Options)
// when DRY_RUN_TIMEOUT is not set: the mirror holds the client's next
// request until the mirrored one ends.
const defaultDryRunTimeout = 3 * time.Second

// defaultQueryTimeout limits each read when mongo.query_timeout is not set.
const defaultQueryTimeout = 10 * time.Second

// Env is what Setup receives.
type Env struct {
	Config config.Config
	DB     *mongo.Database
	API    *evecompat.API
	Info   health.Info
	// DryRun: the writes are rolled back, the post-write hooks and the
	// background tasks do not run (docs/dry-run.md). A service whose routes
	// have other effects refuses them with RefuseInDryRun.
	DryRun bool

	checkers []health.Checker
	start    func(name string, task func(ctx context.Context) error)
	wrappers []func(http.Handler) http.Handler
}

// Store builds the evecompat.Store of a collection, for Setup to assign to
// evecompat.Resource.Store. DB stays available directly for anything that
// needs the raw *mongo.Database (health checks, indexes, a custom query).
func (e *Env) Store(collection string) evecompat.Store {
	return mongodb.NewStore(e.DB.Collection(collection))
}

// Wrap adds a middleware around the whole API (e.g. logging a deprecated
// parameter).
func (e *Env) Wrap(mw func(http.Handler) http.Handler) { e.wrappers = append(e.wrappers, mw) }

// AddCheck adds a healthcheck checker (the database check is built in).
func (e *Env) AddCheck(c health.Checker) { e.checkers = append(e.checkers, c) }

// Go runs task while the service serves, e.g. the outbox relay. Run cancels
// its context on shutdown and waits for it before closing Mongo. Build alone
// (tests) does not start tasks, and neither does a dry run: the tasks write
// (the relay publishes and takes its lease, the scheduler publishes).
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
	Config config.Config
	Mongo  *mongo.Client
	// DB overrides the database from the configuration (tests).
	DB *mongo.Database
	// Auth overrides the authenticator from the configuration (tests).
	Auth *auth.Authenticator
	// Start runs the tasks registered with Env.Go; nil skips them.
	Start func(name string, task func(ctx context.Context) error)
	// DryRun runs the service in dry run (docs/dry-run.md); Run sets it
	// from DRY_RUN.
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
// Mongo and serves until ctx is done.
func Run(ctx context.Context, spec Spec) error {
	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cfg, err := config.LoadFromEnv(startCtx, spec.Name)
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
	defer client.Disconnect(context.Background())

	tasksCtx, stopTasks := context.WithCancel(ctx)
	var tasks sync.WaitGroup
	// the tasks stop before Mongo is closed (deferred above)
	defer tasks.Wait()
	defer stopTasks()
	start := func(name string, task func(ctx context.Context) error) {
		tasks.Go(func() {
			if err := task(tasksCtx); err != nil && tasksCtx.Err() == nil {
				slog.Error("background task stopped", "app", spec.Name, "task", name, "error", err)
			}
		})
	}

	handler, err := Build(startCtx, spec, Deps{Config: cfg, Mongo: client, DB: db, Start: start, DryRun: dryRun, DryRunTimeout: dryRunTimeout, WriteGuard: guard})
	if err != nil {
		return err
	}
	return server.Serve(ctx, handler)
}

// Build wires the service and returns its HTTP handler.
func Build(ctx context.Context, spec Spec, deps Deps) (http.Handler, error) {
	authn := deps.Auth
	if authn == nil {
		var err error
		if authn, err = AuthenticatorFromConfig(deps.Config); err != nil {
			return nil, err
		}
	}
	tx, replicaSet, err := mongodb.TransactorFor(ctx, deps.Mongo)
	if err != nil {
		return nil, err
	}
	if err := checkNoDryRunKey(deps.Config); err != nil {
		return nil, err
	}
	dryRun := deps.DryRun
	dryRunTimeout := deps.DryRunTimeout
	if dryRunTimeout == 0 {
		dryRunTimeout = defaultDryRunTimeout
	}
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
		API: evecompat.New(evecompat.Options{
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

	slog.Info("service ready", "app", spec.Name, "version", spec.Version, "branch", env.Info.Branch,
		"auth", authn.Enabled(), "transactions", replicaSet, "dry_run", dryRun)
	var handler http.Handler = env.API
	for _, mw := range env.wrappers {
		handler = mw(handler)
	}
	if spec.URLPrefix != "" {
		handler = stripURLPrefix("/"+strings.Trim(spec.URLPrefix, "/"), handler)
	}
	// outermost: /metrics stays at the root whatever the URL prefix, and
	// every request is measured (docs/observability.md)
	return metrics.New(spec.Name, dryRun).Handler(handler), nil
}

// stripURLPrefix serves next under prefix; any other path is the Eve 404.
func stripURLPrefix(prefix string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest, ok := strings.CutPrefix(r.URL.Path, prefix)
		if !ok || rest != "" && !strings.HasPrefix(rest, "/") {
			evecompat.WriteError(w, r, evecompat.ErrNotFound)
			return
		}
		if rest == "" {
			rest = "/"
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = rest
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

// WarnEmbedded logs the requests with embedded, which the Eve APIs resolved
// through eve-embedded (HTTP to the related APIs). The Go APIs ignore it;
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
