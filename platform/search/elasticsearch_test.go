package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Gui-Rondon/hyrule/platform/config"
)

func TestWrites(t *testing.T) {
	var calls []string
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c, err := FromConfig(config.Config{"elasticsearch": map[string]any{"hosts": []any{srv.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Index(ctx, "tags", "abc", map[string]any{"slug": "gremio"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Update(ctx, "articles-published", "abc", map[string]any{"status": "published"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, "tags", "abc"); err != nil {
		t.Fatalf("missing document must not fail: %v", err)
	}
	if ok, msg := c.Check(ctx); !ok {
		t.Errorf("check failed: %s", msg)
	}

	want := []string{"PUT /tags/_doc/abc", "POST /articles-published/_update/abc", "DELETE /tags/_doc/abc", "HEAD /"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v", calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, calls[i], want[i])
		}
	}
	if bodies[0]["slug"] != "gremio" {
		t.Errorf("index body = %v", bodies[0])
	}
	if doc, _ := bodies[1]["doc"].(map[string]any); doc["status"] != "published" {
		t.Errorf("update body = %v", bodies[1])
	}
}

func TestErrorStatusFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	if err := New([]string{srv.URL}).Index(context.Background(), "x", "1", map[string]any{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseHostsFromConfigFiles(t *testing.T) {
	hosts, err := parseHosts([]any{
		map[string]any{"host": "es01", "port": float64(9200)},
		map[string]any{"host": "es02"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if hosts[0] != "http://es01:9200" || hosts[1] != "http://es02:9200" {
		t.Errorf("hosts = %v", hosts)
	}
}

func TestFailureContracts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	c := New([]string{srv.URL})
	if err := c.Index(context.Background(), "items", "id", map[string]any{}); err == nil {
		t.Fatal("HTTP failure accepted")
	}
	if ok, _ := c.Check(context.Background()); ok {
		t.Fatal("unhealthy server reported healthy")
	}
	srv.Close()
	if err := c.Delete(context.Background(), "items", "id"); err == nil {
		t.Fatal("connection failure accepted")
	}
	if ok, _ := c.Check(context.Background()); ok {
		t.Fatal("unreachable server reported healthy")
	}
	if err := c.Index(context.Background(), "items", "id", make(chan int)); err == nil {
		t.Fatal("unencodable document accepted")
	}
	if err := New([]string{"://bad"}).Index(context.Background(), "items", "id", nil); err == nil {
		t.Fatal("invalid endpoint accepted")
	}
	if _, err := FromConfig(config.Config{}); err == nil {
		t.Fatal("missing hosts accepted")
	}
	if _, err := parseHosts([]any{17}); err == nil {
		t.Fatal("unsupported hosts accepted")
	}
	hosts, err := parseHosts(map[string]any{"host": "example", "port": 9443, "scheme": "https"})
	if err != nil || len(hosts) != 1 || hosts[0] != "https://example:9443" {
		t.Fatalf("host map: %v / %v", hosts, err)
	}
}
