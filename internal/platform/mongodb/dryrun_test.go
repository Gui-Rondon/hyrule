package mongodb

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/Gui-Rondon/hyrule/internal/platform/config"
)

// guarded connects with a WriteGuard to a fresh database of a replica set.
func guarded(t *testing.T) (*WriteGuard, *mongo.Database, Transactor) {
	t.Helper()
	t.Setenv("MONGO_CONNECTION_STRING", testURI(t))
	ctx := context.Background()
	guard := &WriteGuard{}
	client, db, err := Connect(ctx, config.Config{}, "dryrun_test", options.Client().SetMonitor(guard.Monitor()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Disconnect(context.Background()) })
	if _, replicaSet, err := TransactorFor(ctx, client); err != nil || !replicaSet {
		t.Skipf("dry run needs a replica set (err %v)", err)
	}
	db = client.Database(db.Name() + "_" + bson.NewObjectID().Hex()[18:])
	t.Cleanup(func() { db.Drop(context.Background()) })
	return guard, db, Transactor{Client: client, Rollback: true}
}

func TestRollbackTransactor(t *testing.T) {
	guard, db, tx := guarded(t)
	ctx := context.Background()
	coll := db.Collection("c")
	// the collection and its unique index exist before the dry run, as in
	// production
	if _, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.M{"k": 1}, Options: options.Index().SetUnique(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := coll.InsertOne(ctx, bson.M{"k": "taken", "n": 1}); err != nil {
		t.Fatal(err)
	}
	before := guard.Violations()

	var inside int64
	err := tx.WithTransaction(ctx, func(ctx context.Context) error {
		if _, err := coll.InsertOne(ctx, bson.M{"k": "new"}); err != nil {
			return err
		}
		if _, err := coll.UpdateOne(ctx, bson.M{"k": "taken"}, bson.M{"$set": bson.M{"n": 2}}); err != nil {
			return err
		}
		// the transaction reads its own writes
		inside, _ = coll.CountDocuments(ctx, bson.M{})
		return nil
	})
	if err != nil {
		t.Fatalf("a successful dry run reports success: %v", err)
	}
	if inside != 2 {
		t.Errorf("count inside the transaction = %d", inside)
	}
	var doc bson.M
	coll.FindOne(ctx, bson.M{"k": "taken"}).Decode(&doc)
	if n, _ := coll.CountDocuments(ctx, bson.M{}); n != 1 || doc["n"] != int32(1) {
		t.Errorf("stored: count %d, doc %v", n, doc)
	}

	// the errors of fn come back, as in a real write: a unique index
	// violation is still a duplicate key
	err = tx.WithTransaction(ctx, func(ctx context.Context) error {
		_, err := coll.InsertOne(ctx, bson.M{"k": "taken"})
		return err
	})
	if !mongo.IsDuplicateKeyError(err) {
		t.Errorf("duplicate key in dry run = %v", err)
	}
	boom := errors.New("boom")
	if err := tx.WithTransaction(ctx, func(context.Context) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("fn error = %v", err)
	}
	if guard.Violations() != before {
		t.Errorf("writes in a transaction counted as violations: %d", guard.Violations()-before)
	}
}

func TestWriteGuard(t *testing.T) {
	guard, db, _ := guarded(t)
	ctx := context.Background()
	coll := db.Collection("c")
	before := guard.Violations()
	coll.InsertOne(ctx, bson.M{"x": 1})
	coll.UpdateOne(ctx, bson.M{"x": 1}, bson.M{"$set": bson.M{"y": 1}})
	coll.FindOneAndUpdate(ctx, bson.M{"x": 1}, bson.M{"$set": bson.M{"y": 2}})
	coll.Aggregate(ctx, mongo.Pipeline{{{Key: "$match", Value: bson.M{}}}, {{Key: "$out", Value: "copy"}}})
	if got := guard.Violations() - before; got != 4 {
		t.Errorf("violations = %d, want 4", got)
	}
	// reads are not writes
	before = guard.Violations()
	coll.FindOne(ctx, bson.M{})
	coll.CountDocuments(ctx, bson.M{})
	coll.Aggregate(ctx, mongo.Pipeline{{{Key: "$match", Value: bson.M{}}}})
	if got := guard.Violations() - before; got != 0 {
		t.Errorf("reads counted: %d", got)
	}
}
