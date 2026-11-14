package rest

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"time"
)

// backoffWithJitter is the delay before retry attempt retryNum (1 for the
// first retry): doubling from base, saturating at max, then a uniform
// random draw in [0, d) so concurrent retriers don't all wake up at once.
func backoffWithJitter(retryNum int, base, max time.Duration) time.Duration {
	if base <= 0 {
		base = time.Millisecond
	}
	if max < base {
		max = base
	}
	d := base
	for i := 1; i < retryNum; i++ {
		if d > max/2 {
			d = max
			break
		}
		d *= 2
	}
	if d > max {
		d = max
	}
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(d)))
}

// classifyContextErr decides, uniformly, the cause of a context error
// observed during an operation that used retryCtx:
//  1. ctx (the original caller) expired or was canceled -> propagated as is.
//  2. only retryCtx expired (ctx still valid) -> the retry loop's own
//     budget, reported as a 503 with RetryExhaustedReason "max_duration".
//  3. neither ctx nor retryCtx expired, yet err is a context error anyway
//     (an independent source, e.g. a hook's own timeout) -> preserved as
//     is, never attributed to the retry budget.
//
// A non-context error (database, hook, validation) passes through
// unchanged in every case.
func classifyContextErr(ctx, retryCtx context.Context, err error) error {
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if retryCtx.Err() != nil {
		return abortMaxDurationExhausted()
	}
	return err
}

func abortMaxAttemptsExhausted() error {
	e := Abort(http.StatusServiceUnavailable, "")
	e.RetryExhaustedReason = "max_attempts"
	return e
}

func abortMaxDurationExhausted() error {
	e := Abort(http.StatusServiceUnavailable, "")
	e.RetryExhaustedReason = "max_duration"
	return e
}
