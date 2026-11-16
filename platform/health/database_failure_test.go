package health_test

import (
	"context"
	"os"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/Gui-Rondon/hyrule/platform/health"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDisconnectedMongoIsUnhealthy(t *testing.T) {
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		t.Skip("MONGO_TEST_URI not set")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	if err := client.Ping(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	db := client.Database("health_failure")
	if err := client.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	ok, msg := health.Mongo(db).Check(context.Background())
	if ok || msg != "Database NOK" {
		t.Fatalf("health = %v, %q", ok, msg)
	}
}

func TestClosedPostgresPoolIsUnhealthy(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://localhost/postgres?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	ok, msg := health.Postgres(pool).Check(context.Background())
	if ok || msg != "Database NOK" {
		t.Fatalf("health = %v, %q", ok, msg)
	}
}
