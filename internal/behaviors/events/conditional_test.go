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

func TestConditionalHooksPublishOnFieldChange(t *testing.T) {
	db := testkit.Mongo(t)
	tx, replicaSet, err := mongodb.TransactorFor(context.Background(), db.Client())
	if err != nil {
		t.Fatal(err)
	}
	if !replicaSet {
		t.Skip("Mongo is not a replica set")
	}
	box := outbox.New(db)
	cond := outbox.OnFieldChange("status", []string{"published"}, func(doc map[string]any) outbox.Event {
		return outbox.Event{Topic: "posts-to-publish", Type: "post.published", Key: "k"}
	})
	res := &evecompat.Resource{
		Name:        "items",
		Store:       mongodb.NewStore(db.Collection("items")),
		Schema:      evecompat.Schema{"status": {Type: evecompat.TypeString}},
		ItemMethods: []string{http.MethodGet, http.MethodPatch, http.MethodPut},
	}
	res.Hooks.Use(events.ConditionalHooks(box, cond))
	svc := evecompat.NewService(res, tx)
	ctx := context.Background()
	count := func() int64 {
		n, _ := box.Collection().CountDocuments(ctx, bson.M{})
		return n
	}

	// an insert that doesn't land on "published" emits nothing
	draft, err := svc.Insert(ctx, []evecompat.Doc{{"status": "draft"}}, evecompat.WriteOptions{})
	if err != nil || count() != 0 {
		t.Fatalf("insert draft: %v, events %d", err, count())
	}
	lookup := bson.M{"_id": draft[0]["_id"]}

	// an insert that lands directly on "published" emits
	if _, err := svc.Insert(ctx, []evecompat.Doc{{"status": "published"}}, evecompat.WriteOptions{}); err != nil || count() != 1 {
		t.Fatalf("insert published: %v, events %d", err, count())
	}

	// a patch that doesn't touch status emits nothing
	if _, err := svc.Patch(ctx, lookup, evecompat.Doc{}, evecompat.WriteOptions{SkipConcurrency: true}); err != nil || count() != 1 {
		t.Fatalf("patch without status: %v, events %d", err, count())
	}

	// a patch that changes status to "published" emits
	if _, err := svc.Patch(ctx, lookup, evecompat.Doc{"status": "published"}, evecompat.WriteOptions{SkipConcurrency: true}); err != nil || count() != 2 {
		t.Fatalf("patch to published: %v, events %d", err, count())
	}

	// a patch that resends the same value (no transition) emits nothing
	if _, err := svc.Patch(ctx, lookup, evecompat.Doc{"status": "published"}, evecompat.WriteOptions{SkipConcurrency: true}); err != nil || count() != 2 {
		t.Fatalf("patch same value: %v, events %d", err, count())
	}

	// back to draft (not in the target list: emits nothing), then a replace
	// that lands on "published" again emits — this is a real transition,
	// unlike resending the same value above
	if _, err := svc.Patch(ctx, lookup, evecompat.Doc{"status": "draft"}, evecompat.WriteOptions{SkipConcurrency: true}); err != nil || count() != 2 {
		t.Fatalf("patch to draft: %v, events %d", err, count())
	}
	if _, err := svc.Replace(ctx, lookup, evecompat.Doc{"status": "published"}, evecompat.WriteOptions{SkipConcurrency: true}); err != nil || count() != 3 {
		t.Fatalf("replace to published: %v, events %d", err, count())
	}
}
