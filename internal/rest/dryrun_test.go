package rest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/Gui-Rondon/hyrule/internal/behaviors/events"
	"github.com/Gui-Rondon/hyrule/internal/outbox"
	"github.com/Gui-Rondon/hyrule/internal/platform/mongodb"
	"github.com/Gui-Rondon/hyrule/internal/rest"
	"github.com/Gui-Rondon/hyrule/internal/testkit"
)

// dryRunAPI serves the same collection twice: real (to prepare the data)
// and in dry run, with the rolling back transactor of app.Build.
type dryRunAPI struct {
	real, dry *testkit.Client
	db        *mongo.Database
	api       *rest.API
	svc       *rest.Service
	// post counts the post-write hooks run by the dry run API
	post map[string]int
	pre  int
	mu   sync.Mutex
}

func newDryRunAPI(t *testing.T) *dryRunAPI {
	t.Helper()
	db := testkit.Mongo(t)
	tx, rs, err := mongodb.TransactorFor(context.Background(), db.Client())
	if err != nil {
		t.Fatal(err)
	}
	if !rs {
		t.Skip("dry run needs a replica set")
	}
	d := &dryRunAPI{db: db, post: map[string]int{}}
	real := rest.New(rest.Options{Transactor: tx})
	realRes := items()
	realRes.Store = mongodb.NewStore(db.Collection("items"))
	real.Register(realRes)

	d.api = rest.New(rest.Options{Transactor: mongodb.Transactor{Client: db.Client(), Rollback: true}, DryRun: true})
	res := items()
	res.Store = mongodb.NewStore(db.Collection("items"))
	count := func(event string) { d.mu.Lock(); d.post[event]++; d.mu.Unlock() }
	res.Hooks.Use(events.Hooks(outbox.New(db)), rest.Hooks{
		OnInsert: []func(context.Context, []rest.Doc) error{
			func(context.Context, []rest.Doc) error { d.mu.Lock(); d.pre++; d.mu.Unlock(); return nil },
		},
		OnInserted: []func(context.Context, []rest.Doc) error{
			func(context.Context, []rest.Doc) error { count("inserted"); return nil },
		},
		OnUpdated: []func(context.Context, rest.Doc, rest.Doc) error{
			func(context.Context, rest.Doc, rest.Doc) error { count("updated"); return nil },
		},
		OnReplaced: []func(context.Context, rest.Doc, rest.Doc) error{
			func(context.Context, rest.Doc, rest.Doc) error { count("replaced"); return nil },
		},
		OnDeleted: []func(context.Context, rest.Doc) error{
			func(context.Context, rest.Doc) error { count("deleted"); return nil },
		},
	})
	d.svc = d.api.Register(res)
	d.real, d.dry = testkit.NewClient(t, real), testkit.NewClient(t, d.api)
	return d
}

func (d *dryRunAPI) stored(t *testing.T, coll string) []bson.M {
	t.Helper()
	cur, err := d.db.Collection(coll).Find(context.Background(), bson.M{})
	if err != nil {
		t.Fatal(err)
	}
	var docs []bson.M
	cur.All(context.Background(), &docs)
	return docs
}

// captureLogs records the slog output of the test as JSON lines.
func captureLogs(t *testing.T) func() []map[string]any {
	t.Helper()
	var mu sync.Mutex
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&lockedWriter{mu: &mu, w: &buf}, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		var out []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			var rec map[string]any
			if json.Unmarshal([]byte(line), &rec) == nil {
				out = append(out, rec)
			}
		}
		return out
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func dryRunWrites(logs []map[string]any) []map[string]any {
	var out []map[string]any
	for _, rec := range logs {
		if rec["msg"] == "dry run write" {
			out = append(out, rec)
		}
	}
	return out
}

// Every write answers as a real one and stores nothing: no document, no
// outbox event, no post-write hook; the pre-write hooks and the validation
// run.
func TestDryRunWrites(t *testing.T) {
	d := newDryRunAPI(t)
	logs := captureLogs(t)
	existing := d.real.Do("POST", "/items", map[string]any{"name": "antes"})
	id, etag := existing.String("_id"), existing.String("_etag")
	before := d.stored(t, "items")

	// the events of a write go to the outbox in its transaction
	ctxEvents := events.With(context.Background(), outbox.Event{Topic: "t", Body: "x"})
	if _, err := d.svc.Insert(ctxEvents, []rest.Doc{{"name": "evento"}}, rest.WriteOptions{}); err != nil {
		t.Errorf("Insert with events: %v", err)
	}
	res := d.dry.Do("POST", "/items", map[string]any{"name": "novo"})
	if res.Status != http.StatusCreated || res.String("_status") != "OK" || res.String("_id") == "" {
		t.Errorf("POST = %d %s", res.Status, res.Raw)
	}
	if res.Header.Get(rest.HeaderDryRun) != "true" {
		t.Errorf("POST without %s", rest.HeaderDryRun)
	}
	if res := d.dry.Do("POST", "/items", map[string]any{"name": 1}); res.Status != http.StatusUnprocessableEntity {
		t.Errorf("invalid POST = %d", res.Status)
	}
	patch := d.dry.Do("PATCH", "/items/"+id, map[string]any{"name": "mudou"}, "If-Match", etag)
	if patch.Status != http.StatusOK || patch.String("_etag") == etag {
		t.Errorf("PATCH = %d %s", patch.Status, patch.Raw)
	}
	if res := d.dry.Do("PATCH", "/items/"+id, map[string]any{"name": "x"}, "If-Match", "outro"); res.Status != http.StatusPreconditionFailed {
		t.Errorf("PATCH wrong etag = %d", res.Status)
	}
	if res := d.dry.Do("PUT", "/items/"+id, map[string]any{"name": "trocou"}, "If-Match", etag); res.Status != http.StatusOK {
		t.Errorf("PUT = %d %s", res.Status, res.Raw)
	}
	if res := d.dry.Do("DELETE", "/items/"+id, nil, "If-Match", etag); res.Status != http.StatusNoContent {
		t.Errorf("DELETE = %d %s", res.Status, res.Raw)
	}
	// the custom endpoints write through Service.WithTransaction
	err := d.svc.WithTransaction(context.Background(), func(ctx context.Context) error {
		if _, err := d.svc.Resource().Store.(mongodb.Store).Collection.InsertOne(ctx, bson.M{"name": "direto"}); err != nil {
			return err
		}
		return outbox.New(d.db).Add(ctx, outbox.Event{Topic: "t", Body: "x"})
	})
	if err != nil {
		t.Errorf("custom write: %v", err)
	}

	if after := d.stored(t, "items"); len(after) != 1 || after[0]["name"] != "antes" || after[0]["_etag"] != before[0]["_etag"] {
		t.Errorf("stored = %v", after)
	}
	if evs := d.stored(t, outbox.Collection); len(evs) != 0 {
		t.Errorf("outbox = %v", evs)
	}
	if len(d.post) != 0 {
		t.Errorf("post-write hooks ran: %v", d.post)
	}
	if d.pre != 2 {
		t.Errorf("pre-write hooks of the POST ran %d times", d.pre)
	}

	writes := dryRunWrites(logs())
	want := []struct {
		method, route string
		status        float64
	}{
		{"POST", "/items", 201}, {"POST", "/items", 422}, {"PATCH", "/items/{lookup}", 200},
		{"PATCH", "/items/{lookup}", 412}, {"PUT", "/items/{lookup}", 200}, {"DELETE", "/items/{lookup}", 204},
	}
	if len(writes) != len(want) {
		t.Fatalf("dry run logs = %v", writes)
	}
	for i, w := range want {
		if writes[i]["method"] != w.method || writes[i]["route"] != w.route || writes[i]["status"] != w.status {
			t.Errorf("log %d = %v, want %v", i, writes[i], w)
		}
	}
}

// Reads carry the header too and are not logged; a request served twice
// through the API (duplicate, the contents POST) is logged once.
func TestDryRunReadsAndNestedRequests(t *testing.T) {
	d := newDryRunAPI(t)
	logs := captureLogs(t)
	if res := d.dry.Do("GET", "/items", nil); res.Status != http.StatusOK || res.Header.Get(rest.HeaderDryRun) != "true" {
		t.Errorf("GET = %d, header %q", res.Status, res.Header.Get(rest.HeaderDryRun))
	}
	if res := d.real.Do("GET", "/items", nil); res.Header.Get(rest.HeaderDryRun) != "" {
		t.Error("header without dry run")
	}
	d.api.Handle("POST /items/copy", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/items"
		d.api.ServeHTTP(w, r2)
	}))
	if res := d.dry.Do("POST", "/items/copy", map[string]any{"name": "c"}); res.Status != http.StatusCreated {
		t.Errorf("nested POST = %d %s", res.Status, res.Raw)
	}
	writes := dryRunWrites(logs())
	if len(writes) != 1 || writes[0]["route"] != "POST /items/copy" || writes[0]["status"] != float64(201) {
		t.Errorf("dry run logs = %v", writes)
	}
	if docs := d.stored(t, "items"); len(docs) != 0 {
		t.Errorf("stored = %v", docs)
	}
}

// In dry run each request has a deadline (the mirror holds the client's
// next request until the mirrored one ends); a nested request keeps it.
func TestDryRunTimeout(t *testing.T) {
	logs := captureLogs(t)
	api := rest.New(rest.Options{Transactor: rest.NoTransaction{}, DryRun: true, DryRunTimeout: 50 * time.Millisecond})
	api.Handle("POST /slow", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		rest.WriteError(w, r, rest.Abort(http.StatusServiceUnavailable, ""))
	}))
	api.Handle("POST /nested", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/slow"
		api.ServeHTTP(w, r2)
	}))
	c := testkit.NewClient(t, api)
	for _, path := range []string{"/slow", "/nested"} {
		start := time.Now()
		if res := c.Do("POST", path, nil); res.Status != http.StatusServiceUnavailable || time.Since(start) > time.Second {
			t.Errorf("%s = %d after %v", path, res.Status, time.Since(start))
		}
	}
	writes := dryRunWrites(logs())
	if len(writes) != 2 || writes[0]["timed_out"] != true || writes[1]["timed_out"] != true {
		t.Errorf("dry run logs = %v", writes)
	}

	// an Eve write cut in its transaction
	db := testkit.Mongo(t)
	if _, rs, err := mongodb.TransactorFor(context.Background(), db.Client()); err != nil || !rs {
		t.Skip("dry run needs a replica set")
	}
	eve := rest.New(rest.Options{Transactor: mongodb.Transactor{Client: db.Client(), Rollback: true},
		DryRun: true, DryRunTimeout: 50 * time.Millisecond})
	res := items()
	res.Store = mongodb.NewStore(db.Collection("items"))
	res.Hooks.OnInsert = []func(context.Context, []rest.Doc) error{func(ctx context.Context, _ []rest.Doc) error {
		<-ctx.Done()
		return nil
	}}
	eve.Register(res)
	start := time.Now()
	out := testkit.NewClient(t, eve).Do("POST", "/items", map[string]any{"name": "a"})
	// 500, as any write error: timed_out in the log tells it apart
	if out.Status != http.StatusInternalServerError || time.Since(start) > time.Second {
		t.Errorf("Eve write cut = %d after %v", out.Status, time.Since(start))
	}
	if writes := dryRunWrites(logs()); len(writes) != 3 || writes[2]["timed_out"] != true {
		t.Errorf("dry run logs = %v", writes)
	}

	var deadline bool
	real := rest.New(rest.Options{Transactor: rest.NoTransaction{}, DryRunTimeout: 50 * time.Millisecond})
	real.Handle("GET /deadline", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, deadline = r.Context().Deadline()
	}))
	testkit.NewClient(t, real).Do("GET", "/deadline", nil)
	if deadline {
		t.Error("deadline without dry run")
	}
}

// The status recorder of the dry run writes keeps http.ResponseController
// working (Flush, deadlines).
func TestDryRunResponseController(t *testing.T) {
	api := rest.New(rest.Options{Transactor: rest.NoTransaction{}, DryRun: true})
	var flushErr error
	api.Handle("POST /flush", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		flushErr = http.NewResponseController(w).Flush()
	}))
	if res := testkit.NewClient(t, api).Do("POST", "/flush", nil); res.Status != http.StatusAccepted || flushErr != nil {
		t.Errorf("POST = %d, Flush: %v", res.Status, flushErr)
	}
}

// A read is logged only when the deadline cut it: nothing else records the
// mirrored reads, and the slow ones are the ones to count.
func TestDryRunReadTimeout(t *testing.T) {
	logs := captureLogs(t)
	api := rest.New(rest.Options{Transactor: rest.NoTransaction{}, DryRun: true, DryRunTimeout: 50 * time.Millisecond})
	api.Handle("GET /slow", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		rest.WriteError(w, r, rest.Abort(http.StatusServiceUnavailable, ""))
	}))
	api.Handle("GET /fast", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest.WriteJSON(w, http.StatusOK, map[string]any{})
	}))
	c := testkit.NewClient(t, api)
	if res := c.Do("GET", "/fast", nil); res.Status != http.StatusOK {
		t.Errorf("fast = %d", res.Status)
	}
	if res := c.Do("GET", "/slow", nil); res.Status != http.StatusServiceUnavailable {
		t.Errorf("slow = %d", res.Status)
	}
	var reads []map[string]any
	for _, rec := range logs() {
		if rec["msg"] == "dry run read timed out" {
			reads = append(reads, rec)
		}
	}
	if len(reads) != 1 || reads[0]["path"] != "/slow" || reads[0]["timed_out"] != true ||
		reads[0]["status"] != float64(http.StatusServiceUnavailable) {
		t.Errorf("read logs = %v", reads)
	}
	if writes := dryRunWrites(logs()); len(writes) != 0 {
		t.Errorf("dry run write logs = %v", writes)
	}
}
