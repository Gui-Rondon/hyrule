package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/Gui-Rondon/hyrule/app"
	"github.com/Gui-Rondon/hyrule/behaviors/audit"
	"github.com/Gui-Rondon/hyrule/behaviors/events"
	"github.com/Gui-Rondon/hyrule/behaviors/searchsync"
	"github.com/Gui-Rondon/hyrule/outbox"
	"github.com/Gui-Rondon/hyrule/platform/pubsub"
	"github.com/Gui-Rondon/hyrule/platform/redis"
	"github.com/Gui-Rondon/hyrule/platform/search"
	"github.com/Gui-Rondon/hyrule/rest"
)

const (
	// topicSchedule is the local outbox destination that writes the
	// scheduling hash in Redis: the entry changes if and only if the post
	// write committed.
	topicSchedule    = "local:posts-schedule"
	scheduleHash     = "posts:scheduled"
	defaultToPublish = "posts-to-publish"
	defaultToUnpub   = "posts-to-unpublish"
)

// setup registers the posts resource, its publish/schedule endpoints and
// the outbox relay (Pub/Sub for everything, Redis for the local schedule
// destination).
func setup(ctx context.Context, env *app.Env) error {
	cfg := env.Config
	store := env.Store(cfg.String("mongo.collection", "posts"))
	box := outbox.New(env.DB)
	toPublish := cfg.String("pubsub.to_publish", defaultToPublish)
	toUnpub := cfg.String("pubsub.to_unpublish", defaultToUnpub)

	res := resource(store)
	// publishCondition is declarative: any write that lands the post on
	// "published" — the custom /publish endpoint below, or a plain PATCH
	// that happens to set the same field — emits the event on its own, no
	// "if" needed in the endpoint (contrast with /unpublish, whose
	// transition isn't representable as "reached a value": events.With
	// stays hand-written for that one).
	res.Hooks.Use(draftHook(), audit.Hooks(), events.Hooks(box), events.ConditionalHooks(box, publishCondition(toPublish)))

	if _, ok := cfg.Get("elasticsearch.hosts"); ok {
		es, err := search.FromConfig(cfg)
		if err != nil {
			return err
		}
		res.Hooks.Use(searchsync.Hooks(es, cfg.String("elasticsearch.index", "posts")))
		env.AddCheck(app.ElasticsearchCheck("elasticsearch_check", es))
	}

	svc := env.API.Register(res)

	ps, err := pubsub.Connect(ctx, cfg)
	if err != nil {
		return err
	}
	rdb, redisName, err := redis.Connect(cfg)
	if err != nil {
		return err
	}
	e := &endpoints{svc: svc, toUnpub: toUnpub}
	env.AddCheck(pubsub.Check(cfg, ps, toPublish))
	env.AddCheck(redis.Check(rdb, redisName))

	env.API.Handle("POST /posts/{id}/publish", env.API.RequireAuth(http.HandlerFunc(e.publish)))
	env.API.Handle("DELETE /posts/{id}/publish", env.API.RequireAuth(http.HandlerFunc(e.unpublish)))
	env.API.Handle("POST /posts/{id}/schedule", env.API.RequireAuth(http.HandlerFunc(e.schedule)))
	env.API.Handle("DELETE /posts/{id}/schedule", env.API.RequireAuth(http.HandlerFunc(e.unschedule)))

	pub := &outbox.PubSub{Client: ps}
	relay := &outbox.Relay{
		Outbox:    box,
		Publisher: dispatcher(rdb, cfg.String("redis.schedule_hash", scheduleHash), pub),
	}
	check, err := outbox.CheckFromConfig(cfg, relay)
	if err != nil {
		return err
	}
	env.AddCheck(check)
	env.Go("outbox-relay", func(ctx context.Context) error {
		defer pub.Stop()
		return relay.Run(ctx)
	})
	return nil
}

var slugRe = regexp.MustCompile(`^[\w-]+$`)

func resource(store rest.Store) *rest.Resource {
	objectID := rest.Field{Type: rest.TypeObjectID}
	objectIDs := func(max int) rest.Field {
		return rest.Field{Type: rest.TypeList, MaxLength: max, Default: []any{}, Items: &objectID}
	}
	nullableDate := rest.Field{Type: rest.TypeDatetime, Nullable: true}
	return &rest.Resource{
		Name:      "posts",
		ItemTitle: "post",
		Store:     store,
		Schema: rest.Schema{
			"title":          {Type: rest.TypeString, Required: true},
			"slug":           {Type: rest.TypeString, Required: true, Unique: true, Regex: slugRe},
			"excerpt":        {Type: rest.TypeString},
			"body":           {Type: rest.TypeString},
			"author_id":      objectID,
			"category_id":    {Type: rest.TypeObjectID, Nullable: true},
			"tags":           objectIDs(50),
			"status":         {Type: rest.TypeString, Allowed: []any{"draft", "scheduled", "published"}},
			"allow_comments": {Type: rest.TypeBoolean, Default: true},
			"_published":     nullableDate,
			"_unpublished":   nullableDate,
		},
		AdditionalLookup:  &rest.AdditionalLookup{Field: "slug", Pattern: slugRe},
		ResourceMethods:   []string{http.MethodGet, http.MethodPost},
		ItemMethods:       []string{http.MethodGet, http.MethodPatch, http.MethodPut},
		PublicMethods:     rest.PublicGET,
		PublicItemMethods: rest.PublicGET,
		SoftDelete:        true,
		QueryDenylist:     rest.DefaultQueryDenylist,
		CacheControl:      "max-age=20",
		CacheExpires:      20,
	}
}

// draftHook defaults a new post's status to draft.
func draftHook() rest.Hooks {
	return rest.Hooks{OnInsert: []func(context.Context, []rest.Doc) error{
		func(_ context.Context, docs []rest.Doc) error {
			for _, doc := range docs {
				if s, _ := doc["status"].(string); s == "" {
					doc["status"] = "draft"
				}
			}
			return nil
		},
	}}
}

// publishCondition emits a post.published event the moment "status" lands
// on "published" by any write path — see events.ConditionalHooks.
func publishCondition(topic string) outbox.Condition {
	return outbox.OnFieldChange("status", []string{"published"}, func(doc map[string]any) outbox.Event {
		id, _ := doc["_id"].(bson.ObjectID)
		return outbox.Event{Topic: topic, Type: "post.published", Key: id.Hex(), Body: doc}
	})
}

// endpoints are the custom publish/schedule actions, outside the generic
// CRUD a plain Resource gives for free.
type endpoints struct {
	svc     *rest.Service
	toUnpub string
}

// target finds the post of the URL; an id that is not an ObjectID or a
// missing post is a 404.
func (e *endpoints) target(w http.ResponseWriter, r *http.Request) (string, bson.M, rest.Doc, bool) {
	id := r.PathValue("id")
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		rest.WriteError(w, r, rest.ErrNotFound)
		return "", nil, nil, false
	}
	lookup := bson.M{"_id": oid}
	doc, err := e.svc.Find(r.Context(), lookup)
	if err != nil {
		rest.WriteError(w, r, err)
		return "", nil, nil, false
	}
	return id, lookup, doc, true
}

func (e *endpoints) respond(w http.ResponseWriter, r *http.Request, lookup bson.M) {
	doc, err := e.svc.Get(r.Context(), lookup)
	if err != nil {
		rest.WriteError(w, r, err)
		return
	}
	e.svc.WriteItem(w, r, doc)
}

// publish marks the post as published. The outbox event itself is not
// this endpoint's job anymore — publishCondition above fires it from the
// write alone, so a plain PATCH setting the same status does too.
func (e *endpoints) publish(w http.ResponseWriter, r *http.Request) {
	_, lookup, _, ok := e.target(w, r)
	if !ok {
		return
	}
	changes := rest.Doc{"status": "published", "_published": time.Now().UTC().Format(rest.DateFormat)}
	if _, err := e.svc.Patch(r.Context(), lookup, changes, rest.WriteOptions{SkipConcurrency: true}); err != nil {
		rest.WriteError(w, r, err)
		return
	}
	e.respond(w, r, lookup)
}

// unpublish sends the id to posts-to-unpublish. Unlike publish, "no longer
// published" isn't a value a write lands on — draft is also the default of
// a brand new post — so this stays an explicit events.With instead of a
// Condition.
func (e *endpoints) unpublish(w http.ResponseWriter, r *http.Request) {
	id, lookup, _, ok := e.target(w, r)
	if !ok {
		return
	}
	ctx := events.With(r.Context(), outbox.Event{Topic: e.toUnpub, Type: "post.unpublished", Key: id, Body: id})
	changes := rest.Doc{"status": "draft", "_unpublished": time.Now().UTC().Format(rest.DateFormat)}
	if _, err := e.svc.Patch(ctx, lookup, changes, rest.WriteOptions{SkipConcurrency: true}); err != nil {
		rest.WriteError(w, r, err)
		return
	}
	e.respond(w, r, lookup)
}

// scheduleEntry is the message of the local destination topicSchedule; a
// nil PublishAt removes the entry from the Redis hash.
type scheduleEntry struct {
	ID        string  `json:"_id"`
	PublishAt *string `json:"publish_at"`
}

// schedule moves the post to status scheduled and, in the same write
// transaction, records an outbox event that the relay routes to the local
// Redis hash (not Pub/Sub) — a scheduler process can scan that hash instead
// of polling Mongo.
func (e *endpoints) schedule(w http.ResponseWriter, r *http.Request) {
	id, lookup, original, ok := e.target(w, r)
	if !ok {
		return
	}
	ifMatch := r.Header.Get("If-Match")
	if err := e.svc.CheckEtag(original, ifMatch); err != nil {
		rest.WriteError(w, r, err)
		return
	}
	body, err := readJSONObject(r)
	if err != nil || len(body) == 0 && r.ContentLength == 0 {
		rest.WriteError(w, r, rest.Abort(http.StatusBadRequest, ""))
		return
	}
	value, _ := body["publish_at"].(string)
	if value == "" {
		rest.WriteError(w, r, rest.Abort(http.StatusUnprocessableEntity, "publish_at is required."))
		return
	}
	when, err := time.Parse(rest.DateFormat, value)
	if err != nil {
		rest.WriteError(w, r, &rest.ValidationError{Issues: rest.Issues{"publish_at": "must be of datetime type"}})
		return
	}
	iso := when.UTC().Format("2006-01-02T15:04:05")
	ctx := events.With(r.Context(), outbox.Event{
		Topic: topicSchedule, Type: "post.scheduled", Key: id,
		Body: scheduleEntry{ID: id, PublishAt: &iso},
	})
	changes := rest.Doc{"status": "scheduled"}
	if _, err := e.svc.Patch(ctx, lookup, changes, rest.WriteOptions{IfMatch: ifMatch}); err != nil {
		rest.WriteError(w, r, err)
		return
	}
	e.respond(w, r, lookup)
}

// unschedule returns the post to draft and removes it from the Redis hash.
func (e *endpoints) unschedule(w http.ResponseWriter, r *http.Request) {
	id, lookup, original, ok := e.target(w, r)
	if !ok {
		return
	}
	ifMatch := r.Header.Get("If-Match")
	if err := e.svc.CheckEtag(original, ifMatch); err != nil {
		rest.WriteError(w, r, err)
		return
	}
	ctx := events.With(r.Context(), outbox.Event{
		Topic: topicSchedule, Type: "post.unscheduled", Key: id,
		Body: scheduleEntry{ID: id},
	})
	changes := rest.Doc{"status": "draft"}
	doc, err := e.svc.Patch(ctx, lookup, changes, rest.WriteOptions{IfMatch: ifMatch})
	if err != nil {
		rest.WriteError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+doc["_etag"].(string)+`"`)
	rest.WriteJSON(w, http.StatusOK, e.svc.WriteResult(doc))
}

// dispatcher sends the schedule events to the Redis hash and everything
// else to Pub/Sub.
func dispatcher(rdb redis.Client, hash string, next outbox.Publisher) outbox.Dispatcher {
	return outbox.Dispatcher{
		Local: map[string]outbox.PublisherFunc{
			topicSchedule: func(ctx context.Context, m outbox.Message) error {
				var entry scheduleEntry
				if err := json.Unmarshal(m.Data, &entry); err != nil || entry.ID == "" {
					return errors.Join(errors.New("posts: invalid schedule event"), err)
				}
				if entry.PublishAt == nil {
					return rdb.HDel(ctx, hash, entry.ID).Err()
				}
				return rdb.HSet(ctx, hash, entry.ID, *entry.PublishAt).Err()
			},
		},
		Next: next,
	}
}

// readJSONObject reads an optional JSON object, keeping numbers as sent.
func readJSONObject(r *http.Request) (map[string]any, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if len(bytes.TrimSpace(data)) == 0 {
		return out, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
