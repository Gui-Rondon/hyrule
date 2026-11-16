package taglookup

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gui-Rondon/hyrule/declarative"
	"github.com/Gui-Rondon/hyrule/platform/sqlite"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restsqlite"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type observer struct {
	rest.Adapter
	reads int
	find  func(context.Context) ([]rest.Doc, error)
}

func (a *observer) Find(ctx context.Context, f, s, p any, skip, limit int64) ([]rest.Doc, error) {
	a.reads++
	if skip != 0 || limit != 1 {
		panic("unbounded query")
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > executionTimeout {
		panic("missing deadline")
	}
	if a.find != nil {
		return a.find(ctx)
	}
	return a.Adapter.Find(ctx, f, s, p, skip, limit)
}
func (a *observer) InsertMany(context.Context, []rest.Doc) error { panic("unexpected write") }
func (a *observer) UpdateOne(context.Context, any, rest.Doc, rest.Doc) (int64, error) {
	panic("unexpected write")
}
func (a *observer) ReplaceOne(context.Context, any, rest.Doc) (int64, error) {
	panic("unexpected write")
}
func (a *observer) DeleteOne(context.Context, any) (int64, error) { panic("unexpected write") }

type rig struct {
	e        *Executor
	d        Dependencies
	a        *observer
	records  []Record
	snapshot func() string
}

func setup(t *testing.T, configure func(*rest.Resource)) *rig {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	file, err := os.Open("../../resources.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	f, err := declarative.Parse(file)
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.Resources[0].Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := restsqlite.NewAdapter(ctx, db, "tags", res)
	if err != nil {
		t.Fatal(err)
	}
	res.Adapter = adapter
	svc := rest.New(rest.Options{Transactor: sqlite.Transactor{DB: db}}).Register(res)
	_, err = svc.Insert(ctx, []rest.Doc{{"name": "Go", "slug": "go", "enabled": true}, {"name": "Hidden", "slug": "hidden", "enabled": false}}, rest.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if configure != nil {
		configure(res)
	}
	a := &observer{Adapter: adapter}
	res.Adapter = a
	r := &rig{a: a}
	r.snapshot = func() string {
		var s string
		if err := db.QueryRow(`SELECT group_concat(doc || ':' || version, '|') FROM tags`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	r.d = Dependencies{Service: svc, Scope: "local:tags", Verify: func(context.Context) (Principal, error) { return Principal{"reader"}, nil }, Authorize: func(_ context.Context, p Principal, a Action) error {
		if p.Subject != "reader" || a != (Action{Name, Version, Permission, "local:tags"}) {
			return errors.New("secret policy")
		}
		return nil
	}, Audit: func(ctx context.Context, rec Record) error {
		if ctx.Err() != nil {
			t.Error("audit inherited cancellation")
		}
		r.records = append(r.records, rec)
		return nil
	}}
	r.rebuild(t)
	return r
}
func (r *rig) rebuild(t *testing.T) {
	t.Helper()
	var err error
	r.e, err = New(r.d)
	if err != nil {
		t.Fatal(err)
	}
}
func TestLookupAllowlistAndNoWrites(t *testing.T) {
	r := setup(t, func(res *rest.Resource) {
		res.Pagination.Disabled = true
		res.Hooks.OnFetchedResource = []func(context.Context, []rest.Doc) error{func(_ context.Context, docs []rest.Doc) error {
			for _, d := range docs {
				d["secret"] = "private"
			}
			return nil
		}}
	})
	before := r.snapshot()
	for _, tc := range []struct{ slug, want string }{{"go", `{"found":true,"tag":{"name":"Go","slug":"go","enabled":true}}`}, {"hidden", `{"found":false}`}, {"missing", `{"found":false}`}, {"Go", `{"found":false}`}} {
		out, err := r.e.Execute(context.Background(), strings.NewReader(`{"slug":"`+tc.slug+`"}`))
		if err != nil || string(out) != tc.want {
			t.Fatalf("%s: %s %v", tc.slug, out, err)
		}
	}
	if r.snapshot() != before {
		t.Fatal("stored records changed")
	}
	if r.a.reads != 4 || len(r.records) != 4 {
		t.Fatal("missing read/audit")
	}
	for _, rec := range r.records {
		if !rec.Authorized || rec.Subject != "reader" || rec.Outcome != "ok" || rec.CorrelationID == "" {
			t.Fatalf("bad audit: %+v", rec)
		}
	}
}
func TestDiscoveryAndExecutionAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name   string
		verify func(context.Context) (Principal, error)
		policy func(context.Context, Principal, Action) error
		want   error
	}{
		{"unverified", func(context.Context) (Principal, error) { return Principal{}, errors.New("secret credentials") }, nil, ErrIdentity},
		{"empty subject", func(context.Context) (Principal, error) { return Principal{}, nil }, nil, ErrIdentity},
		{"wrong identity", func(context.Context) (Principal, error) { return Principal{"other"}, nil }, nil, ErrForbidden},
		{"missing permission", nil, func(context.Context, Principal, Action) error { return errors.New("secret policy") }, ErrForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := setup(t, nil)
			if tc.verify != nil {
				r.d.Verify = tc.verify
			}
			if tc.policy != nil {
				r.d.Authorize = tc.policy
			}
			r.rebuild(t)
			tool, err := r.e.Describe(context.Background())
			if !errors.Is(err, tc.want) || tool != (Tool{}) {
				t.Fatalf("discovery: %+v %v", tool, err)
			}
			out, err := r.e.Execute(context.Background(), strings.NewReader(`{"slug":"go"}`))
			if !errors.Is(err, tc.want) || out != nil {
				t.Fatalf("execution: %s %v", out, err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("raw error leaked")
			}
			if r.a.reads != 0 || len(r.records) != 2 {
				t.Fatal("unauthorized storage call or missing audit")
			}
		})
	}
	r := setup(t, nil)
	tool, err := r.e.Describe(context.Background())
	if err != nil || tool != (Tool{Name, Version, true}) || r.a.reads != 0 {
		t.Fatalf("discovery: %+v %v", tool, err)
	}
}
func TestInvalidInputCannotReachStorage(t *testing.T) {
	r := setup(t, nil)
	inputs := []string{``, `null`, `[]`, `{}`, `{"slug":null}`, `{"slug":true}`, `{"slug":1}`, `{"slug":""}`, `{"slug":"a b"}`, `{"slug":"ação"}`, `{"slug":"go","slug":"hidden"}`, `{"slug":"go","where":{}}`, `{"tenant":"other","slug":"go"}`, `{"slug":"go"} {}`, `{"slug":"go"`, `{"slug":"` + strings.Repeat("a", 129) + `"}`, strings.Repeat(" ", MaxInput) + `{"slug":"go"}`}
	for _, body := range inputs {
		out, err := r.e.Execute(context.Background(), strings.NewReader(body))
		if err != ErrInput || out != nil {
			t.Fatalf("input %q: %s %v", body, out, err)
		}
	}
	out, err := r.e.Execute(context.Background(), nil)
	if err != ErrInput || out != nil {
		t.Fatal("nil input accepted")
	}
	if r.a.reads != 0 || len(r.records) != len(inputs)+1 {
		t.Fatal("invalid input reached storage or missed audit")
	}
}
func TestMandatoryFilterAndOutputHooks(t *testing.T) {
	r := setup(t, func(res *rest.Resource) { res.Datasource.Filter = bson.M{"name": "not Go"} })
	out, err := r.e.Execute(context.Background(), strings.NewReader(`{"slug":"go"}`))
	if err != nil || string(out) != `{"found":false}` {
		t.Fatalf("datasource bypass: %s %v", out, err)
	}
	for _, tc := range []struct {
		name   string
		change func(rest.Doc)
	}{
		{"wrong slug", func(d rest.Doc) { d["slug"] = "other" }}, {"disabled", func(d rest.Doc) { d["enabled"] = false }}, {"wrong type", func(d rest.Doc) { d["name"] = 42 }}, {"missing field", func(d rest.Doc) { delete(d, "enabled") }}, {"raw size", func(d rest.Doc) { d["name"] = strings.Repeat("x", MaxOutput+1) }}, {"encoded size", func(d rest.Doc) { d["name"] = strings.Repeat("\n", MaxOutput/2) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := setup(t, func(res *rest.Resource) {
				res.Hooks.OnFetchedResource = []func(context.Context, []rest.Doc) error{func(_ context.Context, docs []rest.Doc) error {
					for _, d := range docs {
						tc.change(d)
					}
					return nil
				}}
			})
			out, err := r.e.Execute(context.Background(), strings.NewReader(`{"slug":"go"}`))
			if err != ErrOutput || out != nil {
				t.Fatalf("hook bypass: %s %v", out, err)
			}
		})
	}
}
func TestFailureAndCancellation(t *testing.T) {
	t.Run("database", func(t *testing.T) {
		r := setup(t, nil)
		r.a.find = func(context.Context) ([]rest.Doc, error) { return nil, errors.New("secret DSN") }
		out, err := r.e.Execute(context.Background(), strings.NewReader(`{"slug":"go"}`))
		if err != ErrUnavailable || out != nil {
			t.Fatalf("%s %v", out, err)
		}
	})
	t.Run("storage deadline", func(t *testing.T) {
		r := setup(t, nil)
		r.a.find = func(ctx context.Context) ([]rest.Doc, error) { <-ctx.Done(); return nil, ctx.Err() }
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		out, err := r.e.Execute(ctx, strings.NewReader(`{"slug":"go"}`))
		if err != ErrDeadline || out != nil || r.a.reads != 1 || len(r.records) != 1 {
			t.Fatalf("%s %v", out, err)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		r := setup(t, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		out, err := r.e.Execute(ctx, strings.NewReader(`{"slug":"go"}`))
		if err != ErrCanceled || out != nil || r.a.reads != 0 || len(r.records) != 1 {
			t.Fatalf("%s %v", out, err)
		}
	})
	t.Run("audit", func(t *testing.T) {
		r := setup(t, nil)
		r.d.Audit = func(context.Context, Record) error { return errors.New("secret audit") }
		r.rebuild(t)
		out, err := r.e.Execute(context.Background(), strings.NewReader(`{"slug":"go"}`))
		if !errors.Is(err, ErrAudit) || out != nil {
			t.Fatalf("%s %v", out, err)
		}
		tool, err := r.e.Describe(context.Background())
		if !errors.Is(err, ErrAudit) || tool != (Tool{}) {
			t.Fatalf("%+v %v", tool, err)
		}
	})
	t.Run("multiple rows", func(t *testing.T) {
		r := setup(t, nil)
		r.a.find = func(context.Context) ([]rest.Doc, error) { return []rest.Doc{{}, {}}, nil }
		out, err := r.e.Execute(context.Background(), strings.NewReader(`{"slug":"go"}`))
		if err != ErrOutput || out != nil {
			t.Fatalf("%s %v", out, err)
		}
	})
}
func TestConstructionFailsClosed(t *testing.T) {
	r := setup(t, nil)
	for _, remove := range []func(*Dependencies){func(d *Dependencies) { d.Service = nil }, func(d *Dependencies) { d.Scope = "" }, func(d *Dependencies) { d.Verify = nil }, func(d *Dependencies) { d.Authorize = nil }, func(d *Dependencies) { d.Audit = nil }} {
		d := r.d
		remove(&d)
		if _, err := New(d); err == nil {
			t.Fatal("missing dependency accepted")
		}
	}
	for _, change := range []func(*rest.Resource){func(r *rest.Resource) { r.Name = "other" }, func(r *rest.Resource) { r.ResourceMethods = []string{"POST"} }, func(r *rest.Resource) { r.Schema["slug"] = rest.Field{Type: rest.TypeString} }, func(r *rest.Resource) {
		r.Hooks.OnPreGet = []func(context.Context, *rest.ListQuery) error{func(context.Context, *rest.ListQuery) error { return nil }}
	}} {
		r := setup(t, nil)
		change(r.d.Service.Resource())
		if _, err := New(r.d); err == nil {
			t.Fatal("unsupported resource accepted")
		}
	}
}

func TestConcurrentCallsAndAuditIsolation(t *testing.T) {
	r := setup(t, nil)
	// The observer is deliberately serial; use the real concurrent SQLite adapter.
	r.d.Service.Resource().Adapter = r.a.Adapter
	var mu sync.Mutex
	records := []Record{}
	r.d.Audit = func(_ context.Context, rec Record) error {
		mu.Lock()
		defer mu.Unlock()
		records = append(records, rec)
		return nil
	}
	r.rebuild(t)
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := r.e.Execute(context.Background(), strings.NewReader(`{"slug":"go"}`))
			if err != nil {
				failures <- err
			} else if !strings.Contains(string(out), `"slug":"go"`) {
				failures <- errors.New("incorrect result")
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if len(records) != 12 {
		t.Fatalf("audit count %d", len(records))
	}
	ids := map[string]bool{}
	for _, r := range records {
		if ids[r.CorrelationID] {
			t.Fatal("shared correlation ID")
		}
		ids[r.CorrelationID] = true
	}
}
func TestWrongScopeAndReaderFailure(t *testing.T) {
	r := setup(t, nil)
	r.d.Scope = "other:tags"
	r.rebuild(t)
	out, err := r.e.Execute(context.Background(), strings.NewReader(`{"slug":"go"}`))
	if err != ErrForbidden || out != nil || r.a.reads != 0 {
		t.Fatalf("scope bypass: %s %v", out, err)
	}
	r = setup(t, nil)
	out, err = r.e.Execute(context.Background(), brokenReader{})
	if err != ErrInput || out != nil || r.a.reads != 0 {
		t.Fatalf("read failure: %s %v", out, err)
	}
	// Exactly MaxInput bytes may be consumed, but never an extra object or field.
	payload := `{"slug":"go"}`
	payload += strings.Repeat(" ", MaxInput-len(payload))
	out, err = r.e.Execute(context.Background(), strings.NewReader(payload))
	if err != nil || out == nil {
		t.Fatalf("boundary rejected: %s %v", out, err)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("secret reader") }
