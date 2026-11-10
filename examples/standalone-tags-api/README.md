[Português](README.pt-br.md)

# standalone-tags-api

The Fase 2 proof for Hyrule's Framework path (see
[`website/docs/reference/roadmap.md`](../../website/docs/reference/roadmap.md)):
a resource with a YAML-declared schema plus one small Go hook (`audit`),
built by a module that depends on Hyrule the way an external team's own
repository would — `go get`, not an import of anything under `internal/`
(there's nothing left to import there for this: see
[ADR-0001](../../website/docs/reference/architecture-decisions.md#adr-0001--public-surface-of-the-framework)).

Unlike [`examples/blog-apis`](../blog-apis), this directory has its own
`go.mod` and `go.sum` — a separate Go module, not a package inside the
Hyrule module. That module boundary is the point: it's what makes this a
real test of "can an external project actually depend on this," not just
"does this compile inside the same `go.mod`."

## Why there's a `replace` directive

Hyrule isn't tagged yet (Fase 1 just landed the public import paths — see
the roadmap). `go.mod` here says:

```
require github.com/Gui-Rondon/hyrule v0.0.0-00010101000000-000000000000
replace github.com/Gui-Rondon/hyrule => ../..
```

A real external consumer, once a version is tagged, would instead write:

```
require github.com/Gui-Rondon/hyrule v0.101.0
```

and drop the `replace` line entirely — `go get github.com/Gui-Rondon/hyrule`
would do exactly that.

## Running locally

```bash
# from the repo root
make env-up    # Mongo (27018)

cd examples/standalone-tags-api
echo '{"auth": {"enable": "false"}}' > config.json
MONGO_CONNECTION_STRING='mongodb://localhost:27018/standalone_demo?directConnection=true' \
  CONFIG_FILE=config.json PORT=8080 go run .
```

```bash
curl -X POST localhost:8080/tags -H 'Content-Type: application/json' -d '{"name":"Go","slug":"go"}'
curl localhost:8080/tags/go
```

## Testing

```bash
cd examples/standalone-tags-api
MONGO_TEST_URI='mongodb://localhost:27018/?directConnection=true' go test ./...
```

`testkit` gives the test its own throwaway Mongo database — nothing about
it changes for a module outside Hyrule's own `go.mod`.
