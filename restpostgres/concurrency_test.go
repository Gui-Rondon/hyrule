package restpostgres

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/testkit"
)

// This file is the Fase 5 (concorrência) item of the Postgres
// portability plan: "testes reais, não simulados" of the scenarios
// section 20 lists. Batch insert, soft delete and the bigint overflow
// case already have real tests elsewhere (restpostgres/crud_test.go's
// TestInsertManyBatchIsIndividuallyReadable/TestVersionOverflowIsInternalNeverMisclassified,
// http_test.go's soft-delete tests) and COMMIT failure was covered by
// Fase 4c (platform/postgres/transaction_test.go) -- not repeated
// here. mongoAdapter.RetryPolicy is always the zero value, so the
// If-Match: * retry loop never engages for Mongo; the matrix below
// (MaxAttempts/MaxDuration/context classification) only makes sense
// against Postgres.
//
// A real conflict under a plain If-Match needs no special tooling:
// optimistic concurrency depends on which stale version a request
// read, not on wall-clock overlap, so two goroutines racing a PATCH
// with the same etag are deterministic by construction (see
// TestHTTPConcurrentPatchSamePlainEtagOnlyOneWins below). A real
// conflict under the wildcard retry loop is not: If-Match: * never
// pins a version, so whether two independent goroutines actually
// overlap inside their own read-then-write window is a timing race,
// not a guarantee. forceConflictHook sidesteps that by bumping the
// row's version directly through a second *pgxpool.Pool (a real,
// separate session -- pgxpool.Pool.Exec never consults ctx's ambient
// transaction, only Store's own querier(ctx) does) from inside an
// OnUpdate hook, deterministically simulating a genuine concurrent
// writer on every attempt it's asked to.

// forceConflictHook returns an OnUpdate hook that bumps table's row
// version directly through pool for the first maxCalls calls (0 means
// every call, unconditionally) -- and the *int it writes call counts
// to, for a test to assert exactly how many attempts ran.
func forceConflictHook(t *testing.T, pool *pgxpool.Pool, table string, maxCalls int) (func(context.Context, rest.Doc, rest.Doc) error, *int) {
	t.Helper()
	calls := new(int)
	return func(_ context.Context, _, original rest.Doc) error {
		*calls++
		if maxCalls > 0 && *calls > maxCalls {
			return nil
		}
		id, ok := original["_id"].(uuid.UUID)
		if !ok {
			t.Fatalf("original[_id] = %#v, not a uuid.UUID", original["_id"])
		}
		_, err := pool.Exec(context.Background(), "UPDATE "+table+" SET version = version + 1 WHERE id = $1::uuid", id)
		return err
	}, calls
}

// newHTTPTestAPIWithRetry is newHTTPTestAPI (http_test.go) with an
// explicit RetryOptions instead of the hardcoded
// rest.DefaultRetryOptions() -- needed by the MaxAttempts=0 and
// retry-converges tests below, which each need their own budget.
func newHTTPTestAPIWithRetry(t *testing.T, table string, schema rest.Schema, retry rest.RetryOptions, configure func(*rest.Resource)) *testkit.Client {
	t.Helper()
	pool := testPool(t)
	setupResourceTable(t, pool, table)
	tx := postgres.Transactor{Pool: pool}
	adapter, err := NewAdapter(postgres.NewStore(pool, table), tx, Options{Schema: schema, Retry: retry})
	if err != nil {
		t.Fatal(err)
	}
	res := &rest.Resource{
		Name:            table,
		Schema:          schema,
		Adapter:         adapter,
		ResourceMethods: []string{http.MethodGet, http.MethodPost},
		ItemMethods:     []string{http.MethodGet, http.MethodPatch, http.MethodPut, http.MethodDelete},
	}
	if configure != nil {
		configure(res)
	}
	api := rest.New(rest.Options{Transactor: tx})
	api.Register(res)
	return testkit.NewClient(t, api)
}

// buildPostgresService builds a rest.Service directly over a real
// postgresAdapter, with no rest.API/HTTP layer -- used by the tests
// below that need to inspect Service.Patch's returned Go error
// directly (RetryExhaustedReason, context.Canceled/DeadlineExceeded),
// none of which an HTTP status code alone carries (rest/errors.go's
// errorBody has no such field).
func buildPostgresService(t *testing.T, table string, schema rest.Schema, retry rest.RetryOptions, configure func(*rest.Resource, *pgxpool.Pool)) *rest.Service {
	t.Helper()
	pool := testPool(t)
	setupResourceTable(t, pool, table)
	tx := postgres.Transactor{Pool: pool}
	adapter, err := NewAdapter(postgres.NewStore(pool, table), tx, Options{Schema: schema, Retry: retry})
	if err != nil {
		t.Fatal(err)
	}
	res := &rest.Resource{Name: table, Schema: schema, Adapter: adapter}
	if configure != nil {
		configure(res, pool)
	}
	return rest.NewService(res, tx)
}

// TestHTTPConcurrentPatchSamePlainEtagOnlyOneWins mirrors
// rest/service_test.go's TestConcurrentPatchWithSameEtagOnlyOneWins
// (Mongo), against a real postgresAdapter: two real goroutines PATCH
// the same document with the same (now stale, for whichever loses)
// etag. Deterministic by construction -- a plain If-Match's CAS
// depends on which version each request read, which is fixed before
// either goroutine starts, not on timing overlap.
func TestHTTPConcurrentPatchSamePlainEtagOnlyOneWins(t *testing.T) {
	c := newHTTPTestAPI(t, "http_concurrent_cas_test", defaultHTTPTestSchema, false, nil)
	created := c.Do("POST", "/http_concurrent_cas_test", map[string]any{"name": "a"})
	id, etag := created.String("_id"), created.String("_etag")

	results := make(chan int, 2)
	for _, name := range []string{"b", "c"} {
		go func() {
			results <- c.Do("PATCH", "/http_concurrent_cas_test/"+id, map[string]any{"name": name}, "If-Match", etag).Status
		}()
	}
	codes := map[int]int{}
	codes[<-results]++
	codes[<-results]++
	if codes[http.StatusOK] != 1 || codes[http.StatusPreconditionFailed] != 1 {
		t.Errorf("want one 200 and one 412, got %v", codes)
	}
}

// TestHTTPConcurrentInsertSameUniqueFieldOnlyOneWins races two real
// POSTs for the same unique field value against a real unique index
// -- section 5 of the plan: "a garantia real é o índice", never the
// friendly pre-check (checkUnique/EqualsFilter) alone, which runs
// before either request has written anything and would let both
// through. An OnInsert hook (runs before InsertMany, same point
// forceConflictHook's OnUpdate uses) blocks the "slow" request on a
// channel until the "fast" one has actually committed, forcing a real
// interleaving instead of hoping two goroutines happen to overlap.
func TestHTTPConcurrentInsertSameUniqueFieldOnlyOneWins(t *testing.T) {
	table := "http_concurrent_unique_test"
	schema := rest.Schema{
		"name": {Type: rest.TypeString, Required: true},
		"slug": {Type: rest.TypeString, Unique: true},
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	c := newHTTPTestAPI(t, table, schema, false, func(res *rest.Resource) {
		res.Hooks.OnInsert = []func(context.Context, []rest.Doc) error{
			func(_ context.Context, docs []rest.Doc) error {
				if docs[0]["name"] == "slow" {
					close(entered)
					<-release
				}
				return nil
			},
		}
	})
	pool := testPool(t)
	if _, err := pool.Exec(context.Background(),
		`CREATE UNIQUE INDEX ON `+table+` ((doc->'slug')) WHERE doc ? 'slug'`); err != nil {
		t.Fatal(err)
	}

	slow := make(chan *testkit.Response, 1)
	go func() {
		slow <- c.Do("POST", "/"+table, map[string]any{"name": "slow", "slug": "dup"})
	}()
	<-entered
	fast := c.Do("POST", "/"+table, map[string]any{"name": "fast", "slug": "dup"})
	close(release)
	slowResp := <-slow

	if fast.Status != http.StatusCreated {
		t.Fatalf("fast POST = %d %s", fast.Status, fast.Raw)
	}
	if slowResp.Status != http.StatusConflict {
		t.Fatalf("slow POST = %d %s, want 409 (real unique index violation, the fast request committed first)", slowResp.Status, slowResp.Raw)
	}
}

// TestHTTPWildcardMaxAttemptsZeroUnderRealConflictIsA412 confirms
// section 9.7 against a real, forced conflict: with no retry
// configured, a real version conflict on the single attempt is a
// plain 412, never a 503.
func TestHTTPWildcardMaxAttemptsZeroUnderRealConflictIsA412(t *testing.T) {
	table := "http_wildcard_maxattempts0_test"
	pool := testPool(t)
	retry := rest.RetryOptions{MaxAttempts: 0, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond, MaxDuration: time.Second}
	c := newHTTPTestAPIWithRetry(t, table, defaultHTTPTestSchema, retry, func(res *rest.Resource) {
		hook, _ := forceConflictHook(t, pool, table, 0)
		res.Hooks.OnUpdate = []func(context.Context, rest.Doc, rest.Doc) error{hook}
	})
	created := c.Do("POST", "/"+table, map[string]any{"name": "a"})
	id := created.String("_id")

	patched := c.Do("PATCH", "/"+table+"/"+id, map[string]any{"name": "b"}, "If-Match", "*")
	if patched.Status != http.StatusPreconditionFailed {
		t.Fatalf("PATCH = %d %s, want 412 (MaxAttempts=0, a real conflict forced on the one attempt)", patched.Status, patched.Raw)
	}
}

// TestHTTPWildcardRetryConvergesDespiteRealConflicts confirms the
// loop's main job against a real, forced conflict that eventually
// stops: the first two attempts collide with a genuinely concurrent
// writer, the third finds the row quiet and succeeds.
func TestHTTPWildcardRetryConvergesDespiteRealConflicts(t *testing.T) {
	table := "http_wildcard_converges_test"
	pool := testPool(t)
	retry := rest.RetryOptions{MaxAttempts: 5, BaseBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond, MaxDuration: time.Second}
	hook, calls := forceConflictHook(t, pool, table, 2) // conflicts twice, then stops
	c := newHTTPTestAPIWithRetry(t, table, defaultHTTPTestSchema, retry, func(res *rest.Resource) {
		res.Hooks.OnUpdate = []func(context.Context, rest.Doc, rest.Doc) error{hook}
	})
	created := c.Do("POST", "/"+table, map[string]any{"name": "a"})
	id := created.String("_id")

	patched := c.Do("PATCH", "/"+table+"/"+id, map[string]any{"name": "b"}, "If-Match", "*")
	if patched.Status != http.StatusOK {
		t.Fatalf("PATCH = %d %s, want 200 (the loop must recover once the real conflict stops)", patched.Status, patched.Raw)
	}
	if *calls != 3 {
		t.Errorf("hook called %d times, want exactly 3 (2 forced conflicts + the attempt that succeeds)", *calls)
	}
}

// TestServiceWildcardMaxAttemptsExhaustedUnderRealConflict confirms
// section 9.7's other exhaustion category against a real, persistent
// conflict (forceConflictHook with maxCalls=0, never stopping):
// MaxAttempts=1 exhausts after exactly 2 real attempts (1 initial + 1
// retry), classified "max_attempts" -- inspected on the Go error,
// since an HTTP status alone can't tell max_attempts from
// max_duration.
func TestServiceWildcardMaxAttemptsExhaustedUnderRealConflict(t *testing.T) {
	table := "svc_wildcard_maxattempts_test"
	retry := rest.RetryOptions{MaxAttempts: 1, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond, MaxDuration: time.Second}
	var calls *int
	svc := buildPostgresService(t, table, defaultHTTPTestSchema, retry, func(res *rest.Resource, pool *pgxpool.Pool) {
		var hook func(context.Context, rest.Doc, rest.Doc) error
		hook, calls = forceConflictHook(t, pool, table, 0)
		res.Hooks.OnUpdate = []func(context.Context, rest.Doc, rest.Doc) error{hook}
	})
	created, err := svc.Insert(context.Background(), []rest.Doc{{"name": "a"}}, rest.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	id := created[0]["_id"].(uuid.UUID)
	match, _ := rest.ParseIfMatch([]string{"*"})

	_, err = svc.Patch(context.Background(), id, rest.Doc{"name": "b"}, rest.WriteOptions{IfMatch: match})
	e, ok := errors.AsType[*rest.Error](err)
	if !ok || e.Status != http.StatusServiceUnavailable || e.RetryExhaustedReason != "max_attempts" {
		t.Fatalf("err = %#v, want a 503 Error with RetryExhaustedReason=max_attempts", err)
	}
	if *calls != 2 {
		t.Errorf("hook called %d times, want exactly 2 (1 initial + 1 retry)", *calls)
	}
}

// TestServiceWildcardMaxDurationExhaustedUnderRealConflict is the
// MaxDuration analog: a persistent real conflict (forceConflictHook,
// never stopping) with a tiny budget and a generous MaxAttempts, so
// the budget -- not the attempt count -- is what gives out first.
func TestServiceWildcardMaxDurationExhaustedUnderRealConflict(t *testing.T) {
	table := "svc_wildcard_maxduration_test"
	retry := rest.RetryOptions{MaxAttempts: 1000, BaseBackoff: 20 * time.Millisecond, MaxBackoff: 20 * time.Millisecond, MaxDuration: 60 * time.Millisecond}
	var calls *int
	svc := buildPostgresService(t, table, defaultHTTPTestSchema, retry, func(res *rest.Resource, pool *pgxpool.Pool) {
		var hook func(context.Context, rest.Doc, rest.Doc) error
		hook, calls = forceConflictHook(t, pool, table, 0)
		res.Hooks.OnUpdate = []func(context.Context, rest.Doc, rest.Doc) error{hook}
	})
	created, err := svc.Insert(context.Background(), []rest.Doc{{"name": "a"}}, rest.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	id := created[0]["_id"].(uuid.UUID)
	match, _ := rest.ParseIfMatch([]string{"*"})

	_, err = svc.Patch(context.Background(), id, rest.Doc{"name": "b"}, rest.WriteOptions{IfMatch: match})
	e, ok := errors.AsType[*rest.Error](err)
	if !ok || e.Status != http.StatusServiceUnavailable || e.RetryExhaustedReason != "max_duration" {
		t.Fatalf("err = %#v, want a 503 Error with RetryExhaustedReason=max_duration", err)
	}
	if *calls < 2 {
		t.Errorf("hook called %d times, want at least 2 (MaxAttempts=1000 must never be what exhausts first)", *calls)
	}
}

// TestServiceWildcardOriginalContextCanceledUnderRealConflict
// confirms classifyContextErr's first branch against a real,
// persistent conflict: the caller's own context, not the retry
// budget, is why this failed -- must propagate as ctx.Err(), never a
// 503.
func TestServiceWildcardOriginalContextCanceledUnderRealConflict(t *testing.T) {
	table := "svc_wildcard_ctxcancel_test"
	retry := rest.RetryOptions{MaxAttempts: 1000, BaseBackoff: 20 * time.Millisecond, MaxBackoff: 20 * time.Millisecond, MaxDuration: time.Second}
	svc := buildPostgresService(t, table, defaultHTTPTestSchema, retry, func(res *rest.Resource, pool *pgxpool.Pool) {
		hook, _ := forceConflictHook(t, pool, table, 0)
		res.Hooks.OnUpdate = []func(context.Context, rest.Doc, rest.Doc) error{hook}
	})
	created, err := svc.Insert(context.Background(), []rest.Doc{{"name": "a"}}, rest.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	id := created[0]["_id"].(uuid.UUID)
	match, _ := rest.ParseIfMatch([]string{"*"})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()

	_, err = svc.Patch(ctx, id, rest.Doc{"name": "b"}, rest.WriteOptions{IfMatch: match})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, ok := errors.AsType[*rest.Error](err); ok {
		t.Fatalf("err = %#v, must not be classified as a retry-exhaustion Error", err)
	}
}

// TestServiceIndependentHookContextErrorNeverReclassified confirms
// classifyContextErr's third branch: a hook's own context, with no
// relation to the retry budget, expiring must be preserved as-is,
// never turned into a 503 -- real Postgres underneath, though nothing
// here depends on a forced conflict at all.
func TestServiceIndependentHookContextErrorNeverReclassified(t *testing.T) {
	table := "svc_independent_ctx_test"
	retry := rest.RetryOptions{MaxAttempts: 5, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond, MaxDuration: time.Second}
	svc := buildPostgresService(t, table, defaultHTTPTestSchema, retry, func(res *rest.Resource, _ *pgxpool.Pool) {
		res.Hooks.OnUpdate = []func(context.Context, rest.Doc, rest.Doc) error{
			func(context.Context, rest.Doc, rest.Doc) error {
				ownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
				defer cancel()
				<-ownCtx.Done()
				return ownCtx.Err()
			},
		}
	})
	created, err := svc.Insert(context.Background(), []rest.Doc{{"name": "a"}}, rest.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	id := created[0]["_id"].(uuid.UUID)
	match, _ := rest.ParseIfMatch([]string{"*"})

	_, err = svc.Patch(context.Background(), id, rest.Doc{"name": "b"}, rest.WriteOptions{IfMatch: match})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded (the hook's own, independent context)", err)
	}
	if _, ok := errors.AsType[*rest.Error](err); ok {
		t.Fatalf("err = %#v, must not be reclassified as max_duration/max_attempts", err)
	}
}
