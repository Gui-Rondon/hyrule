package searchsync_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gui-Rondon/hyrule/internal/behaviors/searchsync"
	"github.com/Gui-Rondon/hyrule/internal/platform/search"
	"github.com/Gui-Rondon/hyrule/internal/rest"
	"github.com/Gui-Rondon/hyrule/internal/testkit"
)

func newAPI(t *testing.T, opts ...searchsync.Option) (*testkit.Client, *testkit.ESRecorder) {
	t.Helper()
	es := testkit.NewES(t)
	res := &rest.Resource{
		Name:            "items",
		Store:           testkit.Store(t, "items"),
		Schema:          rest.Schema{"name": {Type: rest.TypeString}},
		ResourceMethods: []string{http.MethodGet, http.MethodPost},
		ItemMethods:     []string{http.MethodGet, http.MethodPatch, http.MethodDelete},
	}
	res.Hooks.Use(searchsync.Hooks(search.New([]string{es.URL}), "items", opts...))
	api := rest.New(rest.Options{})
	api.Register(res)
	return testkit.NewClient(t, api), es
}

func TestTransformAndDelete(t *testing.T) {
	c, es := newAPI(t, searchsync.WithTransform(func(d rest.Doc) any {
		return map[string]any{"title": d["name"]}
	}))
	created := c.Do("POST", "/items", map[string]any{"name": "a"})
	calls := es.Calls()
	if len(calls) != 1 || calls[0].Body["title"] != "a" || len(calls[0].Body) != 1 {
		t.Fatalf("calls = %+v", calls)
	}

	es.Reset()
	c.Do("DELETE", "/items/"+created.String("_id"), nil, "If-Match", created.String("_etag"))
	calls = es.Calls()
	if len(calls) != 1 || calls[0].Method != "DELETE" || calls[0].Path != "/items/_doc/"+created.String("_id") {
		t.Errorf("delete calls = %+v", calls)
	}
}

func TestEveElasticRefreshesDeletes(t *testing.T) {
	c, es := newAPI(t, searchsync.EveElastic("items"))
	created := c.Do("POST", "/items", map[string]any{"name": "a"})
	es.Reset()
	c.Do("DELETE", "/items/"+created.String("_id"), nil, "If-Match", created.String("_etag"))
	if calls := es.Calls(); len(calls) != 1 || calls[0].Query != "refresh=true" {
		t.Errorf("calls = %+v", calls)
	}
}

func TestIndexFailureIsNotAnError(t *testing.T) {
	c, es := newAPI(t)
	es.Fail = true
	if res := c.Do("POST", "/items", map[string]any{"name": "a"}); res.Status != http.StatusCreated {
		t.Errorf("status = %d", res.Status)
	}
}

func TestID(t *testing.T) {
	if got := searchsync.ID(rest.Doc{"_id": "abc"}); got != "abc" {
		t.Errorf("ID = %q", got)
	}
}

// A PATCH indexes the new _etag and _updated, as Eve's on_updated receives
// them in updates, with the +00:00 offset of the tags-api index in HLG.
func TestPatchIndexesNewMetaWithOffset(t *testing.T) {
	c, es := newAPI(t)
	created := c.Do("POST", "/items", map[string]any{"name": "a"})
	if body := es.Calls()[0].Body; !strings.HasSuffix(body["_created"].(string), "+00:00") {
		t.Errorf("created body = %v", body)
	}
	es.Reset()
	updated := c.Do("PATCH", "/items/"+created.String("_id"), map[string]any{"name": "b"}, "If-Match", created.String("_etag"))
	calls := es.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	at, err := time.Parse(rest.DateFormat, updated.String("_updated"))
	if err != nil {
		t.Fatal(err)
	}
	body := calls[0].Body
	if body["name"] != "b" || body["_etag"] != updated.String("_etag") || body["_updated"] != at.Format("2006-01-02T15:04:05+00:00") {
		t.Errorf("patch body = %v, response %v", body, updated.Map())
	}
}

// Eve-Elastic (settings, channels, classifications, authors) keeps the dates
// without offset, as in the classifications index of HLG.
func TestEveElasticDatesWithoutOffset(t *testing.T) {
	c, es := newAPI(t, searchsync.EveElastic("items"))
	c.Do("POST", "/items", map[string]any{"name": "a"})
	body := es.Calls()[0].Body
	if ok, _ := regexp.MatchString(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d$`, body["_created"].(string)); !ok || body["_resource"] != "items" {
		t.Errorf("body = %v", body)
	}
}
