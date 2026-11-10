---
sidebar_position: 5
---

# The Transactional Outbox

When `posts-api` publishes a post, it needs two things to happen
together or not at all: the document changes to `status: published`,
*and* an event announcing it goes out. If the write succeeds but the
event is lost, nothing downstream ever hears about the publish. If the
event goes out but the write then fails, subscribers react to something
that never happened. This page builds the mechanism that makes "both or
neither" the only possible outcome.

:::info[You will learn]
- Why an event can't just be published after the write commits
- `events.With`: attaching an event to a write explicitly
- `outbox.OnFieldChange` + `events.ConditionalHooks`: making a write
  cause an event *by itself*, with no `if` in the endpoint
:::

## The problem with "publish after you write"

The obvious approach — write the document, then call a Pub/Sub client —
has a gap: the process can die between the two calls. hyrule's answer is
the outbox pattern: the event is written to an `outbox` collection *in
the same MongoDB transaction* as the document write. Both are stored
together, or neither is. A separate `Relay` reads that collection after
the fact and publishes to Pub/Sub, retrying until it succeeds.

```go
box := outbox.New(env.DB)
res.Hooks.Use(events.Hooks(box))
```

`events.Hooks(box)` is a hook bundle — the kind you met on the
[previous page](./relations-and-hooks.md) — that flushes whatever events
were attached to the write's context into `box`, inside the transaction.

## Attaching an event by hand

The simplest way to use it: business code decides, and attaches the
event before writing.

```go
ctx := events.With(r.Context(), outbox.Event{Topic: "posts-to-unpublish", Key: id, Body: id})
e.svc.Patch(ctx, lookup, changes, opts)
```

`posts-api`'s `/unpublish` endpoint does exactly this. It has to: "no
longer published" isn't a value a write lands on — `draft` is also the
status of a brand new post — so there's no way to tell from the
document alone whether this write is *the* unpublish. The endpoint
knows; the document doesn't.

## Letting the write decide

`/publish` is the opposite case: the document *does* know. Any write
that lands `status` on `"published"` should fire a `post.published`
event — whether that write came through `/publish`, or through a plain
`PATCH` nobody designed for this. That's `outbox.Condition`:

```go
func publishCondition(topic string) outbox.Condition {
	return outbox.OnFieldChange("status", []string{"published"}, func(doc map[string]any) outbox.Event {
		id, _ := doc["_id"].(bson.ObjectID)
		return outbox.Event{Topic: topic, Type: "post.published", Key: id.Hex(), Body: doc}
	})
}

res.Hooks.Use(events.Hooks(box), events.ConditionalHooks(box, publishCondition(toPublish)))
```

`OnFieldChange` fires the event when `status`'s value after the write is
`"published"` *and* it wasn't already — a document created directly as
`published`, or patched from `draft`/`scheduled` into `published`, both
count; resending `published` when it's already `published` does not.

With that hook registered, `/publish` shrinks to the one thing it's
actually responsible for — the write itself:

```go
func (e *endpoints) publish(w http.ResponseWriter, r *http.Request) {
	_, lookup, _, ok := e.target(w, r)
	if !ok {
		return
	}
	changes := evecompat.Doc{"status": "published", "_published": time.Now().UTC().Format(evecompat.DateFormat)}
	if _, err := e.svc.Patch(r.Context(), lookup, changes, evecompat.WriteOptions{SkipConcurrency: true}); err != nil {
		evecompat.WriteError(w, r, err)
		return
	}
	e.respond(w, r, lookup)
}
```

No `box.Add` call anywhere in this function. The event isn't the
endpoint's job anymore — it's a consequence of the write, which also
means a plain `PATCH /posts/{id}` that happens to set
`status: published` fires the exact same event, with nobody having to
remember to wire it up twice. `examples/blog-apis/services/posts-api/posts_test.go`'s
`TestPlainPatchAlsoPublishes` is that claim, as a test.

<details>
<summary>Deep dive: local destinations, for events that aren't Pub/Sub</summary>

`posts-api`'s `/schedule` endpoint writes its event to a Redis hash
instead of Pub/Sub — in the same transaction as the status change, so a
scheduler process can scan Redis instead of polling Mongo. `outbox.Dispatcher`
is what makes a topic name like `"local:posts-schedule"` resolve to a
Redis write instead of a real publish. See
[Reference → outbox](/docs/reference/outbox)
for the full mechanism.

</details>

:::tip[Recap]
- `outbox.New` + `events.Hooks` store events in the same transaction as
  the write; a `Relay` publishes them afterwards.
- `events.With` is for a condition only business code knows about.
- `outbox.OnFieldChange` + `events.ConditionalHooks` is for a condition
  the document itself expresses — any write path that reaches it fires
  the event, automatically.
- Next: [Testing](./testing.md) tests exactly this kind of hook, with no
  HTTP server involved.
:::
