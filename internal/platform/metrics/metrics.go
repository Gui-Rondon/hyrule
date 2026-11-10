// Package metrics exposes the Prometheus metrics of an API at /metrics:
// the duration and the response size of every request by method, route
// and status, plus the Go runtime and the process.
//
// https://gui-rondon.github.io/hyrule/docs/reference/observability
//
// The route is the pattern of the mux (e.g. "GET /posts/{lookup}"), set by
// evecompat with SetRoute; a request no route matched is "unmatched", so a
// path never becomes a label. Each Build has its own registry, so the tests
// that build several services do not collide.
package metrics

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Gui-Rondon/hyrule/internal/platform/server"
)

// Path is where the metrics are served, outside any URL prefix of the API.
const Path = "/metrics"

// Unmatched is the route label of a request no route matched.
const Unmatched = "unmatched"

// Metrics holds the collectors of one service.
type Metrics struct {
	registry *prometheus.Registry
	duration *prometheus.HistogramVec
	size     *prometheus.HistogramVec
}

// New registers the collectors of service; dryRun goes to every series, to
// tell dry-run traffic apart from the rest.
func New(service string, dryRun bool) *Metrics {
	labels := prometheus.Labels{"service": service, "dry_run": strconv.FormatBool(dryRun)}
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		// up to 20s, to cover slow requests without an unbounded tail
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "http_server_request_duration_seconds",
			Help:        "Duration of the HTTP requests, by method, route and status.",
			ConstLabels: labels,
			Buckets:     []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 20},
		}, []string{"method", "route", "status"}),
		size: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "http_server_response_size_bytes",
			Help:        "Size of the HTTP response bodies, by method, route and status.",
			ConstLabels: labels,
			Buckets:     prometheus.ExponentialBuckets(256, 4, 9), // 256 B to 16 MB
		}, []string{"method", "route", "status"}),
	}
	m.registry.MustRegister(m.duration, m.size,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

// Handler serves Path (GET and HEAD only) and passes every other request to
// next, measured.
func (m *Metrics) Handler(next http.Handler) http.Handler {
	// scraped every few seconds: out of the access log unless it fails
	scrape := server.Quiet(promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == Path {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
				return
			}
			scrape.ServeHTTP(w, r)
			return
		}
		route := new(string)
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		defer func() {
			// net/http only closes the connection on a panic: count it as
			// the 500 it is, then let it go on (ErrAbortHandler is a
			// deliberate abort, not an error)
			p := recover()
			status := rec.status
			if p != nil && p != http.ErrAbortHandler {
				status = http.StatusInternalServerError
			}
			m.observe(r.Method, *route, status, time.Since(start), rec.bytes)
			if p != nil {
				panic(p)
			}
		}()
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), routeKey{}, route)))
	})
}

func (m *Metrics) observe(method, route string, status int, d time.Duration, bytes int) {
	if route == "" {
		route = Unmatched
	}
	code := strconv.Itoa(status)
	m.duration.WithLabelValues(method, route, code).Observe(d.Seconds())
	m.size.WithLabelValues(method, route, code).Observe(float64(bytes))
}

type routeKey struct{}

// SetRoute records the route of the request being served. A request served
// again inside another (articles duplicate, the contents POST) sets it too,
// before the outer one returns: the route of entry wins.
func SetRoute(ctx context.Context, pattern string) {
	if route, ok := ctx.Value(routeKey{}).(*string); ok && pattern != "" {
		*route = pattern
	}
}

type recorder struct {
	http.ResponseWriter
	status int
	bytes  int
	wrote  bool
}

func (r *recorder) WriteHeader(status int) {
	if !r.wrote {
		r.status, r.wrote = status, true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.wrote = true
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the server's writer.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
