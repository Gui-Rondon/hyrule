---
sidebar_position: 2
---

# Querying: `where=`, `sort`, `projection`

Every list endpoint (the resource's root URL, and any custom list route
built with `Service.WriteList`/`WriteListFallback`) reads `where`,
`sort`, `projection`, `page` and `max_results` from the query string, the
way Eve does. Parsing lives in `rest/query.go`
(`ParseListQuery`/`parseWhere`/`ParseSort`) — this page documents that
parsing, not an aspirational query language: what's below is exactly
what the code accepts today.

## `where=`: Mongo's query language, not a DSL of its own

`where=` is a JSON object, decoded and passed to the `Store` almost
unchanged — it **is** a MongoDB filter document, operators included
(`$gt`, `$in`, `$exists`, ...), not an abstraction over one:

```
GET /posts?where={"status":"published"}&sort=-_published&max_results=10
```

Two conversions happen on the way in (`mongotize`, matching Eve's
`_mongotize`):

- A string matching `DateFormat` (`Mon, 02 Jan 2006 15:04:05 GMT`, Eve's
  `DATE_FORMAT`) becomes a `time.Time`.
- A 24-hex-character string becomes a BSON `ObjectID` — unless the
  resource sets `QueryObjectIDAsString` (Eve's
  `IDS_ARE_HEX_AND_OBJECTID_COMPATIBLE`), in which case it's left as a
  string.

```
GET /posts?where={"category_id":"507f1f77bcf86cd799439011"}
```

matches documents where `category_id` equals that `ObjectID`, not the
string.

### What's rejected

Before conversion, every key in the parsed JSON (recursively, including
inside arrays) is checked against `Resource.QueryDenylist`. `nil` means
`DefaultQueryDenylist`: `$where`, `$function`, `$accumulator` — the
operators that run JavaScript server-side. Eve's own default
(`MONGO_QUERY_BLACKLIST`) also blocks `$regex`; a resource that wants
that behavior sets it explicitly in `QueryDenylist`. A denied operator
anywhere in the filter fails the whole request with a `400`, as
`ErrDeniedOperator`.

Malformed JSON is also a `400` — there is no partial recovery.

### What's out of scope

`where=` reaches `Store.Find`/`FindOne`/`CountDocuments`, which take a
MongoDB *filter*, not an aggregation pipeline — there's no way to express
`$lookup`, `$group` or `$facet` through the query string. A resource that
needs that reads straight from the driver instead, bypassing `Store`
for that one query — see `categories-api`'s `GET /categories/tree`
(`examples/blog-apis/services/categories-api/categories.go`) and
[`store.md`](store.md#places-that-still-go-straight-to-mongo-on-purpose).

## `sort`

Three syntaxes, matching what Eve's client code historically sent:

| Syntax | Example |
|---|---|
| Comma list, `-` for descending | `sort=-_published,title` |
| JSON/Python list of pairs | `sort=[("_published", -1), ("title", 1)]` |
| JSON/Python dict | `sort={"_published": -1, "title": 1}` (dict order is preserved) |

An empty list (`[]`) or dict (`{}`) means no sort. Anything else that
doesn't match one of the three shapes is a `400`.

## `projection`

A JSON object, passed straight through as the Mongo projection
(`{"title": 1, "body": 0}`) — no validation beyond being valid JSON.

## How the server's own filter composes with the client's

A resource's `Datasource.Filter`, and any lookup a custom route passes to
`WriteList`/`WriteListFallback`, are combined with the client's `where=`
via `$and` — never a plain merge, so the client can't shadow a key the
server set:

```go
q.Filter = bson.M{"$and": bson.A{clientWhere, serverLookup}}
```

The client's `where=` is always evaluated; it narrows the server's
filter, never replaces it.

## Pagination knobs that interact with these

`page` and `max_results` follow `Resource.Pagination` (`Default`,
`Limit`, `Disabled`) with Eve's exact edge cases: a non-numeric or
non-positive `max_results` falls back to the default, `max_results` is
capped at `Limit`, `page` uses its absolute value and an invalid `page`
is ignored (falls back to `1`). With `Pagination.Disabled`, the default
is `0` (no limit), there's no cap, and `page` isn't read at all —
every matching document comes back in one response.

`show_deleted` (presence, not value) includes soft-deleted documents on
a `SoftDelete` resource — Eve's own convention.

## On a relational backend

None of the above is Mongo-agnostic — `where=` *is* Mongo's query
language, operators included. `platform/postgres.Filter`
doesn't carry any of this over: it's equality-only (`column: value`,
ANDed together, a `nil` value meaning `IS NULL`), no `$gt`/`$in`/denylist
to speak of, because there's no JSON query language to parse in the
first place:

```go
docs, err := store.Find(ctx, postgres.Filter{"status": "published"}, 0, 10,
	[]postgres.SortField{{Column: "published_at", Desc: true}}, nil)
```

Same shape as `Find`'s skip/limit/sort/columns above, a narrower filter
language on purpose — see [Postgres backend](postgres-rfc.md) for why a
richer one isn't decided yet.

## Optimizing these queries

None of the above makes a query fast on its own — see
[Query performance today](store.md#query-performance-today) for how
indexing (and its Postgres-vs-Mongo differences) fits in.
