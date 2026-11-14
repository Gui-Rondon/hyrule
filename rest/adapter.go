package rest

import "context"

// ErrorClassifier tells Service what kind of failure a Store operation
// returned, so the HTTP status it picks does not depend on checking a
// backend-specific error value (mongo.ErrNoDocuments, mongo.IsTimeout...)
// at each call site.
//
// Fase 0 of the Postgres portability work (see
// website/docs/reference/architecture-decisions.md, ADR-0002): extracted
// out of rest/service.go, with mongoAdapter implementing it exactly as
// that code already behaved — no behavior change for Mongo.
type ErrorClassifier interface {
	// IsNotFound reports whether err means a lookup found no document.
	IsNotFound(err error) bool
	// IsDuplicateKey reports whether err means a unique index was
	// violated.
	IsDuplicateKey(err error) bool
	// IsClientError reports whether err was caused by the client's query
	// itself (a bad filter value, an invalid regular expression...), not
	// the server or the connection.
	IsClientError(err error) bool
	// IsTimeout reports whether err means the operation ran out of time.
	IsTimeout(err error) bool
	// ClassifiesWriteTimeouts reports whether Service's writeError
	// (section 12 of the Postgres portability plan) may run a write's
	// backend error through IsTimeout at all, turning a true result
	// into a 503 before returning it from the Transactor.WithTransaction
	// closure it ran inside of. mongoAdapter returns false:
	// platform/mongodb's Transactor.WithTransaction hands that very
	// closure's return value straight to the driver's own
	// sess.WithTransaction, which inspects the *original* error for a
	// TransientTransactionError label to decide whether to silently
	// retry the whole transaction -- replacing it with Abort(503, "")
	// here, even only for a write IsTimeout would also recognize,
	// would erase that label and turn an error the driver already
	// retries transparently into an immediate, un-retried 503.
	// postgresAdapter returns true: platform/postgres's Transactor has
	// no such label-based retry (a plain BeginTx/fn/Commit/Rollback,
	// confirmed by reading it), so nothing is lost by classifying
	// 57014/40P01/40001 as 503 from inside the closure.
	ClassifiesWriteTimeouts() bool
}

// IDCodec generates and parses the `_id` field's values.
type IDCodec interface {
	// NewID generates the id of a document being inserted.
	NewID() any
	// IDString renders id the way it appears in a URL or a response body.
	IDString(id any) string
	// LookupByID parses value, taken from a URL path segment, as an id;
	// ok is false when value does not look like an id at all.
	LookupByID(value string) (id any, ok bool)
	// LookupID adapts an id already stored in a Doc for use inside a
	// filter (e.g. excluding the current document from a uniqueness
	// check).
	LookupID(id any) any
}

// QueryAdapter parses the client's where/sort/projection query parameters
// and combines them with a Resource's own defaults (datasource filter,
// soft delete, default sort, datasource projection), and builds the CAS
// lookup a write conditions on. Same Fase 0 extraction as ErrorClassifier/
// IDCodec: mongoAdapter implements it exactly as rest/query.go and
// rest/service.go already behaved.
type QueryAdapter interface {
	// ParseWhere decodes the client's "where" query parameter into a
	// filter, applying res's QueryDenylist/QueryObjectIDAsString.
	ParseWhere(raw string, res *Resource) (any, error)
	// ParseSort decodes the client's "sort" query parameter.
	ParseSort(raw string) (any, error)
	// ParseProjection decodes the client's "projection" query parameter.
	ParseProjection(raw string) (any, error)

	// CombineFilter combines clientFilter with res's datasource filter
	// and, unless includeDeleted, the soft delete condition.
	CombineFilter(res *Resource, clientFilter any, includeDeleted bool) (any, error)
	// CombineSort falls back to res's Datasource.DefaultSort when
	// clientSort is empty.
	CombineSort(res *Resource, clientSort any) any
	// CombineProjection narrows res's datasource projection by
	// clientProjection, or applies clientProjection as sent when res has
	// none (AllowUnknown).
	CombineProjection(res *Resource, clientProjection any) (any, error)

	// WriteLookup builds the CAS condition a write conditions on, from
	// the document read before the write (original) -- always from
	// original, never from a client's match/SkipConcurrency.
	WriteLookup(original Doc) (any, error)

	// IDFilter builds a filter selecting the single document whose _id
	// equals id (an IDCodec.NewID/LookupByID value) -- combinable with
	// CombineFilter exactly like a client's ParseWhere result, so an
	// item route (GET/PATCH/PUT/DELETE /<url>/<id>) can reach the right
	// document through the same CombineFilter/datasource-filter/soft-
	// delete path a collection query already does, instead of a
	// Mongo-only bson.M{"_id": id} literal hardcoded at the call site
	// (http.go's itemLookup, service.go's checkMatched, before this
	// method existed).
	IDFilter(id any) (any, error)

	// RetryPolicy returns the RetryOptions this adapter was configured
	// with -- the budget Service's patchOnce retry loop uses for a PATCH
	// whose If-Match is the wildcard "*" (section 9.7). mongoAdapter
	// returns the zero RetryOptions: Mongo keeps today's behavior
	// exactly, a single attempt, so a version conflict under
	// If-Match: * is still a plain 412, never a 503.
	RetryPolicy() RetryOptions
}

// Adapter is the full backend seam Service writes through -- the
// unified interface ADR-0002/this plan's section 3 describes.
// QueryAdapter/IDCodec/ErrorClassifier (above) were extracted first
// (Fase 0), as three separate, independently testable interfaces;
// Adapter embeds all three and adds the CRUD/merge/uniqueness methods
// a Resource needs to never touch Store directly. mongoAdapter (this
// package, Fase 4a) and restpostgres's postgresAdapter (Fase 2-4b)
// both implement it in full, each with its own
// `var _ Adapter = (*...)(nil)` assertion.
//
// Fase 0/2's history, not repeated here to reopen it: Store stays
// bson.M/bson.D-typed (rest/store.go) -- Adapter does not replace it,
// it sits in front of it. A Resource with Adapter set never calls
// Store at all (Resource.setDefaults only builds a mongoAdapter
// wrapping Store when Adapter is nil); a Postgres Resource sets
// Adapter directly and leaves Store nil.
type Adapter interface {
	QueryAdapter
	IDCodec
	ErrorClassifier

	// Find lists documents; filter/sort/projection are whatever
	// CombineFilter/CombineSort/CombineProjection returned. limit <= 0
	// means no skip/limit is applied. Parameter order intentionally
	// matches plan section 3's pseudocode, not Store.Find's (filter,
	// skip, limit, sort, projection) -- mongoAdapter.Find is the one
	// place that transposes between the two; a second implementation
	// should double check it against this doc comment, not Store's.
	Find(ctx context.Context, filter, sort, projection any, skip, limit int64) ([]Doc, error)
	CountDocuments(ctx context.Context, filter any) (int64, error)
	// FindOne reads one document; lookup is whatever CombineFilter (or
	// WriteLookup, for a write's own re-read) returned.
	FindOne(ctx context.Context, lookup, projection any) (Doc, error)
	// InsertMany writes docs, computing and setting each one's _etag in
	// place -- _id/_created/_updated are already set by Service.Insert,
	// before the OnInsert hooks ran; _etag is adapter-specific (a
	// content hash for Mongo, "1" for a fresh Postgres row) and is set
	// here, after the hooks, immediately before the write.
	InsertMany(ctx context.Context, docs []Doc) error
	// UpdateOne writes updated's business fields changed (changes
	// names which keys) at lookup, and sets updated's final _etag/
	// _updated in place before returning -- a fresh value the
	// implementation is itself responsible for, computed however its
	// backend actually determines it: a content hash set right before
	// the write for a backend with no way to read a value back (Mongo;
	// mongoAdapter.setEtag), or the value a RETURNING clause confirmed
	// the write actually produced, for a backend that has one
	// (Postgres's version counter). Either way, the caller never
	// computes or sets _etag/_updated itself. matched == 0 without an
	// error is not a failure: the caller (Service's checkMatched)
	// decides 404 vs 412 by re-reading.
	UpdateOne(ctx context.Context, lookup any, updated, changes Doc) (matched int64, err error)
	// ReplaceOne overwrites the whole document at lookup, setting
	// replacement's final _etag/_updated in place, same contract as
	// UpdateOne.
	ReplaceOne(ctx context.Context, lookup any, replacement Doc) (matched int64, err error)
	DeleteOne(ctx context.Context, lookup any) (deleted int64, err error)

	// MergePatch applies changes to original's business fields only
	// (original's meta fields pass through untouched) -- Eve's
	// MERGE_NESTED_DOCUMENTS deep merge for Mongo, RFC 7396 JSON Merge
	// Patch for Postgres (seção 10, deliberate divergence). The caller
	// sets _updated/_etag on the result afterward.
	MergePatch(original, changes Doc) (Doc, error)
	// ValidatesMergedDocument reports whether patchOnce (section 9.4)
	// must validate the whole post-merge document against the
	// resource's Schema before writing it, in addition to the
	// pre-merge validation of the client's own PATCH body that always
	// runs. mongoAdapter returns false -- Mongo keeps today's exact
	// behavior, validating only what the client sent. postgresAdapter
	// returns true: RFC 7396's merge can delete a key the Schema marks
	// Required (a Required+Nullable field's null never means "store
	// null", it means "remove the key"), and only a check against the
	// merged result catches that before the write.
	ValidatesMergedDocument() bool
	// EqualsFilter builds "field == value", optionally excluding one
	// document by excludeID -- the friendly pre-check a Schema's
	// unique validation runs before a write; the real uniqueness
	// guarantee is the backend's own unique index.
	EqualsFilter(field string, value any, excludeID any) (any, error)
}
