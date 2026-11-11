package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, Path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics = %d", rec.Code)
	}
	return rec.Body.String()
}

// Each request is measured by method, route and status; the route comes
// from SetRoute, the outer one when a request is served inside another,
// and a path without route is "unmatched".
func TestHandler(t *testing.T) {
	m := New("items-api", true)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetRoute(r.Context(), "POST /items/{id}/duplicate")
	})
	h := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/items":
			io.WriteString(w, `{"_items": []}`)
			SetRoute(r.Context(), "GET /items")
		case "/items/1/duplicate":
			// served again through the API, as the articles duplicate
			inner.ServeHTTP(w, r)
			w.WriteHeader(http.StatusCreated)
			SetRoute(r.Context(), "POST /items/{id}")
		default:
			http.NotFound(w, r)
		}
	}))
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/items", nil),
		httptest.NewRequest(http.MethodPost, "/items/1/duplicate", nil),
		httptest.NewRequest(http.MethodGet, "/nothing/here", nil),
	} {
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	out := scrape(t, h)
	for _, want := range []string{
		`http_server_request_duration_seconds_count{dry_run="true",method="GET",route="GET /items",service="items-api",status="200"} 1`,
		`http_server_request_duration_seconds_count{dry_run="true",method="POST",route="POST /items/{id}",service="items-api",status="201"} 1`,
		`http_server_request_duration_seconds_count{dry_run="true",method="GET",route="unmatched",service="items-api",status="404"} 1`,
		`http_server_response_size_bytes_sum{dry_run="true",method="GET",route="GET /items",service="items-api",status="200"} 14`,
		"go_goroutines",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s", want)
		}
	}
	// the scrape itself is not measured
	if strings.Contains(out, `route="/metrics"`) || strings.Contains(out, `"/metrics"`) {
		t.Error("/metrics measured")
	}
}

// Two instances do not share a registry (app.Build runs many times in the
// tests).
func TestInstancesAreIndependent(t *testing.T) {
	a, b := New("a-api", false), New("b-api", false)
	a.Handler(http.NotFoundHandler()).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if out := scrape(t, b.Handler(http.NotFoundHandler())); strings.Contains(out, `service="a-api"`) {
		t.Error("b sees the series of a")
	}
}

// A panic is counted as a 500 with its route and goes on to the server;
// http.ErrAbortHandler keeps the status written.
func TestHandlerPanic(t *testing.T) {
	m := New("items-api", false)
	h := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetRoute(r.Context(), "GET /items/{id}")
		if r.URL.Path == "/abort" {
			w.WriteHeader(http.StatusOK)
			panic(http.ErrAbortHandler)
		}
		panic("boom")
	}))
	for _, path := range []string{"/items/1", "/abort"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: panic swallowed", path)
				}
			}()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
		}()
	}
	out := scrape(t, h)
	for _, want := range []string{
		`http_server_request_duration_seconds_count{dry_run="false",method="GET",route="GET /items/{id}",service="items-api",status="500"} 1`,
		`http_server_request_duration_seconds_count{dry_run="false",method="GET",route="GET /items/{id}",service="items-api",status="200"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s", want)
		}
	}
}

// /metrics answers only GET and HEAD.
func TestScrapeMethods(t *testing.T) {
	h := New("items-api", false).Handler(http.NotFoundHandler())
	for method, want := range map[string]int{http.MethodGet: 200, http.MethodHead: 200, http.MethodPost: 405, http.MethodDelete: 405} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, Path, nil))
		if rec.Code != want {
			t.Errorf("%s /metrics = %d, want %d", method, rec.Code, want)
		}
		if want == 405 && rec.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s /metrics: Allow %q", method, rec.Header().Get("Allow"))
		}
	}
}
