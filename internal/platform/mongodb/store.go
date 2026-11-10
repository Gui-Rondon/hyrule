package mongodb

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Store wraps a *mongo.Collection and structurally satisfies
// evecompat.Store, without this package importing evecompat (platform sits
// below evecompat, docs/architecture.md).
type Store struct {
	Collection *mongo.Collection
}

// NewStore wraps coll.
func NewStore(coll *mongo.Collection) Store { return Store{Collection: coll} }

func (s Store) Find(ctx context.Context, filter bson.M, skip, limit int64, sort bson.D, projection bson.M) ([]map[string]any, error) {
	opts := options.Find()
	if limit > 0 {
		opts.SetSkip(skip).SetLimit(limit)
	}
	if len(sort) > 0 {
		opts.SetSort(sort)
	}
	if projection != nil {
		opts.SetProjection(projection)
	}
	cur, err := s.Collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	docs := []map[string]any{}
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

func (s Store) CountDocuments(ctx context.Context, filter bson.M) (int64, error) {
	return s.Collection.CountDocuments(ctx, filter)
}

func (s Store) FindOne(ctx context.Context, filter bson.M, projection bson.M) (map[string]any, error) {
	opts := options.FindOne()
	if projection != nil {
		opts.SetProjection(projection)
	}
	var doc map[string]any
	if err := s.Collection.FindOne(ctx, filter, opts).Decode(&doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func (s Store) InsertMany(ctx context.Context, docs []any) error {
	_, err := s.Collection.InsertMany(ctx, docs)
	return err
}

func (s Store) UpdateOne(ctx context.Context, filter, update bson.M) (int64, error) {
	res, err := s.Collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return 0, err
	}
	return res.MatchedCount, nil
}

func (s Store) ReplaceOne(ctx context.Context, filter bson.M, replacement any) (int64, error) {
	res, err := s.Collection.ReplaceOne(ctx, filter, replacement)
	if err != nil {
		return 0, err
	}
	return res.MatchedCount, nil
}

func (s Store) DeleteOne(ctx context.Context, filter bson.M) (int64, error) {
	res, err := s.Collection.DeleteOne(ctx, filter)
	if err != nil {
		return 0, err
	}
	return res.DeletedCount, nil
}
