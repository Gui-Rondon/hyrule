package events_test

import (
	"context"
	"net/http"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Gui-Rondon/hyrule/internal/behaviors/events"
	"github.com/Gui-Rondon/hyrule/internal/outbox"
	"github.com/Gui-Rondon/hyrule/internal/platform/mongodb"
	"github.com/Gui-Rondon/hyrule/internal/rest"
	"github.com/Gui-Rondon/hyrule/internal/testkit"
)

func TestEventsFollowTheWrite(t *testing.T) {
	db := testkit.Mongo(t)
	tx, replicaSet, err := mongodb.TransactorFor(context.Background(), db.Client())
	if err != nil {
		t.Fatal(err)
	}
	if !replicaSet {
		t.Skip("Mongo is not a replica set")
	}
	box := outbox.New(db)
	res := &rest.Resource{
		Name:        "items",
		Store:       mongodb.NewStore(db.Collection("items")),
		Schema:      rest.Schema{"name": {Type: rest.TypeString, MaxLength: 3}},
		ItemMethods: []string{http.MethodGet, http.MethodPatch},
	}
	res.Hooks.Use(events.Hooks(box))
	svc := rest.NewService(res, tx)
	ctx := context.Background()
	count := func() int64 {
		n, _ := box.Collection().CountDocuments(ctx, bson.M{})
		return n
	}

	// a write without events records nothing
	docs, err := svc.Insert(ctx, []rest.Doc{{"name": "a"}}, rest.WriteOptions{})
	if err != nil || count() != 0 {
		t.Fatalf("insert: %v, events %d", err, count())
	}
	lookup := bson.M{"_id": docs[0]["_id"]}

	ev := outbox.Event{Topic: "t", Key: "k", Body: "1"}
	if _, err := svc.Patch(events.With(ctx, ev), lookup, rest.Doc{"name": "b"}, rest.WriteOptions{SkipConcurrency: true}); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Errorf("events after patch = %d", count())
	}

	// an invalid write fails before the hooks; a failing hook rolls back
	if _, err := svc.Patch(events.With(ctx, ev), lookup, rest.Doc{"name": "longo"}, rest.WriteOptions{SkipConcurrency: true}); err == nil {
		t.Error("invalid patch accepted")
	}
	if _, err := svc.Patch(events.With(ctx, outbox.Event{}), lookup, rest.Doc{"name": "c"}, rest.WriteOptions{SkipConcurrency: true}); err == nil {
		t.Error("event without topic accepted")
	}
	doc, _ := svc.Find(ctx, lookup)
	if count() != 1 || doc["name"] != "b" {
		t.Errorf("after failures: events %d, name %v", count(), doc["name"])
	}

	two := events.With(events.With(ctx, ev), outbox.Event{Topic: "u"})
	if _, err := svc.Replace(two, lookup, rest.Doc{"name": "d"}, rest.WriteOptions{SkipConcurrency: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(events.With(ctx, ev), lookup, rest.WriteOptions{SkipConcurrency: true}); err != nil {
		t.Fatal(err)
	}
	if count() != 4 {
		t.Errorf("events after replace and delete = %d", count())
	}
}
