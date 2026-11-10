package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServeStopsWithTheContext(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	t.Setenv("PORT", fmt.Sprint(port))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}))
	}()

	var code int
	for i := 0; i < 50; i++ {
		if resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port)); err == nil {
			code = resp.StatusCode
			resp.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if code != http.StatusTeapot {
		t.Fatalf("code = %d", code)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("serve: %v", err)
	}
}

func TestServeReportsListenErrors(t *testing.T) {
	t.Setenv("PORT", "-1")
	if err := Serve(context.Background(), http.NotFoundHandler()); err == nil {
		t.Error("expected error")
	}
}

func TestAccessLogKeepsTheStatus(t *testing.T) {
	SetupLogging()
	t.Setenv("DEBUG", "1")
	SetupLogging()
	rec := httptest.NewRecorder()
	AccessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(rec, httptest.NewRequest("GET", "/x?a=1", nil))
	if rec.Code != http.StatusAccepted {
		t.Errorf("code = %d", rec.Code)
	}
}

// The requests of a Quiet handler (probes, healthchecks) are logged only
// when they fail: 4xx as a warning, 5xx as an error. The others are logged
// as before.
func TestAccessLogQuiet(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	status := http.StatusOK
	mux := http.NewServeMux()
	mux.Handle("/status", Quiet(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })))
	mux.Handle("/items", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	h := AccessLog(mux)
	for _, c := range []struct {
		path   string
		status int
		level  string
	}{
		{"/status", 200, ""},
		{"/status", 404, "WARN"},
		{"/status", 500, "ERROR"},
		{"/items", 200, "INFO"},
	} {
		buf.Reset()
		status = c.status
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", c.path, nil))
		if c.level == "" {
			if buf.Len() != 0 {
				t.Errorf("%s %d logged: %s", c.path, c.status, buf.String())
			}
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(buf.Bytes(), &rec); err != nil || rec["level"] != c.level || rec["path"] != c.path {
			t.Errorf("%s %d: %s", c.path, c.status, buf.String())
		}
	}
}
