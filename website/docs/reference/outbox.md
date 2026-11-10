---
sidebar_position: 3
---

# Transactional outbox

`internal/outbox` implements the outbox pattern: a write stores its
events in the `outbox` collection, in the same Mongo transaction as the
write — both are stored together or neither is. After the commit, a
`Relay` publishes the pending events (at least once; every message
carries the `event_id` attribute for consumers to deduplicate).

```go
box := outbox.New(env.DB)
res.Hooks.Use(events.Hooks(box))          // flushes the events attached to ctx
```

## Two ways to decide what to publish

**`events.With`** — business code explicitly decides, before the hook,
that this specific write should emit an event:

```go
ctx := events.With(r.Context(), outbox.Event{Topic: "posts-to-unpublish", Key: id, Body: id})
svc.Patch(ctx, lookup, changes, opts)
```

Use this when the condition isn't "a field reached this value" — for
example `examples/blog-apis/services/posts-api`'s `/unpublish`: "draft"
is also the default status of a brand new post, so there's no way to
express "stopped being published" by only looking at the final value.

**`events.ConditionalHooks` + `outbox.Condition`** — the write itself
decides, with no `if` in business code:

```go
cond := outbox.OnFieldChange("status", []string{"published"}, func(doc map[string]any) outbox.Event {
	return outbox.Event{Topic: "posts-to-publish", Type: "post.published", Key: doc["_id"].(bson.ObjectID).Hex()}
})
res.Hooks.Use(events.ConditionalHooks(box, cond))
```

`OnFieldChange` covers the common case: it fires when the field's
effective value (after the write) is one of the target values and —
outside an insert — represents a real transition (the previous value
differed). Once registered, **any** write path that lands on that value
fires the event, not just one dedicated endpoint:
`examples/blog-apis/services/posts-api/posts.go` uses exactly this
condition for `/publish`, and a plain `PATCH /posts/{id}` that also sets
`status: published` fires the same way
(`TestPlainPatchAlsoPublishes` in `posts_test.go`).

The two coexist on the same `Resource` — `events.Hooks` and
`events.ConditionalHooks` are independent hook bundles, combined with
`res.Hooks.Use(...)`.

## Local destinations (no real Pub/Sub)

`outbox.Dispatcher` routes by topic before reaching the real `Publisher`:

```go
outbox.Dispatcher{
	Local: map[string]outbox.PublisherFunc{
		"local:posts-schedule": func(ctx context.Context, m outbox.Message) error {
			// write to a Redis hash, for instance — never leaves for Pub/Sub
		},
	},
	Next: &outbox.PubSub{Client: ps}, // anything not "local:*" falls through here
}
```

`examples/blog-apis/services/posts-api` uses this in `/schedule`: the
schedule event writes to a Redis hash (for a scheduler to scan) in the
same transaction as the write, never touching Pub/Sub.

## `Outbox.InTransaction`

`Outbox.Add` refuses to write outside a transaction (`ErrNoTransaction`)
— the check is `Outbox.InTransaction func(ctx) bool`, which `outbox.New`
fills in with the Mongo driver's checker (`mongo.SessionFromContext`). It
isn't hardcoded inside `Add`: a backend other than Mongo would swap that
field for its own way of knowing "I'm inside a transaction".
`Outbox.AllowNoTransaction` turns the check off (standalone Mongo in
development only — never in production).
