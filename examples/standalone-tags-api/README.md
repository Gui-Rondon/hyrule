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

## Optional local tags lookup pilot

`internal/taglookup` is an application-local executor for the single read-only
`tags.lookup` operation. It is not registered in `main`; its HTTP handler/server helpers are opt-in.
Construct it explicitly with an already registered tags `rest.Service`, a fixed
resource scope, a trusted identity verifier, an authorization policy and a mandatory
audit sink. Missing dependencies fail construction; missing identity/permission
fails before any storage query. The existing REST authentication is unchanged.

The executor accepts only `{"slug":"go"}` (strict bounded JSON), looks up one
enabled tag and returns only `name`, `slug`, `enabled`. Resources with query-mutating
`OnPreGet` hooks are rejected. Fetched hooks run before output checks. Dependencies
and resource definitions must remain immutable and safe for concurrent use.
Callbacks/readers must honor cancellation; this is not an arbitrary-code sandbox.
Execution has a two-second budget plus up to one second for mandatory audit after
cancellation. Classify failures with `errors.Is` and the package's sentinel errors.

Run the SQLite-backed pilot tests without external services:

```sh
go test -race ./internal/taglookup
```

The root `make standalone-example-test` already includes these tests. The
JWT integration tests use real RSA signatures and validate issuer, audience,
expiration, subject and the exact tags:read scope. Production issuer/key
configuration and external transport remain application work. A local persistent
SQLite audit sink is included.
See the [pilot contract](../../website/docs/reference/read-pilot.md) and its
[Portuguese version](../../website/i18n/pt-BR/docusaurus-plugin-content-docs/current/reference/read-pilot.md).


`taglookup.NewJWTIdentity` supplies `Verify` and `Authorize` callbacks for the
executor. Configure explicit issuer, audience, resource scope and trusted public
RSA keys by key ID. Pass an untrusted compact token with `taglookup.WithToken`;
only successful signature/claim validation establishes identity. The integration
accepts RS256 and the exact space-delimited `scope` permission, without legacy
fallback, role inference, JWKS/network access or changes to REST authentication.
See the pilot contract for composition and rotation details.


`LoadJWTIdentity(JWTFileConfig)` loads a provisioned RSA public-key PEM while
keeping issuer/audience/scope/key ID explicit. `OpenSQLiteAudit(ctx, path)` supplies
a persistent audit callback (`sink.Write`) with WAL/FULL synchronization and
independent committed appends. Use a separate file from resource data; close the
sink on shutdown. New audit files use mode 0600; existing permissions, retention,
backup and volume protection remain deployment concerns. The pilot contract
includes startup composition and failure behavior. No credentials are bundled.


`NewHTTPHandler(service, identity, audit.Write)` exposes authorized GET discovery
and POST invocation at `/tools/tags.lookup`. It requires a single Bearer header,
strict JSON input and mandatory audit; cookies, query credentials and inherited
token contexts grant no access. `NewLocalHTTPServer("127.0.0.1:8081", handler)`
creates, but does not start, a loopback-only server with read/write/header limits.
The application owns startup/shutdown. No default route, remote deployment or TLS
provisioning is introduced; MCP is an optional separate adapter described below. The pilot tests include a real loopback socket
and a partial-body timeout, so their environment must permit local listening.


Authorized HTTP GET and `Executor.Discover` now return JSON Schema 2020-12 input
and output contracts, actual executor limits, explicit server-only rules and a
deterministic SHA-256 contract hash. Field types derive from registered resource
schema; the lookup's restricted slug and result shapes are operation-specific.
Discovery performs authorization/audit without data queries. Returned metadata is
fresh per call; hash/schema validation never replace execution authorization.
The schemas are tested against the real parser and SQLite lookup results.


The optional `internal/tagmcp` adapter pins official Go SDK v1.8.0 and protocol
2025-11-25. `NewServer`/`RunStdio` reuse discovery/execution with an explicit local
credential source and per-operation authorization/audit. Tests use actual SDK
clients over memory and a stdio subprocess. `main` is unchanged; MCP HTTP/OAuth
and remote deployment are not included. See the [MCP pilot](../../website/docs/reference/mcp-pilot.md).


Build the opt-in executable with
`go build -o .local/tags-mcp ./cmd/tags-mcp` (create `.local` first).
`--help` lists nine required variables on stderr. The command takes explicit
schema/data/audit/key/token paths and opens an existing SQLite source read-only.
It generates no data/tokens and does not change the REST server. The
[MCP guide](../../website/docs/reference/mcp-pilot.md) includes `mcpServers`
configuration, troubleshooting and compatibility limits.
`go test -race ./cmd/tags-mcp` verifies the actual binary with the SDK client and
local fixtures.

Optional pgvector documentation retrieval: see [setup and limits](../../website/docs/reference/knowledge-pilot.md).

For source changes, immediate ACL/deletion denial and background indexing, see [versioned source/outbox](../../website/docs/reference/knowledge-sync.md) (`enqueue` and `worker`).
