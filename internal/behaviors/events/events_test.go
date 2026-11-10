package events_test

import (
	"context"
	"net/http"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Gui-Rondon/hyrule/internal/behaviors/events"
	"github.com/Gui-Rondon/hyrule/internal/evecompat"
	"github.com/Gui-Rondon/hyrule/internal/outbox"
	"github.com/Gui-Rondon/hyrule/internal/platform/mongodb"
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
	res := &evecompat.Resource{
		Name:        "items",
		Store:       mongodb.NewStore(db.Collection("items")),
		Schema:      evecompat.Schema{"name": {Type: evecompat.TypeString, MaxLength: 3}},
		ItemMethods: []string{http.MethodGet, http.MethodPatch},
	}
	res.Hooks.Use(events.Hooks(box))
	svc := evecompat.NewService(res, tx)
	ctx := context.Background()
	count := func() int64 {
		n, _ := box.Collection().CountDocuments(ctx, bson.M{})
		return n
	}

	// a write without events records nothing
	docs, err := svc.Insert(ctx, []evecompat.Doc{{"name": "a"}}, evecompat.WriteOptions{})
	if err != nil || count() != 0 {
		t.Fatalf("insert: %v, events %d", err, count())
	}
	lookup := bson.M{"_id": docs[0]["_id"]}

	ev := outbox.Event{Topic: "t", Key: "k", Body: "1"}
	if _, err := svc.Patch(events.With(ctx, ev), lookup, evecompat.Doc{"name": "b"}, evecompat.WriteOptions{SkipConcurrency: true}); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Errorf("events after patch = %d", count())
	}

	// an invalid write fails before the hooks; a failing hook rolls back
	if _, err := svc.Patch(events.With(ctx, ev), lookup, evecompat.Doc{"name": "longo"}, evecompat.WriteOptions{SkipConcurrency: true}); err == nil {
		t.Error("invalid patch accepted")
	}
	if _, err := svc.Patch(events.With(ctx, outbox.Event{}), lookup, evecompat.Doc{"name": "c"}, evecompat.WriteOptions{SkipConcurrency: true}); err == nil {
		t.Error("event without topic accepted")
	}
	doc, _ := svc.Find(ctx, lookup)
	if count() != 1 || doc["name"] != "b" {
		t.Errorf("after failures: events %d, name %v", count(), doc["name"])
	}

	two := events.With(events.With(ctx, ev), outbox.Event{Topic: "u"})
	if _, err := svc.Replace(two, lookup, evecompat.Doc{"name": "d"}, evecompat.WriteOptions{SkipConcurrency: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(events.With(ctx, ev), lookup, evecompat.WriteOptions{SkipConcurrency: true}); err != nil {
		t.Fatal(err)
	}
	if count() != 4 {
		t.Errorf("events after replace and delete = %d", count())
	}
}
