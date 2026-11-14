package rest

import (
	"context"
	"errors"
	"maps"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// mongoAdapter implements Adapter in full for the only Store
// implementation today (platform/mongodb.Store). ParseWhere/ParseSort/
// ParseProjection/CombineFilter/CombineSort/CombineProjection/
// IsNotFound/IsDuplicateKey/IsClientError/IsTimeout/NewID/IDString/
// LookupByID/LookupID/WriteLookup are exactly the check/generation/
// query logic that was inlined in rest/service.go, rest/query.go and
// rest/http.go before this extraction (Fase 0) -- those methods never
// touch store/res, consistent with their interface signatures already
// taking res explicitly. The CRUD/MergePatch/EqualsFilter methods
// below (Fase 4a) are new: they need store (and, for SoftDelete, res)
// to actually reach Store, which is why this is no longer stateless.
type mongoAdapter struct {
	store Store
	res   *Resource
}

func (*mongoAdapter) IsNotFound(err error) bool {
	return errors.Is(err, mongo.ErrNoDocuments)
}

func (*mongoAdapter) IsDuplicateKey(err error) bool {
	return mongo.IsDuplicateKeyError(err)
}

// IsClientError reports whether err carries one of clientQueryErrors
// (BadValue, FailedToParse, an invalid regular expression) — Mongo codes
// caused by the client's own query, not the server.
func (*mongoAdapter) IsClientError(err error) bool {
	var srvErr mongo.ServerError
	if !errors.As(err, &srvErr) {
		return false
	}
	for _, code := range clientQueryErrors {
		if srvErr.HasErrorCode(code) {
			return true
		}
	}
	return false
}

func (*mongoAdapter) IsTimeout(err error) bool {
	return mongo.IsTimeout(err) || errors.Is(err, context.DeadlineExceeded)
}

func (*mongoAdapter) NewID() any { return bson.NewObjectID() }

func (*mongoAdapter) IDString(id any) string { return idString(id) }

// LookupByID parses value as a 24 hex-digit ObjectID, Eve's convention for
// the item URL segment (objectIDRe, rest/query.go).
func (*mongoAdapter) LookupByID(value string) (any, bool) {
	if !objectIDRe.MatchString(value) {
		return nil, false
	}
	oid, err := bson.ObjectIDFromHex(value)
	if err != nil {
		return nil, false
	}
	return oid, true
}

// LookupID is the identity function for Mongo: a Doc's "_id" is already
// the bson.ObjectID a filter needs, nothing to adapt.
func (*mongoAdapter) LookupID(id any) any { return id }

// ParseWhere decodes raw with res's own QueryDenylist/QueryObjectIDAsString
// -- the same call parseListQuery (rest/query.go) already makes for the
// resource's own HTTP list endpoint.
func (*mongoAdapter) ParseWhere(raw string, res *Resource) (any, error) {
	return parseWhere(raw, res.QueryDenylist, res.QueryObjectIDAsString)
}

func (*mongoAdapter) ParseSort(raw string) (any, error) {
	return ParseSort(raw)
}

func (*mongoAdapter) ParseProjection(raw string) (any, error) {
	return parseProjection(raw)
}

// as recovers the Mongo-native type Adapter's any-typed parameters
// carry. A nil/wrong-type value (an empty filter, no sort, no
// projection) comes through as T's zero value -- the Mongo driver's
// own default, exactly as before this extraction. Every caller in
// this package only ever passes a value this same adapter's own
// CombineFilter/CombineSort/CombineProjection/WriteLookup produced, so
// there is no wrong-type value to recover from in practice.
func as[T any](v any) T { t, _ := v.(T); return t }

// asFilter/asSort/asProjection name as's instantiations at each call
// site's actual meaning, since bson.M alone (filter and projection
// share it) wouldn't say which.
func asFilter(v any) bson.M     { return as[bson.M](v) }
func asSort(v any) bson.D       { return as[bson.D](v) }
func asProjection(v any) bson.M { return as[bson.M](v) }

// CombineFilter is combineFilter (rest/service.go), typed for the Adapter
// interface: clientFilter is asserted to bson.M, nil for any other (or
// absent) value -- the same as an empty client filter today.
func (*mongoAdapter) CombineFilter(res *Resource, clientFilter any, includeDeleted bool) (any, error) {
	return combineFilter(res, asFilter(clientFilter), includeDeleted), nil
}

func (*mongoAdapter) CombineSort(res *Resource, clientSort any) any {
	return combineSort(res, asSort(clientSort))
}

func (*mongoAdapter) CombineProjection(res *Resource, clientProjection any) (any, error) {
	return combineProjection(res, asProjection(clientProjection)), nil
}

func (*mongoAdapter) WriteLookup(original Doc) (any, error) {
	return writeLookup(original), nil
}

// IDFilter is {"_id": id}, the same shape CombineFilter's asFilter
// already asserts for any other Mongo filter.
func (*mongoAdapter) IDFilter(id any) (any, error) {
	return bson.M{"_id": id}, nil
}

// RetryPolicy is the zero RetryOptions: Mongo never retries a PATCH
// under If-Match: * today, and this adapter doesn't change that.
func (*mongoAdapter) RetryPolicy() RetryOptions {
	return RetryOptions{}
}

func (a *mongoAdapter) Find(ctx context.Context, filter, sort, projection any, skip, limit int64) ([]Doc, error) {
	return a.store.Find(ctx, asFilter(filter), skip, limit, asSort(sort), asProjection(projection))
}

func (a *mongoAdapter) CountDocuments(ctx context.Context, filter any) (int64, error) {
	return a.store.CountDocuments(ctx, asFilter(filter))
}

func (a *mongoAdapter) FindOne(ctx context.Context, lookup, projection any) (Doc, error) {
	return a.store.FindOne(ctx, asFilter(lookup), asProjection(projection))
}

// setEtag computes doc's content hash and sets it in place, returning
// the same value -- the one step InsertMany/UpdateOne/ReplaceOne all
// share: a fresh _etag, computed here (after the OnInsert/OnUpdate
// hooks already ran, immediately before the write), never from a
// value the caller computed earlier.
func setEtag(doc Doc) string {
	etag := newEtag(doc)
	doc["_etag"] = etag
	return etag
}

// InsertMany sets each doc's _etag (setEtag, per Adapter.InsertMany's
// contract) and forwards to store.
func (a *mongoAdapter) InsertMany(ctx context.Context, docs []Doc) error {
	toInsert := make([]any, len(docs))
	for i, doc := range docs {
		setEtag(doc)
		toInsert[i] = doc
	}
	return a.store.InsertMany(ctx, toInsert)
}

// UpdateOne sets updated's _etag (setEtag, per Adapter.UpdateOne's
// contract) and writes a $set of exactly: _updated/_etag always,
// _deleted when updated carries one (SoftDelete writes, Patch and
// soft Delete alike), and every key named by changes (Patch's applied
// changes, or none for a plain soft delete).
func (a *mongoAdapter) UpdateOne(ctx context.Context, lookup any, updated, changes Doc) (int64, error) {
	set := bson.M{"_updated": updated["_updated"], "_etag": setEtag(updated)}
	if a.res.SoftDelete {
		set["_deleted"] = updated["_deleted"]
	}
	for k := range changes {
		set[k] = updated[k]
	}
	return a.store.UpdateOne(ctx, asFilter(lookup), bson.M{"$set": set})
}

// ReplaceOne sets replacement's _etag (setEtag, per Adapter.ReplaceOne's
// contract) and forwards to store.
func (a *mongoAdapter) ReplaceOne(ctx context.Context, lookup any, replacement Doc) (int64, error) {
	setEtag(replacement)
	return a.store.ReplaceOne(ctx, asFilter(lookup), replacement)
}

func (a *mongoAdapter) DeleteOne(ctx context.Context, lookup any) (int64, error) {
	return a.store.DeleteOne(ctx, asFilter(lookup))
}

// MergePatch is Eve's MERGE_NESTED_DOCUMENTS deep merge -- a deliberate
// divergence from Postgres's RFC 7396 (seção 10), unchanged from what
// Service.Patch already did inline before this extraction.
func (*mongoAdapter) MergePatch(original, changes Doc) (Doc, error) {
	return deepMerge(maps.Clone(original), changes), nil
}

// EqualsFilter builds "field == value", optionally excluding one
// document by excludeID -- the same filter Schema's checkUnique
// already built inline before this extraction.
func (*mongoAdapter) EqualsFilter(field string, value any, excludeID any) (any, error) {
	filter := bson.M{field: value}
	if excludeID != nil {
		filter["_id"] = bson.M{"$ne": excludeID}
	}
	return filter, nil
}
