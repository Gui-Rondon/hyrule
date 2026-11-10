// Package health serves /status and /healthcheck with the bodies of the Eve
// APIs (endpoints/status.py and eve-healthcheck), which monitoring and the
// Kubernetes probes already read.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Info identifies the running build.
type Info struct {
	Version string
	// Branch is the content of git_branch.txt, written by the CI build job.
	Branch string
	// DryRun adds "dry_run": true to /status and /healthcheck, so a client
	// can tell dry-run traffic apart (docs/dry-run.md). Without dry run the
	// bodies are Eve's.
	DryRun bool
}

func (i Info) markDryRun(body map[string]any) {
	if i.DryRun {
		body["dry_run"] = true
	}
}

// LoadInfo reads git_branch.txt from the working directory when present.
func LoadInfo(version string) Info {
	branch := ""
	if b, err := os.ReadFile("git_branch.txt"); err == nil {
		branch = strings.TrimSpace(string(b))
	}
	return Info{Version: version, Branch: branch}
}

var commonHeaders = map[string]string{
	"Cache-Control":                    "no-cache, max-age=0",
	"Expires":                          "0",
	"Access-Control-Allow-Headers":     "*",
	"Access-Control-Allow-Origin":      "*",
	"Access-Control-Allow-Credentials": "true",
	"Access-Control-Allow-Methods":     "GET, OPTIONS",
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	for k, v := range commonHeaders {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

// Status answers {"hostname", "version", "message": "Running...", "branch"},
// plus "dry_run": true in dry run.
func Status(info Info) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"hostname": hostname(),
			"version":  info.Version,
			"message":  "Running...",
			"branch":   info.Branch,
		}
		info.markDryRun(body)
		writeJSON(w, http.StatusOK, body)
	})
}

// Checker returns whether a dependency is healthy and a message.
type Checker struct {
	Name  string
	Check func(ctx context.Context) (bool, string)
}

type result struct {
	Checker      string  `json:"checker"`
	Output       string  `json:"output"`
	Passed       bool    `json:"passed"`
	Timestamp    float64 `json:"timestamp"`
	Expires      float64 `json:"expires"`
	ResponseTime float64 `json:"response_time"`
}

// HealthCheck runs the checkers with the caching of python-healthcheck:
// passing results are reused for 27s and failing ones for 9s.
type HealthCheck struct {
	info     Info
	checkers []Checker

	mu    sync.Mutex
	cache map[string]result
}

// NewHealthCheck builds the handler.
func NewHealthCheck(info Info, checkers ...Checker) *HealthCheck {
	return &HealthCheck{info: info, checkers: checkers, cache: map[string]result{}}
}

func (h *HealthCheck) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	results := make([]result, 0, len(h.checkers))
	passed := true
	for _, c := range h.checkers {
		res := h.run(ctx, c)
		passed = passed && res.Passed
		results = append(results, res)
	}
	status, code := "success", http.StatusOK
	if !passed {
		status, code = "failure", http.StatusInternalServerError
	}
	body := map[string]any{
		"hostname":  hostname(),
		"status":    status,
		"timestamp": unix(time.Now()),
		"results":   results,
		"version":   h.info.Version,
		"branch":    h.info.Branch,
	}
	h.info.markDryRun(body)
	writeJSON(w, code, body)
}

func (h *HealthCheck) run(ctx context.Context, c Checker) result {
	h.mu.Lock()
	cached, ok := h.cache[c.Name]
	h.mu.Unlock()
	if ok && cached.Expires >= unix(time.Now()) {
		return cached
	}

	start := time.Now()
	passed, output := c.Check(ctx)
	end := time.Now()
	ttl := 27 * time.Second
	if !passed {
		ttl = 9 * time.Second
	}
	res := result{
		Checker:      c.Name,
		Output:       output,
		Passed:       passed,
		Timestamp:    unix(end),
		Expires:      unix(end.Add(ttl)),
		ResponseTime: end.Sub(start).Seconds(),
	}
	h.mu.Lock()
	h.cache[c.Name] = res
	h.mu.Unlock()
	return res
}

func unix(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }
