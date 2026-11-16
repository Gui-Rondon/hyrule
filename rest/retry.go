package rest

import (
	"errors"
	"time"
)

// RetryOptions bounds patchOnce's retry loop (the Postgres portability
// plan's section 9): how many times, and for how long, a PATCH whose
// If-Match is the wildcard "*" retries after an optimistic-concurrency
// conflict, instead of answering the first conflict with 412.
//
// There is no usable zero value: RetryOptions{} fails Validate() because
// every duration field must be positive regardless of MaxAttempts --
// start from DefaultRetryOptions() and override only what differs. An
// Adapter that never configures retrying (mongoAdapter today) exposes the
// zero value through RetryPolicy instead, which Service treats as "no
// retry capability" without ever calling Validate() on it -- Validate is
// for a caller that is about to use the loop, checked once at
// registration (e.g. restpostgres.NewAdapter), never on every request.
type RetryOptions struct {
	// MaxAttempts is retries BEYOND the initial attempt; 0 is valid
	// ("disabled" -- a single attempt, a conflict there is a plain 412,
	// never a 503); negative is an error.
	MaxAttempts int
	// BaseBackoff is the ceiling for the first retry's jitter; must be > 0.
	BaseBackoff time.Duration
	// MaxBackoff is the ceiling later retries saturate to; must be >= BaseBackoff.
	MaxBackoff time.Duration
	// MaxDuration bounds the retry loop's total wall-clock budget (only
	// meaningful once MaxAttempts > 0); must be > 0.
	MaxDuration time.Duration
}

// DefaultRetryOptions is the only recognized starting point for a
// RetryOptions value -- disable retrying by overriding just MaxAttempts
// to 0 from this, rather than building a RetryOptions{} from scratch.
func DefaultRetryOptions() RetryOptions {
	return RetryOptions{
		MaxAttempts: 5,
		BaseBackoff: 10 * time.Millisecond,
		MaxBackoff:  200 * time.Millisecond,
		MaxDuration: 2 * time.Second,
	}
}

// Validate reports whether r is usable: every duration field positive,
// MaxBackoff at least BaseBackoff, MaxAttempts not negative.
func (r RetryOptions) Validate() error {
	if r.MaxAttempts < 0 {
		return errors.New("rest: RetryOptions.MaxAttempts must be >= 0")
	}
	if r.BaseBackoff <= 0 {
		return errors.New("rest: RetryOptions.BaseBackoff must be > 0")
	}
	if r.MaxBackoff < r.BaseBackoff {
		return errors.New("rest: RetryOptions.MaxBackoff must be >= BaseBackoff")
	}
	if r.MaxDuration <= 0 {
		return errors.New("rest: RetryOptions.MaxDuration must be > 0")
	}
	return nil
}
