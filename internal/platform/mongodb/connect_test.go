package mongodb

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/Gui-Rondon/hyrule/internal/platform/config"
	"github.com/Gui-Rondon/hyrule/internal/platform/mongodb/mongotest"
)

func testURI(t *testing.T) string {
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		t.Skip("MONGO_TEST_URI not set")
	}
	// a fresh CI server may still be initiating its replica set
	c, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Disconnect(context.Background())
	if err := mongotest.Prepare(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return uri
}

func TestConnectAndTransactions(t *testing.T) {
	uri := testURI(t)
	t.Setenv("MONGO_CONNECTION_STRING", uri)
	ctx := context.Background()
	client, db, err := Connect(ctx, config.Config{}, "connect_test")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(ctx)
	if db.Name() != "connect_test" {
		t.Errorf("db = %s", db.Name())
	}
	defer db.Drop(ctx)

	tx, replicaSet, err := TransactorFor(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	coll := db.Collection("c")
	boom := errors.New("boom")
	err = tx.WithTransaction(ctx, func(ctx context.Context) error {
		if _, err := coll.InsertOne(ctx, bson.M{"x": 1}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	n, _ := coll.CountDocuments(ctx, bson.M{})
	if replicaSet && n != 0 {
		t.Errorf("transaction not rolled back")
	}
	if !replicaSet && n != 1 {
		t.Errorf("standalone writes directly, n = %d", n)
	}
}

func TestConnectFailsOnUnreachableServer(t *testing.T) {
	t.Setenv("MONGO_CONNECTION_STRING", "mongodb://127.0.0.1:1/x?serverSelectionTimeoutMS=200")
	if _, _, err := Connect(context.Background(), config.Config{}, "x"); err == nil || !strings.Contains(err.Error(), "ping") {
		t.Errorf("err = %v", err)
	}
	t.Setenv("MONGO_CONNECTION_STRING", "")
	_, _, err := Connect(context.Background(), config.Config{"mongo": map[string]any{"username": "u", "password": "%%%"}}, "x")
	if err == nil {
		t.Error("invalid base64 password must fail")
	}
}
