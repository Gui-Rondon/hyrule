// Package lease elects one active replica through a document in Mongo, as
// the outbox relay does: the holder renews the lease before it expires, and
// another replica takes it only after it expires or is released.
package lease

import (
	"context"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Lease is the document Name in Coll.
type Lease struct {
	Coll *mongo.Collection
	Name string
	// Owner identifies the replica (default: the hostname).
	Owner string
	// TTL is how long the lease lasts without a renewal (default 30 s).
	TTL time.Duration
}

func (l *Lease) defaults() {
	if l.Owner == "" {
		l.Owner, _ = os.Hostname()
	}
	if l.TTL <= 0 {
		l.TTL = 30 * time.Second
	}
}

// Acquire takes the lease, or renews it when this replica holds it. It
// reports whether this replica is the holder.
func (l *Lease) Acquire(ctx context.Context) (bool, error) {
	l.defaults()
	now := time.Now().UTC()
	_, err := l.Coll.UpdateOne(ctx,
		bson.M{"_id": l.Name, "$or": bson.A{
			bson.M{"until": bson.M{"$lt": now}},
			bson.M{"owner": l.Owner},
		}},
		bson.M{"$set": bson.M{"owner": l.Owner, "until": now.Add(l.TTL)}},
		options.UpdateOne().SetUpsert(true))
	// another replica holds it: the filter misses and the upsert collides
	if mongo.IsDuplicateKeyError(err) {
		return false, nil
	}
	return err == nil, err
}

// Release gives the lease up, so that another replica takes over without
// waiting for it to expire.
func (l *Lease) Release() {
	l.defaults()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l.Coll.UpdateOne(ctx, bson.M{"_id": l.Name, "owner": l.Owner},
		bson.M{"$set": bson.M{"until": time.Unix(0, 0)}})
}
