package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func get(t *testing.T, h http.Handler, method string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, "/", nil))
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body
}

// endpoints/status.py
func TestStatus(t *testing.T) {
	rec, body := get(t, Status(Info{Version: "1.0.0", Branch: "abc (tag: 1.0.0) msg"}), "GET")
	if body["version"] != "1.0.0" || body["branch"] != "abc (tag: 1.0.0) msg" || body["message"] != "Running..." || body["hostname"] == "" {
		t.Errorf("body = %v", body)
	}
	if rec.Header().Get("Cache-Control") != "no-cache, max-age=0" || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("headers = %v", rec.Header())
	}
}

// dry_run appears only in dry run: otherwise the bodies are Eve's.
func TestDryRunMark(t *testing.T) {
	if _, body := get(t, Status(Info{}), "GET"); body["dry_run"] != nil {
		t.Errorf("status = %v", body)
	}
	if _, body := get(t, Status(Info{DryRun: true}), "GET"); body["dry_run"] != true {
		t.Errorf("status in dry run = %v", body)
	}
	if _, body := get(t, NewHealthCheck(Info{DryRun: true}), "GET"); body["dry_run"] != true {
		t.Errorf("healthcheck in dry run = %v", body)
	}
}

func TestLoadInfoReadsGitBranchFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "git_branch.txt"), []byte("abc123 (HEAD) msg\n"), 0o600)
	wd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(wd)
	if info := LoadInfo("2.0"); info.Branch != "abc123 (HEAD) msg" || info.Version != "2.0" {
		t.Errorf("info = %+v", info)
	}
}

// python-healthcheck: 200 when every checker passes, 500 otherwise; results
// are cached (27s when passing, 9s when failing).
func TestHealthCheck(t *testing.T) {
	calls := 0
	ok := true
	h := NewHealthCheck(Info{Version: "1"}, Checker{Name: "db", Check: func(context.Context) (bool, string) {
		calls++
		return ok, "msg"
	}})

	rec, body := get(t, h, "GET")
	if rec.Code != http.StatusOK || body["status"] != "success" {
		t.Fatalf("status = %d %v", rec.Code, body)
	}
	results := body["results"].([]any)
	r := results[0].(map[string]any)
	if r["checker"] != "db" || r["passed"] != true || r["output"] != "msg" {
		t.Errorf("result = %v", r)
	}
	ok = false
	get(t, h, "GET")
	if calls != 1 {
		t.Errorf("passing result must be cached, calls = %d", calls)
	}

	failing := NewHealthCheck(Info{}, Checker{Name: "es", Check: func(context.Context) (bool, string) { return false, "down" }})
	rec, body = get(t, failing, "GET")
	if rec.Code != http.StatusInternalServerError || body["status"] != "failure" {
		t.Errorf("failing = %d %v", rec.Code, body)
	}
	if rec, _ := get(t, failing, "OPTIONS"); rec.Code != http.StatusOK {
		t.Errorf("OPTIONS = %d", rec.Code)
	}
}
