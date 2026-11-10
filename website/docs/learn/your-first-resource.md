---
sidebar_position: 3
---

# Your First Resource

[Quick Start](./quick-start.md) built a resource from YAML. This page
builds the same kind of resource in Go — `examples/blog-apis`'s
`authors-api` — one field at a time, so you can see what each part of a
`Resource` actually does.

:::info[You will learn]
- What `rest.Resource` and `rest.Schema` are
- The field options you'll reach for most: `Required`, `Unique`, `Regex`, `Default`
- How `ResourceMethods`/`ItemMethods` and `PublicMethods` control what's exposed
- Where a resource plugs into a running service
:::

## A resource is a Go value

Everything about an `authors` resource — its name, its storage, its
schema, which HTTP methods it answers — is one `rest.Resource`
value:

```go
res := &rest.Resource{
	Name:      "authors",
	ItemTitle: "author",
	Store:     env.Store("authors"),
	Schema:    rest.Schema{ /* next section */ },
}
```

`Store` is how the resource talks to MongoDB — it's an interface, not a
concrete `*mongo.Collection`, which is what makes it possible to test a
resource against an isolated database or, down the line, swap the
storage underneath (see [Reference → `Store`](/docs/reference/store) for
the full story). `env.Store("authors")` builds the Mongo-backed one for
the `authors` collection.

## The schema, field by field

`rest.Schema` is a `map[string]rest.Field`. Start with the
obvious fields:

```go
Schema: rest.Schema{
	"name": {Type: rest.TypeString, Required: true},
	"bio":  {Type: rest.TypeString},
},
```

`Required: true` means `POST /authors` without a `name` is a 422, before
anything touches the database. A field with no `Required` is simply
optional.

Now add validation that depends on the value, not just its presence:

```go
"email": {Type: rest.TypeString, Unique: true, Regex: regexp.MustCompile(`^.+\@.+\..+$`)},
```

`Unique: true` runs a check against the collection before inserting or
updating: a second author with the same email is rejected, not stored.
`Regex` runs a pattern match — here, "looks like an email", intentionally
loose rather than a full RFC 5322 validator.

`authors-api` reuses one compiled pattern for two fields:

```go
var handle = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// ...
"twitter":   {Type: rest.TypeString, Regex: handle},
"instagram": {Type: rest.TypeString, Regex: handle},
```

Nothing special here — `Field.Regex` is just a `*regexp.Regexp`, so
anything you'd do with one in plain Go works.

<details>
<summary>Deep dive: what else can a field express?</summary>

`Default`, `Allowed` (an enum), `MinLength`/`MaxLength`, `Min`/`Max` for
numbers, `Nullable`, `NotEmpty`/`AllowEmpty`, and — for `dict`/`list`
fields — a nested `Schema` or `Items` field for validating structure.
`tags-api`'s `enabled` field uses the simplest of these:

```go
"enabled": {Type: rest.TypeBoolean, Default: true},
```

Every one of these has a direct equivalent in the YAML form from Quick
Start — see [Reference → `resources.yaml`](/docs/reference/resources-yaml)
for the full field reference, including the two field options
(`Field.Check`, a custom validation closure) that are Go-only because
they carry code, not data.

</details>

## Controlling what's exposed

```go
ResourceMethods:   []string{http.MethodGet, http.MethodPost},
ItemMethods:       []string{http.MethodGet, http.MethodPatch, http.MethodPut},
PublicMethods:     rest.PublicGET,
PublicItemMethods: rest.PublicGET,
```

`ResourceMethods` applies to the collection URL (`/authors`); `ItemMethods`
to a single document (`/authors/{id}`). `PublicMethods`/`PublicItemMethods`
list which of those don't require authentication — here, only `GET`:
anyone can read an author, but creating or editing one needs a token.

## Registering it

A resource does nothing by itself; a service registers it:

```go
func setup(_ context.Context, env *app.Env) error {
	res := &rest.Resource{ /* ... */ }
	env.API.Register(res)
	return nil
}

func main() {
	app.Main(app.Spec{Name: "authors-api", DefaultDB: "authors", Version: version, Setup: setup})
}
```

That's the entire shape of `examples/blog-apis/services/authors-api`:
one `Resource` literal, one `Register` call. There's no custom endpoint,
no hook beyond [audit tracking](./relations-and-hooks.md) — which is
exactly why this resource is also a perfect candidate to be a plain YAML
file, as you saw in Quick Start.

:::tip[Recap]
- A `Resource` is a Go value: name, `Store`, `Schema`, allowed methods.
- `Field` options (`Required`, `Unique`, `Regex`, `Default`, ...) cover
  most validation without writing a single `if`.
- `ResourceMethods`/`ItemMethods` and the `Public*` variants decide what's
  exposed and what needs a token.
- Next: [Relations and Hooks](./relations-and-hooks.md) builds a resource
  that references other resources and reacts to writes.
:::
