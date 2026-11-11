---
sidebar_position: 4
---

# Local development and testing

:::info[You will learn]
- How to run and test a generated service with its own Makefile
- How to add a second service to the same project
- What's shared between services, and what isn't yet
:::

## Build, run, test

From the project root, with `services/articles/` created ([Creating
services](./creating-services.md)):

```bash
make env-up                 # Mongo, replica set, on :27017
make build SERVICE=articles
make run SERVICE=articles   # :8080, auth disabled
```

```bash
curl -X POST localhost:8080/articles -H 'Content-Type: application/json' \
  -d '{"name":"hello"}'
curl localhost:8080/articles/<id-from-the-response>
```

```bash
make test
```

`make test` runs `go test ./...`, so it picks up every service's tests
in one pass — `resource_test.go`'s `testkit.Service` call gives each
test its own throwaway Mongo database, same as everywhere else in
this framework (see [Testing](/docs/learn/testing)).

## Adding a second service

```bash
hyrule service create authors
make build SERVICE=authors
make run SERVICE=authors GO_PORT=8081
```

Each service is its own binary (`go build -o .local/<name>
./services/<name>`), so two can run side by side on different ports —
`GO_PORT` picks the port `make run` listens on. There's no process
manager or `docker-compose` entry generated per service; for local
development, running the ones you're working on in separate terminals
is enough. (`make env-up`'s `docker-compose.yml` is shared
infrastructure — Mongo — not a per-service thing to add to.)

## What's shared, what isn't

Today, services in the same project share:

- The `go.mod` (one module, one `go.sum`) and the local Mongo from
  `docker-compose.yml`.
- The `Makefile`'s pattern (`SERVICE=`-parametrized targets).

They do **not** share a coordinated multi-service dev command (there's
no `hyrule dev` that starts every service at once — see
[ADR-0004](/docs/reference/architecture-decisions))
or cross-service relations out of the box — wiring one service's
resource to another's (an `author_id` field, say) is written the same
way [Relations and Hooks](/docs/learn/relations-and-hooks) shows for
`blog-apis`'s `posts-api` → `authors-api`/`categories-api`, by hand.

:::tip[Recap]
- `make build`/`make run`/`make test SERVICE=<name>` work exactly like
  this repository's own Makefile — nothing new to learn.
- `make test` covers every service in one pass; running services
  locally is one `make run` per service, in its own terminal.
- There's no multi-service `dev` command yet — see the
  [roadmap](/docs/reference/roadmap).
:::
