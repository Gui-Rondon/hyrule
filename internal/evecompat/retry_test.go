package evecompat_test

import (
	"context"
	"net/http"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/Gui-Rondon/hyrule/internal/evecompat"
	"github.com/Gui-Rondon/hyrule/internal/platform/mongodb"
	"github.com/Gui-Rondon/hyrule/internal/testkit"
)

// retryOnce runs fn in a transaction that is aborted, then again in a real
// one, as the driver does after a transient error.
type retryOnce struct{ client *mongo.Client }

func (r retryOnce) WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	sess, err := r.client.StartSession()
	if err != nil {
		return err
	}
	defer sess.EndSession(ctx)
	if err := sess.StartTransaction(); err != nil {
		return err
	}
	_ = fn(mongo.NewSessionContext(ctx, sess))
	if err := sess.AbortTransaction(ctx); err != nil {
		return err
	}
	return mongodb.Transactor{Client: r.client}.WithTransaction(ctx, fn)
}

// A retried transaction runs the hooks again on the request document: a
// hook that pops a field finds it again. OnPrepare runs once, outside.
func TestHooksSurviveTransactionRetry(t *testing.T) {
	db := testkit.Mongo(t)
	if _, rs, err := mongodb.TransactorFor(context.Background(), db.Client()); err != nil || !rs {
		t.Skip("needs a replica set")
	}
	res := items()
	res.Schema["username"] = evecompat.Field{Type: evecompat.TypeString}
	res.Store = mongodb.NewStore(db.Collection("items"))
	stamp := func(doc evecompat.Doc) {
		who := "fallback"
		if v, ok := doc["username"].(string); ok {
			delete(doc, "username")
			who = v
		}
		doc["who"] = who
	}
	prepared := 0
	res.Hooks.Use(evecompat.Hooks{
		OnPrepare: []func(context.Context, evecompat.Doc, evecompat.Doc) error{
			func(context.Context, evecompat.Doc, evecompat.Doc) error { prepared++; return nil },
		},
		OnInsert: []func(context.Context, []evecompat.Doc) error{
			func(_ context.Context, docs []evecompat.Doc) error {
				for _, d := range docs {
					stamp(d)
				}
				return nil
			},
		},
		OnUpdate: []func(context.Context, evecompat.Doc, evecompat.Doc) error{
			func(_ context.Context, changes, _ evecompat.Doc) error { stamp(changes); return nil },
		},
		OnReplace: []func(context.Context, evecompat.Doc, evecompat.Doc) error{
			func(_ context.Context, doc, _ evecompat.Doc) error { stamp(doc); return nil },
		},
	})
	api := evecompat.New(evecompat.Options{Transactor: retryOnce{db.Client()}})
	api.Register(res)
	c := testkit.NewClient(t, api)

	created := c.Do("POST", "/items", map[string]any{"name": "a", "username": "ana"})
	if created.Status != http.StatusCreated {
		t.Fatalf("post = %d %s", created.Status, created.Raw)
	}
	id := created.String("_id")
	stored := func() bson.M {
		oid, _ := bson.ObjectIDFromHex(id)
		var doc bson.M
		if err := res.Store.(mongodb.Store).Collection.FindOne(context.Background(), bson.M{"_id": oid}).Decode(&doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	if doc := stored(); doc["who"] != "ana" || doc["username"] != nil {
		t.Errorf("insert after retry = %v", doc)
	}
	patched := c.Do("PATCH", "/items/"+id, map[string]any{"username": "bia"}, "If-Match", created.String("_etag"))
	if doc := stored(); patched.Status != http.StatusOK || doc["who"] != "bia" {
		t.Errorf("patch after retry = %d %v", patched.Status, doc)
	}
	replaced := c.Do("PUT", "/items/"+id, map[string]any{"name": "b", "username": "caio"}, "If-Match", patched.String("_etag"))
	if doc := stored(); replaced.Status != http.StatusOK || doc["who"] != "caio" {
		t.Errorf("put after retry = %d %v", replaced.Status, doc)
	}
	if prepared != 3 {
		t.Errorf("OnPrepare ran %d times, want 3", prepared)
	}
}
