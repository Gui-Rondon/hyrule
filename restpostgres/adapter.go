package restpostgres

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Gui-Rondon/hyrule/platform/postgres"
	"github.com/Gui-Rondon/hyrule/rest"
)

// Options configures NewAdapter.
type Options struct {
	// Schema validates the Resource -- ToSchemaSpec converts it to the
	// neutral SchemaSpec the DSL compiler needs.
	Schema rest.Schema
	// SoftDelete marks documents with _deleted instead of removing
	// them -- mirrors rest.Resource.SoftDelete. A resource without it
	// never has a _deleted field in its Doc, same as Mongo's.
	SoftDelete bool
	// Retry configures how a PATCH under If-Match: * retries an
	// optimistic-concurrency conflict -- rest.Service's patchOnce loop
	// reads it back through RetryPolicy, so it's validated once, here
	// at registration, rather than on every request. rest.RetryOptions{}
	// (the zero value) is invalid -- start from rest.DefaultRetryOptions()
	// and override only what differs.
	Retry rest.RetryOptions
}

// postgresAdapter implements rest.Adapter in full (Fase 4b's first
// cut completed it: QueryAdapter, in query.go, was the missing
// third -- IDCodec/ErrorClassifier were already complete since Fase
// 2, plus the CRUD/MergePatch/EqualsFilter methods below). It expects
// store's table to have the shape restpostgres/adapter_test.go's
// setupResourceTable creates: id uuid primary key, doc jsonb,
// created_at/updated_at timestamptz, version bigint, deleted boolean.
type postgresAdapter struct {
	store  postgres.Store
	tx     postgres.Transactor
	schema postgres.SchemaSpec
	retry  rest.RetryOptions
	// table is store.Table, sanitized once here -- store.Table never
	// changes for the adapter's lifetime, so every CRUD method reuses
	// this instead of re-sanitizing the same identifier on every call.
	table      string
	softDelete bool
}

var _ rest.Adapter = (*postgresAdapter)(nil)

// RetryPolicy returns the RetryOptions NewAdapter validated at
// registration -- rest.Service's patchOnce loop reads it from here
// instead of the caller duplicating it onto rest.Resource too.
func (a *postgresAdapter) RetryPolicy() rest.RetryOptions { return a.retry }

// ValidatesMergedDocument is always true: RFC 7396's merge (MergePatch,
// patch.go) can delete a key the Schema marks Required -- a null in the
// PATCH body means "remove this key", never "store null" -- so
// rest.Service's patchOnce validates the merged result against the full
// Schema before writing it, not just the client's own PATCH body.
func (a *postgresAdapter) ValidatesMergedDocument() bool { return true }

// NewAdapter validates opts and builds an adapter over store's table.
// Errors here are a registration-time failure (the resource never
// starts), never a per-request one.
func NewAdapter(store postgres.Store, tx postgres.Transactor, opts Options) (*postgresAdapter, error) {
	if store.Pool == nil {
		return nil, errors.New("restpostgres: store.Pool must not be nil")
	}
	if store.Table == "" {
		return nil, errors.New("restpostgres: store.Table must not be empty")
	}
	schemaSpec, err := ToSchemaSpec(opts.Schema)
	if err != nil {
		return nil, fmt.Errorf("restpostgres: %w", err)
	}
	if err := opts.Retry.Validate(); err != nil {
		return nil, fmt.Errorf("restpostgres: %w", err)
	}
	return &postgresAdapter{
		store:      store,
		tx:         tx,
		schema:     schemaSpec,
		retry:      opts.Retry,
		table:      pgx.Identifier{store.Table}.Sanitize(),
		softDelete: opts.SoftDelete,
	}, nil
}
