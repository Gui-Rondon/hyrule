package evecompat

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Store is the persistence seam a Resource reads and writes through. It
// decouples the concrete storage client from evecompat — not the query
// language: filters, sorts and updates stay bson.M/bson.D, and the error
// values a Store returns (not found, duplicate key, timeout) are still
// Mongo-shaped (mongo.ErrNoDocuments, mongo.IsDuplicateKeyError...), because
// the callers in this package check for those directly. The only
// implementation today wraps a *mongo.Collection (internal/platform/mongodb.
// Store); a non-Mongo backend needs either to return Mongo-compatible
// errors for the checks above, or this interface needs a second pass to
// carry its own error values instead.
//
// Methods take plain bson/stdlib types, not evecompat-owned option structs:
// that keeps platform/mongodb (or any other Store implementation) from
// having to import evecompat for its method signatures to match, which
// would invert the services → behaviors → evecompat → platform layering.
//
// https://gui-rondon.github.io/hyrule/docs/reference/architecture
type Store interface {
	// Find lists documents; limit <= 0 means no skip/limit is applied, and
	// a nil sort or projection leaves Mongo's defaults.
	Find(ctx context.Context, filter bson.M, skip, limit int64, sort bson.D, projection bson.M) ([]Doc, error)
	CountDocuments(ctx context.Context, filter bson.M) (int64, error)
	// FindOne reads one document; a nil projection leaves Mongo's default.
	FindOne(ctx context.Context, filter bson.M, projection bson.M) (Doc, error)
	InsertMany(ctx context.Context, docs []any) error
	// UpdateOne applies a $set-shaped update document and returns the
	// number of matched documents.
	UpdateOne(ctx context.Context, filter, update bson.M) (matched int64, err error)
	ReplaceOne(ctx context.Context, filter bson.M, replacement any) (matched int64, err error)
	DeleteOne(ctx context.Context, filter bson.M) (deleted int64, err error)
}
