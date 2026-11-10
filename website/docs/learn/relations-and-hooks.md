---
sidebar_position: 4
---

# Relations and Hooks

`authors-api` and `tags-api` stand alone. `posts-api` doesn't: a post
references an author, a category, and a list of tags, and it needs to
react to its own writes — stamp who changed it, index it for search.
This page builds that resource.

:::info[You will learn]
- How a resource references another one (and what hyrule does *not*
  check for you)
- The shape of `rest.Hooks`, and when each kind runs
- How to compose reusable hooks with `Hooks.Use`
:::

## A relation is just a field

```go
objectID := rest.Field{Type: rest.TypeObjectID}
objectIDs := func(max int) rest.Field {
	return rest.Field{Type: rest.TypeList, MaxLength: max, Default: []any{}, Items: &objectID}
}

Schema: rest.Schema{
	"author_id":   objectID,
	"category_id": rest.Field{Type: rest.TypeObjectID, Nullable: true},
	"tags":        objectIDs(50),
	// ...
},
```

`author_id` is required (no `Nullable`), `category_id` is optional,
`tags` is a list of up to 50 ids. That's it — hyrule validates that
these look like ObjectIDs, nothing more. **It does not check that the
author, category or tags actually exist.** If you need that, it's what
`Field.Check` is for: a closure that runs after the built-in rules,
free to do anything — including an HTTP call to `categories-api` to
confirm the id is real. It's deliberately a Go-only escape (see
[Reference → `resources.yaml`](/docs/reference/resources-yaml)):
that kind of check is business logic, not data.

## Hooks run around a write

A write goes through, in order: validation → `OnPrepare` → (the write,
inside a transaction) `OnInsert`/`OnUpdate`/`OnReplace`/`OnDelete` → commit
→ (outside the transaction) `OnInserted`/`OnUpdated`/`OnReplaced`/`OnDeleted`.
Everything before the commit can still abort the write; everything after
it can't roll anything back — it's for side effects like search
indexing that don't have a transaction of their own.

`posts-api` defaults a new post's status with an `OnInsert` hook:

```go
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
```

A hook mutates the document in place — there's nothing to return besides
an error. This one runs on every insert, for every document in the
batch, and sets `status` only when the client didn't send one.

## Reusable hooks

Writing the same `OnInsert` closure in every resource that needs it
would defeat the point. `behaviors/audit` packages exactly one
reusable hook bundle:

```go
res.Hooks.Use(draftHook(), audit.Hooks())
```

`audit.Hooks()` stamps `_created_user` on insert and `_updated_user` on
every write, reading the username from the request's token — the same
one line works for `authors-api`, `tags-api` and `posts-api`.
`Hooks.Use` just appends each bundle's hooks to the resource's own, in
order, so they compose freely:

```go
res.Hooks.Use(draftHook(), audit.Hooks(), events.Hooks(box), events.ConditionalHooks(box, cond))
```

The last two — `events.Hooks` and `events.ConditionalHooks` — are how a
write ends up in the transactional outbox. That's the next page.

:::tip[Recap]
- A relation is a plain `ObjectID`/list-of-`ObjectID` field; hyrule
  doesn't check the reference exists — add a `Field.Check` if you need that.
- Hooks run in a fixed order around a write; pre-commit hooks can abort
  it, post-commit hooks can't.
- `behaviors/*` packages reusable hook bundles; `Hooks.Use`
  composes them with a resource's own.
- Next: [The Transactional Outbox](./the-outbox.md).
:::
