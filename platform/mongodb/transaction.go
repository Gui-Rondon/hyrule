package mongodb

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Transactor runs functions in a Mongo transaction. The driver retries the
// function on transient errors, so it must only touch Mongo.
type Transactor struct {
	Client *mongo.Client
	// Rollback aborts the transaction even when fn succeeds, and reports
	// success: the writes run with their validations (unique indexes, write
	// conflicts, filters that match nothing) but nothing is stored (dry run).
	Rollback bool
}

// errRollback aborts a dry run transaction; it is not transient, so the
// driver does not retry.
var errRollback = errors.New("mongodb: dry run, transaction rolled back")

// WithTransaction runs fn in a transaction bound to ctx.
func (t Transactor) WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	sess, err := t.Client.StartSession()
	if err != nil {
		return err
	}
	defer sess.EndSession(ctx)
	_, err = sess.WithTransaction(ctx, func(ctx context.Context) (any, error) {
		if err := fn(ctx); err != nil {
			return nil, err
		}
		if t.Rollback {
			return nil, errRollback
		}
		return nil, nil
	})
	if errors.Is(err, errRollback) {
		return nil
	}
	return err
}

// direct runs fn without a transaction (standalone Mongo).
type direct struct{}

func (direct) WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// TransactorFor returns a Transactor when the server is a replica set (as
// production, rs0) and a pass-through otherwise.
func TransactorFor(ctx context.Context, client *mongo.Client) (interface {
	WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}, bool, error) {
	var hello struct {
		SetName string `bson:"setName"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return nil, false, err
	}
	if hello.SetName == "" {
		return direct{}, false, nil
	}
	return Transactor{Client: client}, true, nil
}
