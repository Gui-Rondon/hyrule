// Package server holds the bootstrap shared by the services: logging, the
// HTTP server and graceful shutdown.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// SetupLogging configures JSON logs on stdout; DEBUG enables debug level.
func SetupLogging() {
	level := slog.LevelInfo
	if v := strings.ToLower(os.Getenv("DEBUG")); v != "" && v != "0" && v != "false" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}

// Run serves handler on PORT (default 8080) until SIGINT/SIGTERM, then drains
// the in-flight requests.
func Run(handler http.Handler) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return Serve(ctx, handler)
}

// Serve serves handler on PORT (default 8080) until ctx is done, then drains
// the in-flight requests.
func Serve(ctx context.Context, handler http.Handler) error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           AccessLog(handler),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", srv.Addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// AccessLog logs every request with its status and duration. A request
// served by a Quiet handler is logged only when it fails: 4xx as a warning,
// 5xx as an error.
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		quiet := new(bool)
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), quietKey{}, quiet)))
		level := slog.LevelInfo
		if *quiet {
			switch {
			case rec.status >= 500:
				level = slog.LevelError
			case rec.status >= 400:
				level = slog.LevelWarn
			default:
				return
			}
		}
		slog.Log(r.Context(), level, "request",
			"method", r.Method,
			"path", r.URL.Path,
			"query", r.URL.RawQuery,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

type quietKey struct{}

// Quiet marks the requests served by h, such as the probes of /status and
// the checks of /healthcheck (every few seconds per pod), to stay out of the
// access log of AccessLog unless they fail.
func Quiet(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if quiet, ok := r.Context().Value(quietKey{}).(*bool); ok {
			*quiet = true
		}
		h.ServeHTTP(w, r)
	})
}
