package rest

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// mongoAdapter implements ErrorClassifier, IDCodec and QueryAdapter for
// the only Store implementation today (platform/mongodb.Store). It is
// stateless: every method here is exactly the check/generation/query
// logic that was inlined in rest/service.go, rest/query.go and
// rest/http.go before this extraction (Fase 0).
type mongoAdapter struct{}

// defaultAdapter is the one instance Service and the HTTP handler use.
// mongoAdapter carries no state, so a shared value is safe.
var defaultAdapter mongoAdapter

func (mongoAdapter) IsNotFound(err error) bool {
	return errors.Is(err, mongo.ErrNoDocuments)
}

func (mongoAdapter) IsDuplicateKey(err error) bool {
	return mongo.IsDuplicateKeyError(err)
}

// IsClientError reports whether err carries one of clientQueryErrors
// (BadValue, FailedToParse, an invalid regular expression) — Mongo codes
// caused by the client's own query, not the server.
func (mongoAdapter) IsClientError(err error) bool {
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

func (mongoAdapter) IsTimeout(err error) bool {
	return mongo.IsTimeout(err) || errors.Is(err, context.DeadlineExceeded)
}

func (mongoAdapter) NewID() any { return bson.NewObjectID() }

func (mongoAdapter) IDString(id any) string { return idString(id) }

// LookupByID parses value as a 24 hex-digit ObjectID, Eve's convention for
// the item URL segment (objectIDRe, rest/query.go).
func (mongoAdapter) LookupByID(value string) (any, bool) {
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
func (mongoAdapter) LookupID(id any) any { return id }

// ParseWhere decodes raw with res's own QueryDenylist/QueryObjectIDAsString
// -- the same call parseListQuery (rest/query.go) already makes for the
// resource's own HTTP list endpoint.
func (mongoAdapter) ParseWhere(raw string, res *Resource) (any, error) {
	return parseWhere(raw, res.QueryDenylist, res.QueryObjectIDAsString)
}

func (mongoAdapter) ParseSort(raw string) (any, error) {
	return ParseSort(raw)
}

func (mongoAdapter) ParseProjection(raw string) (any, error) {
	return parseProjection(raw)
}

// CombineFilter is combineFilter (rest/service.go), typed for the Adapter
// interface: clientFilter is asserted to bson.M, nil for any other (or
// absent) value -- the same as an empty client filter today.
func (mongoAdapter) CombineFilter(res *Resource, clientFilter any, includeDeleted bool) (any, error) {
	lookup, _ := clientFilter.(bson.M)
	return combineFilter(res, lookup, includeDeleted), nil
}

func (mongoAdapter) CombineSort(res *Resource, clientSort any) any {
	sort, _ := clientSort.(bson.D)
	return combineSort(res, sort)
}

func (mongoAdapter) CombineProjection(res *Resource, clientProjection any) (any, error) {
	client, _ := clientProjection.(bson.M)
	return combineProjection(res, client), nil
}

func (mongoAdapter) WriteLookup(original Doc) (any, error) {
	return writeLookup(original), nil
}
