package restpostgres

import (
	"fmt"

	"github.com/google/uuid"
)

// NewID generates the id of a document being inserted.
func (a *postgresAdapter) NewID() any { return uuid.New() }

// IDString renders id the way it appears in a URL or a response body.
func (a *postgresAdapter) IDString(id any) string {
	u, ok := id.(uuid.UUID)
	if !ok {
		return ""
	}
	return u.String()
}

// LookupByID parses value, taken from a URL path segment, as an id; ok
// is false when value does not look like a UUID at all.
func (a *postgresAdapter) LookupByID(value string) (id any, ok bool) {
	u, err := uuid.Parse(value)
	if err != nil {
		return nil, false
	}
	return u, true
}

// LookupID adapts an id already stored in a Doc for use inside a
// filter -- a Doc's "_id" is already a uuid.UUID, the form the
// id column itself compares against, so this is the identity function
// (same role mongoAdapter.LookupID plays for Mongo's bson.ObjectID).
func (a *postgresAdapter) LookupID(id any) any { return id }

// IDFilter builds a filter selecting the single document whose
// physical id column equals id -- combinable with CombineFilter (see
// query.go's combinedFilter) the same way a client's ParseWhere result
// is, so an item route can reach the right document without
// hardcoding a Mongo-shaped bson.M{"_id": id} at the call site.
func (a *postgresAdapter) IDFilter(id any) (any, error) {
	u, ok := id.(uuid.UUID)
	if !ok {
		return nil, fmt.Errorf("restpostgres: IDFilter: id must be a uuid.UUID, got %T", id)
	}
	return u, nil
}
