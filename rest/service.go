package rest

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Service runs the operations of a resource with Eve's rules. It is the
// equivalent of Eve's getitem/post_internal/patch_internal/put_internal/
// deleteitem_internal and has no HTTP concerns.
type Service struct {
	res          *Resource
	tx           Transactor
	validator    validator
	queryTimeout time.Duration
	// retry is res.Adapter.RetryPolicy(), read once at construction --
	// patchOnce's retry loop (section 9 of the Postgres portability
	// plan) uses it for a PATCH whose If-Match is the wildcard "*".
	// Mongo's adapter returns the zero RetryOptions, so this stays
	// inert (MaxAttempts 0) for every resource that doesn't opt in.
	retry RetryOptions
	// dryRun skips the post-write hooks: the transaction was rolled back
	dryRun bool
}

// readContext bounds a read by the query timeout.
func (s *Service) readContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.queryTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.queryTimeout)
}

// NewService builds a Service; tx may be nil for no transactions.
func NewService(res *Resource, tx Transactor) *Service {
	res.setDefaults()
	res.Datasource.Projection = schemaProjection(res)
	if tx == nil {
		tx = NoTransaction{}
	}
	return &Service{
		res:   res,
		tx:    tx,
		retry: res.Adapter.RetryPolicy(),
		validator: validator{
			schema:       res.Schema,
			allowUnknown: res.AllowUnknown,
			adapter:      res.Adapter,
			custom:       res.Validate,
		},
	}
}

// Resource returns the resource definition.
func (s *Service) Resource() *Resource { return s.res }

// WithTransaction runs fn in the transaction used by the writes of the
// resource, for custom endpoints that write directly and record outbox
// events.
func (s *Service) WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	return s.tx.WithTransaction(ctx, fn)
}

// CheckEtag is Eve's If-Match check for custom endpoints: 428 without it, 412
// when it differs from the stored etag. ifMatch is the raw header value (one
// line's worth; use ParseIfMatch directly for several repeated headers, or
// for the wildcard "*" to be recognized as matching any current etag).
func (s *Service) CheckEtag(original Doc, ifMatch string) error {
	match, _ := ParseIfMatch([]string{ifMatch})
	return s.checkEtag(original, WriteOptions{IfMatch: match})
}

// WriteOptions control the concurrency and validation checks of a write.
type WriteOptions struct {
	// IfMatch is the client's parsed If-Match precondition. A zero
	// IfMatch (IsZero true) is "no header sent" -- the same as an empty
	// string before this field's type changed from string to IfMatch.
	IfMatch IfMatch
	// SkipConcurrency disables the If-Match check, as Eve's *_internal
	// functions do by default.
	SkipConcurrency bool
	// SkipValidation disables the schema validation.
	SkipValidation bool
}

// ListResult is a page of documents. Total is -1 when the resource
// optimizes pagination for speed.
type ListResult struct {
	Docs  []Doc
	Total int64
}

// List returns a page of documents (Eve's get).
func (s *Service) List(ctx context.Context, q *ListQuery) (*ListResult, error) {
	for _, hook := range s.res.Hooks.OnPreGet {
		if err := hook(ctx, q); err != nil {
			return nil, err
		}
	}
	var filter any
	var err error
	if q.ShowDeleted {
		filter, err = s.filterIncludingDeleted(q.Filter)
	} else {
		filter, err = s.readFilter(q.Filter)
	}
	if err != nil {
		return nil, err
	}

	var skip, limit int64
	if q.MaxResults > 0 {
		skip, limit = int64((q.Page-1)*q.MaxResults), int64(q.MaxResults)
	}
	sort := s.res.Adapter.CombineSort(s.res, q.Sort)
	projection, err := s.projection(q.Projection)
	if err != nil {
		return nil, err
	}

	readCtx, cancel := s.readContext(ctx)
	defer cancel()
	docs, err := s.res.Adapter.Find(readCtx, filter, sort, projection, skip, limit)
	if err != nil {
		return nil, s.queryError(err)
	}
	if docs == nil {
		docs = []Doc{}
	}

	total := int64(-1)
	if !s.res.Pagination.OptimizeForSpeed && !s.res.Pagination.Disabled {
		if total, err = s.res.Adapter.CountDocuments(readCtx, filter); err != nil {
			return nil, s.queryError(err)
		}
	}
	for _, hook := range s.res.Hooks.OnFetchedResource {
		if err := hook(ctx, docs); err != nil {
			return nil, err
		}
	}
	return &ListResult{Docs: docs, Total: total}, nil
}

// Get returns one document with the default projection and the fetched
// hooks applied (Eve's getitem).
func (s *Service) Get(ctx context.Context, lookup any) (Doc, error) {
	return s.get(ctx, lookup, false, nil)
}

// getIncludingDeleted is Get for getitem, which fetches soft deleted
// documents to answer them with a 404 that carries the document, with the
// client projection of the request.
func (s *Service) getIncludingDeleted(ctx context.Context, lookup, client any) (Doc, error) {
	return s.get(ctx, lookup, true, client)
}

func (s *Service) get(ctx context.Context, lookup any, includeDeleted bool, client any) (Doc, error) {
	projection, err := s.projection(client)
	if err != nil {
		return nil, err
	}
	var doc Doc
	if includeDeleted {
		doc, err = s.findOneIncludingDeleted(ctx, lookup, projection)
	} else {
		doc, err = s.findOne(ctx, lookup, projection)
	}
	if err != nil {
		return nil, err
	}
	for _, hook := range s.res.Hooks.OnFetchedItem {
		if err := hook(ctx, doc); err != nil {
			return nil, err
		}
	}
	return doc, nil
}

// Find returns the stored document without hooks or projection.
func (s *Service) Find(ctx context.Context, lookup any) (Doc, error) {
	return s.findOne(ctx, lookup, nil)
}

func (s *Service) findOne(ctx context.Context, lookup, projection any) (Doc, error) {
	filter, err := s.readFilter(lookup)
	if err != nil {
		return nil, err
	}
	return s.findOneWith(ctx, filter, projection)
}

func (s *Service) findOneIncludingDeleted(ctx context.Context, lookup, projection any) (Doc, error) {
	filter, err := s.filterIncludingDeleted(lookup)
	if err != nil {
		return nil, err
	}
	return s.findOneWith(ctx, filter, projection)
}

func (s *Service) findOneWith(ctx context.Context, filter, projection any) (Doc, error) {
	readCtx, cancel := s.readContext(ctx)
	defer cancel()
	doc, err := s.res.Adapter.FindOne(readCtx, filter, projection)
	if s.res.Adapter.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, s.queryError(err)
	}
	return doc, nil
}

// Insert validates and stores documents (Eve's post_internal). When a
// document is invalid nothing is stored and an *InsertError is returned.
func (s *Service) Insert(ctx context.Context, docs []Doc, opts WriteOptions) ([]Doc, error) {
	if !opts.SkipValidation {
		results := make([]Issues, len(docs))
		failed := 0
		for i, doc := range docs {
			issues, err := s.validator.validate(ctx, doc, false, nil)
			if err != nil {
				return nil, err
			}
			if len(issues) > 0 {
				results[i] = issues
				failed++
			}
		}
		if failed > 0 {
			return nil, &InsertError{Results: results, Failed: failed}
		}
	}

	if err := s.prepare(ctx, docs...); err != nil {
		return nil, err
	}

	ts := now()
	for _, doc := range docs {
		doc["_created"] = ts
		doc["_updated"] = ts
		if s.res.SoftDelete {
			doc["_deleted"] = false
		}
		// assigned before the hooks so an outbox event can reference it
		if _, ok := doc["_id"]; !ok {
			doc["_id"] = s.res.Adapter.NewID()
		}
	}

	var stored []Doc
	err := s.tx.WithTransaction(ctx, func(ctx context.Context) error {
		stored = make([]Doc, len(docs))
		for i, doc := range docs {
			stored[i] = maps.Clone(doc)
		}
		for _, hook := range s.res.Hooks.OnInsert {
			if err := hook(ctx, stored); err != nil {
				return err
			}
		}
		err := s.res.Adapter.InsertMany(ctx, stored)
		return s.writeError(err, 409)
	})
	if err != nil {
		return nil, err
	}
	for i := range docs {
		keep(docs[i], stored[i])
	}
	runPost(ctx, s.dryRun, "on_inserted", s.res.Hooks.OnInserted, func(h func(context.Context, []Doc) error) error {
		return h(ctx, docs)
	})
	return docs, nil
}

// Patch updates some fields (Eve's patch). Nested documents are merged, as
// Eve's MERGE_NESTED_DOCUMENTS does. It returns the updated document.
//
// A plain If-Match (one or more concrete entity-tags, or none at all) runs
// patchOnce exactly once, same as before this method gained a retry loop:
// a version conflict there is the usual 412, whether or not the resource's
// Adapter.RetryPolicy ever configures retrying. The loop only engages for
// the wildcard "If-Match: *" on a resource whose RetryPolicy has
// MaxAttempts > 0 (restpostgres's postgresAdapter, when its Options.Retry
// was given one; mongoAdapter never does) -- section 9.7 of the Postgres
// portability plan: retry an optimistic-concurrency conflict instead of
// answering the first one with 412, since the client already said it
// doesn't care which version it's patching.
func (s *Service) Patch(ctx context.Context, lookup any, changes Doc, opts WriteOptions) (Doc, error) {
	if !opts.IfMatch.Any || s.retry.MaxAttempts == 0 {
		return s.patchOnce(ctx, lookup, changes, opts) // no retryCtx, no loop -- a conflict here is 412, never 503
	}

	retryCtx, cancel := context.WithTimeout(ctx, s.retry.MaxDuration)
	defer cancel()

	for attempt := 0; attempt <= s.retry.MaxAttempts; attempt++ {
		if attempt > 0 {
			d := backoffWithJitter(attempt, s.retry.BaseBackoff, s.retry.MaxBackoff)
			select {
			case <-time.After(d):
			case <-retryCtx.Done():
				return nil, classifyContextErr(ctx, retryCtx, retryCtx.Err())
			}
		}
		updated, err := s.patchOnce(retryCtx, lookup, changes, opts)
		if err == nil {
			return updated, nil
		}
		if _, ok := errors.AsType[*PreconditionFailedError](err); ok {
			continue // the only reason to retry: a version conflict under If-Match: *
		}
		return nil, classifyContextErr(ctx, retryCtx, err)
	}

	// MaxAttempts ran out -- but it may have tied with the context
	// expiring too; check explicitly before blaming "ran out of
	// attempts" (section 9.7/9.8).
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if retryCtx.Err() != nil {
		return nil, abortMaxDurationExhausted()
	}
	return nil, abortMaxAttemptsExhausted()
}

// patchOnce is Patch's single-attempt body, reused by both the no-retry
// path and every iteration of its retry loop.
func (s *Service) patchOnce(ctx context.Context, lookup any, changes Doc, opts WriteOptions) (Doc, error) {
	original, err := s.prepareWrite(ctx, lookup, changes, true, opts)
	if err != nil {
		return nil, err
	}

	var updated, applied Doc
	err = s.tx.WithTransaction(ctx, func(ctx context.Context) error {
		applied = maps.Clone(changes)
		for _, hook := range s.res.Hooks.OnUpdate {
			if err := hook(ctx, applied, original); err != nil {
				return err
			}
		}
		updated, err = s.res.Adapter.MergePatch(original, applied)
		if err != nil {
			return err
		}
		updated["_updated"] = now()
		if s.res.SoftDelete {
			// patch.py stores it on every PATCH; after the hooks, which in
			// Go see only the client's changes
			updated["_deleted"] = false
		}

		// Adapter.ValidatesMergedDocument (section 9.4 of the Postgres
		// portability plan): only for an adapter that opts in (Postgres)
		// -- RFC 7396's merge can delete a key the Schema marks Required,
		// which the pre-merge validation in prepareWrite above (the
		// client's own PATCH body, before any merge) never sees, since
		// Nullable only means a Required field's *stored value* may be
		// null, never that its key may be absent, and RFC 7396 has no
		// way to set a value to null without deleting the key.
		//
		// Deliberately narrow: only checks which Required keys are
		// present in updated, nothing else. An earlier version of this
		// check ran the full validateDict (type/format) pass over the
		// whole merged document, found by /code-review to be the wrong
		// depth: updated's values are in storage/converted
		// representation (a touched TypeInteger/TypeObjectID/
		// TypeDatetime field is already an int64/bson.ObjectID/
		// time.Time, from convert() in prepareWrite's pre-merge pass; an
		// untouched one read back from Postgres is whatever hydrate's
		// json.Unmarshal of jsonb produced, e.g. an RFC 3339 string for
		// a date, never Eve's wire format) -- checkType expects raw
		// client JSON, so it rejected a field that was never actually
		// wrong, on every single PATCH that merely had a non-string
		// field already set. Presence alone needs none of that: it
		// never inspects a value's type, so it never cares which
		// representation updated[key] happens to be in, and also never
		// treats a hook-added field the Schema doesn't declare (e.g.
		// behaviors/audit's _updated_user) as "unknown field" -- it
		// only ever looks at the Schema's own Required keys.
		if !opts.SkipValidation && s.res.Adapter.ValidatesMergedDocument() {
			missing := Issues{}
			for key, field := range s.res.Schema {
				if _, ok := updated[key]; !ok && field.Required && field.Default == nil {
					missing[key] = "required field"
				}
			}
			if len(missing) > 0 {
				return &ValidationError{Issues: missing}
			}
		}

		writeLookup, err := s.res.Adapter.WriteLookup(original)
		if err != nil {
			return err
		}
		// like Eve's resolve_nested_documents: changed top level fields are
		// written whole, with nested documents already merged. UpdateOne
		// sets updated["_etag"] (and may refresh updated["_updated"]) from
		// what the write actually confirmed -- never from a value computed
		// here.
		matched, err := s.res.Adapter.UpdateOne(ctx, writeLookup, updated, applied)
		if err != nil {
			return s.writeError(err, 400)
		}
		return s.checkMatched(ctx, matched, original)
	})
	if err != nil {
		return nil, err
	}
	keep(changes, applied)
	// Eve's on_updated receives the updates with the new _updated and _etag
	// (and _deleted with soft delete): searchsync indexes them, as
	// resource_callbacks.py does
	updates := maps.Clone(changes)
	updates["_updated"] = updated["_updated"]
	updates["_etag"] = updated["_etag"]
	if s.res.SoftDelete {
		updates["_deleted"] = false
	}
	runPost(ctx, s.dryRun, "on_updated", s.res.Hooks.OnUpdated, func(h func(context.Context, Doc, Doc) error) error {
		return h(ctx, updates, original)
	})
	return updated, nil
}

// Replace stores a new version of the document keeping _id and _created
// (Eve's put). It returns the stored document.
func (s *Service) Replace(ctx context.Context, lookup any, doc Doc, opts WriteOptions) (Doc, error) {
	original, err := s.prepareWrite(ctx, lookup, doc, false, opts)
	if err != nil {
		return nil, err
	}

	var stored Doc
	err = s.tx.WithTransaction(ctx, func(ctx context.Context) error {
		stored = maps.Clone(doc)
		for _, hook := range s.res.Hooks.OnReplace {
			if err := hook(ctx, stored, original); err != nil {
				return err
			}
		}
		stored["_id"] = original["_id"]
		stored["_created"] = original["_created"]
		stored["_updated"] = now()
		if s.res.SoftDelete {
			stored["_deleted"] = false
		}
		writeLookup, err := s.res.Adapter.WriteLookup(original)
		if err != nil {
			return err
		}
		// ReplaceOne sets stored["_etag"] (and may refresh
		// stored["_updated"]) from what the write actually confirmed.
		matched, err := s.res.Adapter.ReplaceOne(ctx, writeLookup, stored)
		if err != nil {
			return s.writeError(err, 400)
		}
		return s.checkMatched(ctx, matched, original)
	})
	if err != nil {
		return nil, err
	}
	keep(doc, stored)
	runPost(ctx, s.dryRun, "on_replaced", s.res.Hooks.OnReplaced, func(h func(context.Context, Doc, Doc) error) error {
		return h(ctx, doc, original)
	})
	return doc, nil
}

// Delete removes the document, or marks it with _deleted when the resource
// uses soft delete (Eve's deleteitem).
func (s *Service) Delete(ctx context.Context, lookup any, opts WriteOptions) error {
	original, err := s.Find(ctx, lookup)
	if err != nil {
		return err
	}
	if err := s.checkEtag(original, opts); err != nil {
		return err
	}
	err = s.tx.WithTransaction(ctx, func(ctx context.Context) error {
		for _, hook := range s.res.Hooks.OnDelete {
			if err := hook(ctx, original); err != nil {
				return err
			}
		}
		writeLookup, err := s.res.Adapter.WriteLookup(original)
		if err != nil {
			return err
		}
		if !s.res.SoftDelete {
			deletedCount, err := s.res.Adapter.DeleteOne(ctx, writeLookup)
			if err != nil {
				return s.timeoutError(err)
			}
			return s.checkMatched(ctx, deletedCount, original)
		}
		deleted := maps.Clone(original)
		deleted["_deleted"] = true
		deleted["_updated"] = now()
		// UpdateOne sets deleted["_etag"] from what the write actually
		// confirmed; no "changes" here, every key it writes comes from
		// deleted itself (_deleted/_updated/_etag).
		matched, err := s.res.Adapter.UpdateOne(ctx, writeLookup, deleted, Doc{})
		if err != nil {
			return s.timeoutError(err)
		}
		return s.checkMatched(ctx, matched, original)
	})
	if err != nil {
		return err
	}
	runPost(ctx, s.dryRun, "on_deleted", s.res.Hooks.OnDeleted, func(h func(context.Context, Doc) error) error {
		return h(ctx, original)
	})
	return nil
}

// prepareWrite loads the original and runs the concurrency and validation
// checks in Eve's order: 404, 428/412, then 422.
func (s *Service) prepareWrite(ctx context.Context, lookup any, doc Doc, partial bool, opts WriteOptions) (Doc, error) {
	original, err := s.Find(ctx, lookup)
	if err != nil {
		return nil, err
	}
	if err := s.checkEtag(original, opts); err != nil {
		return nil, err
	}
	if !opts.SkipValidation {
		issues, err := s.validator.validate(ctx, doc, partial, original)
		if err != nil {
			return nil, err
		}
		if len(issues) > 0 {
			return nil, &ValidationError{Issues: issues}
		}
	}
	if err := s.prepareWith(ctx, original, doc); err != nil {
		return nil, err
	}
	return original, nil
}

// prepare runs the OnPrepare hooks of new documents.
func (s *Service) prepare(ctx context.Context, docs ...Doc) error {
	for _, doc := range docs {
		if err := s.prepareWith(ctx, nil, doc); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) prepareWith(ctx context.Context, original, doc Doc) error {
	for _, hook := range s.res.Hooks.OnPrepare {
		if err := hook(ctx, doc, original); err != nil {
			return err
		}
	}
	return nil
}

// keep makes dst the document the transaction stored. The driver retries
// the whole transaction function on transient errors, so each attempt runs
// the hooks on a fresh copy of the request document (a hook that pops a
// field must find it again); the copy is shallow, so hooks must change
// nested values idempotently.
func keep(dst, stored Doc) {
	clear(dst)
	maps.Copy(dst, stored)
}

func (s *Service) checkEtag(original Doc, opts WriteOptions) error {
	if opts.SkipConcurrency {
		return nil
	}
	if opts.IfMatch.IsZero() {
		return ErrPreconditionRequired
	}
	candidates := append([]string{etagOf(original)}, s.compatibleEtags(original)...)
	if !opts.IfMatch.StrongMatchesAny(candidates...) {
		return &PreconditionFailedError{Current: original}
	}
	return nil
}

// compatibleEtags lists the older etags the document's CompatibleEtags
// field carries (writes that did not touch what the client edits put
// them there) -- nil when the resource has no such field.
func (s *Service) compatibleEtags(original Doc) []string {
	if s.res.CompatibleEtags == "" {
		return nil
	}
	var list []any
	switch v := original[s.res.CompatibleEtags].(type) {
	case bson.A:
		list = v
	case []any:
		list = v
	default:
		return nil
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// writeLookup conditions the write on the etag read, as Eve 2.x's
// _change_request does, so concurrent writers cannot both win --
// mongoAdapter.WriteLookup's body (Fase 0/4a, rest.Adapter.WriteLookup);
// Service calls it only through s.res.Adapter.WriteLookup now, never
// directly.
func writeLookup(original Doc) bson.M {
	filter := bson.M{"_id": original["_id"]}
	if etag, ok := original["_etag"].(string); ok && etag != "" {
		filter["_etag"] = etag
	}
	return filter
}

func (s *Service) checkMatched(ctx context.Context, matched int64, original Doc) error {
	if matched > 0 {
		return nil
	}
	idFilter, err := s.res.Adapter.IDFilter(original["_id"])
	if err != nil {
		return err
	}
	current, err := s.Find(ctx, idFilter)
	if err != nil {
		return err
	}
	return &PreconditionFailedError{Current: current}
}

// combineSort is List's inline sort fallback, extracted (Fase 0,
// rest.Adapter's CombineSort): clientSort, or res's Datasource.DefaultSort
// when clientSort is empty.
func combineSort(res *Resource, clientSort bson.D) bson.D {
	if len(clientSort) == 0 {
		return res.Datasource.DefaultSort
	}
	return clientSort
}

// readFilter combines the lookup with the datasource filter and soft delete.
func (s *Service) readFilter(lookup any) (any, error) {
	return s.filter(lookup, false)
}

// filterIncludingDeleted is readFilter without the soft delete condition.
func (s *Service) filterIncludingDeleted(lookup any) (any, error) {
	return s.filter(lookup, true)
}

func (s *Service) filter(lookup any, includeDeleted bool) (any, error) {
	return s.res.Adapter.CombineFilter(s.res, lookup, includeDeleted)
}

// combineFilter is filter's body, extracted (Fase 0, rest.Adapter's
// CombineFilter) so mongoAdapter can expose the same, unchanged logic.
func combineFilter(res *Resource, lookup bson.M, includeDeleted bool) bson.M {
	var parts []bson.M
	if len(res.Datasource.Filter) > 0 {
		parts = append(parts, res.Datasource.Filter)
	}
	if len(lookup) > 0 {
		parts = append(parts, lookup)
	}
	if res.SoftDelete && !includeDeleted {
		parts = append(parts, bson.M{"_deleted": bson.M{"$ne": true}})
	}
	switch len(parts) {
	case 0:
		return bson.M{}
	case 1:
		return parts[0]
	}
	all := make(bson.A, len(parts))
	for i, p := range parts {
		all[i] = p
	}
	return bson.M{"$and": all}
}

// metaFields are Eve's auto_fields stored in the document.
func metaFields(res *Resource) []string {
	meta := []string{"_id", "_created", "_updated", "_etag"}
	if res.SoftDelete {
		meta = append(meta, "_deleted")
	}
	return meta
}

// schemaProjection is Eve's _set_resource_projection (flaskapp.py): without
// allow_unknown and without an inclusion, the datasource projection is every
// schema field not excluded, so fields stored outside the schema are not
// returned. The excluded keys stay, with 0, and the meta fields are added.
func schemaProjection(res *Resource) bson.M {
	p := maps.Clone(res.Datasource.Projection)
	if len(res.Schema) == 0 || res.AllowUnknown {
		return p
	}
	if p == nil {
		p = bson.M{}
	}
	if !slices.ContainsFunc(slices.Collect(maps.Values(p)), isOne) {
		for f := range res.Schema {
			if _, excluded := p[f]; !excluded {
				p[f] = 1
			}
		}
	}
	for _, f := range []string{"_id", "_created", "_updated", "_etag"} {
		p[f] = 1
	}
	if res.SoftDelete {
		p["_deleted"] = 1
	}
	return p
}

// projection is the projection of _datasource_ex in Eve 2.3.1 (io/base.py).
// The datasource projection always carries the meta fields (auto_fields).
// A client projection can only narrow the datasource projection: fields the
// datasource does not project are ignored ("avoid sniffing of private
// fields") and exclusions are dropped after being applied. Without a
// datasource projection (allow_unknown) the client one applies as sent,
// minus its falsy values: an exclusion is ignored.
func (s *Service) projection(client any) (any, error) {
	return s.res.Adapter.CombineProjection(s.res, client)
}

// combineProjection is projection's body, extracted (Fase 0, rest.Adapter's
// CombineProjection) so mongoAdapter can expose the same, unchanged logic.
func combineProjection(res *Resource, client bson.M) bson.M {
	meta := metaFields(res)
	ds := res.Datasource.Projection
	if len(ds) > 0 {
		fields := maps.Clone(ds)
		for _, f := range meta {
			fields[f] = 1
		}
		if len(client) > 0 {
			if slices.ContainsFunc(slices.Collect(maps.Values(client)), isOne) {
				// inclusive projection: all values are 0 unless specified or auto
				for f := range fields {
					if !slices.Contains(meta, f) {
						fields[f] = 0
					}
				}
			}
			for f, v := range client {
				base, _, _ := strings.Cut(f, ".")
				if _, projected := fields[base]; projected && !slices.Contains(meta, base) {
					fields[f] = v
				}
			}
		}
		maps.DeleteFunc(fields, func(_ string, v any) bool { return !truthyValue(v) })
		return fields
	}

	// allow_unknown: the client projection as sent, without the falsy
	// values, so an exclusion is ignored (and a mixed one does not reach
	// Mongo); the meta fields come as stored (V-17)
	fields := maps.Clone(client)
	maps.DeleteFunc(fields, func(_ string, v any) bool { return !truthyValue(v) })
	if len(fields) == 0 {
		return nil
	}
	for _, f := range meta {
		fields[f] = 1
	}
	return fields
}

// isOne is Python's `1 in projection.values()`: 1, 1.0 and True.
func isOne(v any) bool {
	switch n := v.(type) {
	case float64:
		return n == 1
	case int:
		return n == 1
	case int32:
		return n == 1
	case int64:
		return n == 1
	case bool:
		return n
	}
	return false
}

func truthyValue(v any) bool {
	switch n := v.(type) {
	case float64:
		return n != 0
	case int:
		return n != 0
	case int32:
		return n != 0
	case int64:
		return n != 0
	case bool:
		return n
	}
	return false
}

// writeError classifies a write's backend error into Eve's HTTP status: a
// unique index violation becomes duplicateStatus (409 on insert, 400 on
// update/replace, pymongo's DuplicateKeyError contract), classified by the
// resource's own Adapter so a Postgres resource's 23505 is recognized too,
// not just Mongo's. Anything else falls through to timeoutError.
func (s *Service) writeError(err error, duplicateStatus int) error {
	if s.res.Adapter.IsDuplicateKey(err) {
		return Abort(duplicateStatus, "")
	}
	return s.timeoutError(err)
}

// timeoutError turns a write's backend error into 503 when the resource's
// own Adapter opts in (ClassifiesWriteTimeouts) and recognizes it as a
// timeout -- section 12 of the Postgres portability plan: a Postgres
// resource's 57014 (statement timeout), 40P01 (deadlock) or 40001
// (serialization failure) are classified this way (postgresAdapter.
// IsTimeout), none of them a 500. mongoAdapter's ClassifiesWriteTimeouts
// is false (see its own doc comment, rest/adapter.go): this must never run
// for Mongo, or it would erase the TransientTransactionError label
// platform/mongodb's Transactor relies on to retry a write transparently.
// writeError calls this for its own non-duplicate-key case; Delete calls
// it directly, since a delete can never produce a duplicate-key error.
//
// A bare context.DeadlineExceeded/Canceled is excluded even when the
// Adapter's own IsTimeout would also say true for it (postgresAdapter's
// IsTimeout falls back to exactly this for an error it doesn't
// recognize by its own pgconn-specific signal): it means the caller's
// own context ended, not that the backend reported a failure.
// Resource.DryRun's write cut (DryRunTimeout) deliberately produces
// exactly this from inside a write's own hooks or adapter call, and
// rest/dryrun_test.go's TestDryRunTimeout already pins that case to a
// plain 500 ("as any write error") -- a contract this plan never asked
// to change. A genuine backend timeout/conflict (a real pgconn.PgError
// 57014/40P01/40001) is unaffected, since it neither wraps nor equals a
// context error.
func (s *Service) timeoutError(err error) error {
	if err == nil || !s.res.Adapter.ClassifiesWriteTimeouts() {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	if s.res.Adapter.IsTimeout(err) {
		slog.Warn("write timed out", "error", err)
		return Abort(http.StatusServiceUnavailable, "")
	}
	return err
}

// clientQueryErrors are Mongo error codes caused by the client query:
// BadValue, FailedToParse and invalid regular expressions.
var clientQueryErrors = []int{2, 9, 51091, 51108}

// queryError turns a backend error caused by the client query into 400
// (Eve answered 500, V-16), classified by the resource's own Adapter.
func (s *Service) queryError(err error) error {
	if s.res.Adapter.IsTimeout(err) {
		slog.Warn("query timed out", "error", err)
		return Abort(http.StatusServiceUnavailable, "")
	}
	if s.res.Adapter.IsClientError(err) {
		return Abort(400, "")
	}
	return err
}

// deepMerge merges src into dst, merging nested maps.
func deepMerge(dst, src Doc) Doc {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := asMap(dst[k]); ok {
				dst[k] = deepMerge(maps.Clone(dm), sm)
				continue
			}
		}
		dst[k] = v
	}
	return dst
}

func asMap(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case map[string]any:
		return t, true
	case bson.M:
		return t, true
	case bson.D:
		m := make(map[string]any, len(t))
		for _, e := range t {
			m[e.Key] = e.Value
		}
		return m, true
	}
	return nil, false
}

// runPost runs post-write hooks; errors are logged because the write is
// already committed. In dry run nothing was committed and they do not run:
// they reach Elasticsearch and other systems directly.
func runPost[H any](ctx context.Context, dryRun bool, event string, hooks []H, call func(H) error) {
	if dryRun {
		if len(hooks) > 0 {
			slog.DebugContext(ctx, "dry run: post-write hooks skipped", "event", event, "hooks", len(hooks))
		}
		return
	}
	for _, h := range hooks {
		if err := call(h); err != nil {
			slog.ErrorContext(ctx, "post-write hook failed", "event", event, "error", err)
		}
	}
}
