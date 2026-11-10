package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Gui-Rondon/hyrule/internal/platform/config"
	"github.com/Gui-Rondon/hyrule/internal/platform/health"
	"github.com/Gui-Rondon/hyrule/internal/platform/mongodb"
	"github.com/Gui-Rondon/hyrule/internal/rest"
)

// DryRun tells whether the service runs in dry run, from the DRY_RUN
// variable of the Deployment. An unknown value is an error, so a typo
// never turns the writes on.
//
// https://gui-rondon.github.io/hyrule/docs/reference/dry-run
func DryRun() (bool, error) {
	v := os.Getenv("DRY_RUN")
	if v == "" {
		return false, nil
	}
	return parseBool("DRY_RUN", v)
}

// DryRunTimeout is the limit of each request in dry run, from the
// DRY_RUN_TIMEOUT variable (a Go duration, e.g. 20s); 0 when not set. Raising
// it is useful to measure slow reads, at the cost of holding the client's
// next request longer.
func DryRunTimeout() (time.Duration, error) {
	v := os.Getenv("DRY_RUN_TIMEOUT")
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("DRY_RUN_TIMEOUT: invalid duration %q", v)
	}
	return d, nil
}

// checkNoDryRunKey refuses a dry_run key in the configuration: dry run is a
// deployment-time switch (DRY_RUN in the environment), not a value that
// could be left on in a shared config by accident.
func checkNoDryRunKey(cfg config.Config) error {
	if _, ok := cfg.Get("dry_run"); ok {
		return errors.New("dry_run in the configuration is not read: dry run is set only by DRY_RUN in the Deployment")
	}
	return nil
}

// dryRunCheck fails /healthcheck once the guard saw a write outside a
// transaction: it was stored, and the monitoring that reads /healthcheck
// alerts without anyone searching the logs. It stays failed until the pod
// restarts; the probes read /status, so the pod keeps serving.
func dryRunCheck(guard *mongodb.WriteGuard) health.Checker {
	return health.Checker{
		Name: "dry_run_check",
		Check: func(context.Context) (bool, string) {
			if n := guard.Violations(); n > 0 {
				return false, fmt.Sprintf("Dry run NOK: %d writes outside a transaction were stored", n)
			}
			return true, "Dry run OK"
		},
	}
}

// parseBool accepts the strings of Python's strtobool, as config.Bool.
func parseBool(name, v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "y", "yes", "t", "true", "on", "1":
		return true, nil
	case "n", "no", "f", "false", "off", "0":
		return false, nil
	}
	return false, fmt.Errorf("%s: invalid value %q", name, v)
}

// RefuseInDryRun answers 503 with the reason in dry run, for a route whose
// effects are outside Mongo and cannot be rolled back (e.g. a call to
// another service). Without dry run it returns h.
func (e *Env) RefuseInDryRun(reason string, h http.Handler) http.Handler {
	if !e.DryRun {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest.WriteError(w, r, rest.Abort(http.StatusServiceUnavailable, "dry run: "+reason))
	})
}
