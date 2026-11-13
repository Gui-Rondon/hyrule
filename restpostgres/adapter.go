package restpostgres

import (
	"errors"
	"fmt"
	"time"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
)

// Options configures NewAdapter.
type Options struct {
	// Schema validates the Resource -- ToSchemaSpec converts it to the
	// neutral SchemaSpec the DSL compiler needs.
	Schema rest.Schema
	// Retry configures how a PATCH under If-Match: * retries an
	// optimistic-concurrency conflict -- a rest.Service-level concern
	// (Fase 4's patchOnce loop), stored here only so it's validated
	// once, at registration, rather than on every request.
	Retry RetryOptions
}

// RetryOptions bounds the retry loop a PATCH under If-Match: * runs on
// an optimistic-concurrency conflict. There is no zero-value default:
// a RetryOptions{} is invalid (every duration field must be positive)
// -- start from DefaultRetryOptions() and override only what differs.
type RetryOptions struct {
	// MaxAttempts is retries BEYOND the initial attempt; 0 is valid
	// ("disabled" -- a single attempt, a conflict there is a plain
	// 412, never a 503); negative is an error.
	MaxAttempts int
	// BaseBackoff is the ceiling for the first retry's jitter; must be > 0.
	BaseBackoff time.Duration
	// MaxBackoff is the ceiling later retries saturate to; must be >= BaseBackoff.
	MaxBackoff time.Duration
	// MaxDuration bounds the retry loop's total wall-clock budget
	// (only meaningful once MaxAttempts > 0); must be > 0.
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

func (r RetryOptions) validate() error {
	if r.MaxAttempts < 0 {
		return errors.New("RetryOptions.MaxAttempts must be >= 0")
	}
	if r.BaseBackoff <= 0 {
		return errors.New("RetryOptions.BaseBackoff must be > 0")
	}
	if r.MaxBackoff < r.BaseBackoff {
		return errors.New("RetryOptions.MaxBackoff must be >= BaseBackoff")
	}
	if r.MaxDuration <= 0 {
		return errors.New("RetryOptions.MaxDuration must be > 0")
	}
	return nil
}

// postgresAdapter implements rest.IDCodec and rest.ErrorClassifier in
// full, plus WriteLookup/MergePatch/EqualsFilter and the CRUD methods a
// future rest.Adapter (Fase 4 -- not defined yet; see ADR-0002) will
// need. It expects store's table to have the shape
// restpostgres/adapter_test.go's setupResourceTable creates: id uuid
// primary key, doc jsonb, created_at/updated_at timestamptz, version
// bigint, deleted boolean.
type postgresAdapter struct {
	store  postgres.Store
	tx     postgres.Transactor
	schema postgres.SchemaSpec
	retry  RetryOptions
}

var (
	_ rest.IDCodec         = (*postgresAdapter)(nil)
	_ rest.ErrorClassifier = (*postgresAdapter)(nil)
)

// NewAdapter validates opts and builds an adapter over store's table.
// Errors here are a registration-time failure (the resource never
// starts), never a per-request one.
func NewAdapter(store postgres.Store, tx postgres.Transactor, opts Options) (*postgresAdapter, error) {
	schemaSpec, err := ToSchemaSpec(opts.Schema)
	if err != nil {
		return nil, fmt.Errorf("restpostgres: %w", err)
	}
	if err := opts.Retry.validate(); err != nil {
		return nil, fmt.Errorf("restpostgres: %w", err)
	}
	return &postgresAdapter{store: store, tx: tx, schema: schemaSpec, retry: opts.Retry}, nil
}
