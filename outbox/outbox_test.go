package outbox_test

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/Gui-Rondon/hyrule/outbox"
	"github.com/Gui-Rondon/hyrule/platform/mongodb"
	"github.com/Gui-Rondon/hyrule/testkit"
)

func transactor(t *testing.T, db *mongo.Database) mongodb.Transactor {
	t.Helper()
	_, ok, err := mongodb.TransactorFor(context.Background(), db.Client())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Skip("Mongo is not a replica set")
	}
	return mongodb.Transactor{Client: db.Client()}
}

func TestAddRequiresTransaction(t *testing.T) {
	box := outbox.New(testkit.Mongo(t))
	err := box.Add(context.Background(), outbox.Event{Topic: "t"})
	if !errors.Is(err, outbox.ErrNoTransaction) {
		t.Fatalf("err = %v", err)
	}
	if n, _ := box.Collection().CountDocuments(context.Background(), bson.M{}); n != 0 {
		t.Errorf("stored %d events outside a transaction", n)
	}
}

// The events are stored with the write or not at all.
func TestAddFollowsTheTransaction(t *testing.T) {
	db := testkit.Mongo(t)
	tx := transactor(t, db)
	box := outbox.New(db)
	ctx := context.Background()
	videos := db.Collection("videos")

	write := func(fail bool) error {
		return tx.WithTransaction(ctx, func(ctx context.Context) error {
			if _, err := videos.InsertOne(ctx, bson.M{"title": "x"}); err != nil {
				return err
			}
			if err := box.Add(ctx, outbox.Event{Topic: "videos-to-publish", Type: "video.publish_requested",
				Key: "v1", Body: map[string]any{"_id": "v1"}, Attributes: map[string]string{"origin": "edit"}}); err != nil {
				return err
			}
			if fail {
				return errors.New("write failed after the event")
			}
			return nil
		})
	}

	if err := write(true); err == nil {
		t.Fatal("expected error")
	}
	if n, _ := box.Collection().CountDocuments(ctx, bson.M{}); n != 0 {
		t.Fatalf("event kept after rollback: %d", n)
	}

	if err := write(false); err != nil {
		t.Fatal(err)
	}
	var got bson.M
	if err := box.Collection().FindOne(ctx, bson.M{}).Decode(&got); err != nil {
		t.Fatal(err)
	}
	data, _ := got["data"].(bson.Binary)
	if got["topic"] != "videos-to-publish" || got["type"] != "video.publish_requested" || got["key"] != "v1" ||
		string(data.Data) != `{"_id":"v1"}` || got["published_at"] != nil || got["occurred_at"] == nil {
		t.Errorf("event = %v", got)
	}
}

// InTransaction is not hardcoded to mongo.SessionFromContext: a caller can
// swap it for its own check (e.g. a non-Mongo backend's own transaction
// marker).
func TestInTransactionIsOverridable(t *testing.T) {
	box := outbox.New(testkit.Mongo(t))
	box.InTransaction = func(context.Context) bool { return true }
	if err := box.Add(context.Background(), outbox.Event{Topic: "t"}); err != nil {
		t.Fatalf("add with overridden InTransaction = %v", err)
	}
	box.InTransaction = func(context.Context) bool { return false }
	if err := box.Add(context.Background(), outbox.Event{Topic: "t"}); !errors.Is(err, outbox.ErrNoTransaction) {
		t.Fatalf("add with overridden InTransaction (false) = %v", err)
	}
}

func TestAddEncodesBody(t *testing.T) {
	db := testkit.Mongo(t)
	box := outbox.New(db)
	box.AllowNoTransaction = true
	ctx := context.Background()
	err := box.Add(ctx,
		outbox.Event{Topic: "raw", Body: []byte("bytes")},
		outbox.Event{Topic: "str", Body: "texto"},
		outbox.Event{Topic: "json", Body: struct {
			ID string `json:"id"`
		}{"a"}},
		outbox.Event{Topic: "empty"},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"raw": "bytes", "str": "texto", "json": `{"id":"a"}`, "empty": ""}
	cur, _ := box.Collection().Find(ctx, bson.M{})
	var docs []struct {
		Topic string `bson:"topic"`
		Data  []byte `bson:"data"`
	}
	cur.All(ctx, &docs)
	for _, d := range docs {
		if string(d.Data) != want[d.Topic] {
			t.Errorf("%s = %q", d.Topic, d.Data)
		}
	}
	if err := box.Add(ctx, outbox.Event{}); err == nil {
		t.Error("event without topic must fail")
	}
	if err := box.Add(ctx, outbox.Event{Topic: "x", Body: make(chan int)}); err == nil {
		t.Error("unencodable body must fail")
	}
}
