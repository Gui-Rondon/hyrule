---
sidebar_position: 23
---

# Read pilot — tags lookup

**Status:** Local pilot implemented in the standalone example's internal package,
2026-11-16. JWT RS256 identity and scope policy are integrated as optional dependencies;
audit can use the included persistent SQLite sink. No generic registry, production identity-provider
configuration or remote deployment is included. Local HTTP transport is opt-in. This
does not complete Phase 7 or establish production readiness.

## Consumer and resource

Use a local technical consumer that looks up a tag by its exact slug before
suggesting tags for a draft. This is a reproducible demonstration with synthetic
data, not an identified production team or an approved editorial business policy.
The schema comes from `examples/standalone-tags-api/resources.yaml`; the matching
Go resource in `examples/blog-apis/services/tags-api/tags.go` confirms that
`name`, `slug` and `enabled` already exist and slug is unique.

The standalone example normally uses Mongo; the local rehearsal attaches its
compiled schema to SQLite in memory. The blog example's public GET does not
implicitly authorize a tool call. Leave both examples' REST policies unchanged.

## Operation contract

Tool name: `tags.lookup`, contract version `1`. Read-only and registered explicitly.
Input is exactly one required string `slug`, 1–128 ASCII characters matching
`^[A-Za-z0-9_-]+$`. Reject extra properties, empty/non-string input, malformed JSON
and duplicate object keys. Matching is exact and case-sensitive; do not normalize
or silently truncate. The input body is limited to 1 KiB before decoding.

```json
{"slug":"go"}
```

The server builds the filter from validated input: slug equals the input AND
`enabled=true`. Clients cannot choose resource, tenant, datasource, backend,
`where`, SQL, operators, sort, projection, page or result limit. Use
`Adapter.ParseWhere`/`ParseProjection` for the bound service's query representation,
then `Service.List` with `Page=1`, `MaxResults=1`, `ShowDeleted=false`. This retains
mandatory datasource filters and existing read hooks. A caller-provided deadline
may be shorter; impose a maximum of two seconds. Do not retry automatically.

Projection requests only `name`, `slug`, `enabled`; map these fields to a fresh
output object after service hooks. Never serialize the entire `Doc`. Validate
the returned slug/enabled and field types before returning the allowlisted result;
fail closed on a hook-modified or inconsistent result. The response is capped at
16 KiB without truncating JSON. Limits are pilot choices, not framework defaults.

```json
{"found":true,"tag":{"name":"Go","slug":"go","enabled":true}}
```

Absent and disabled records both return `{"found":false}`. Never return IDs,
ETags, timestamps, audit fields, internal notes, total counts, other records or
raw adapter errors. Authorization and infrastructure failures must not be reported
as absence. Only `found` appears on a miss; only `found` and `tag` appear on a hit.

## Proposed access policy

| Check | Rule |
|---|---|
| Enablement | Off by default; application explicitly registers this resource/tool. |
| Identity | Trusted authentication boundary provides a nonempty verified subject. User arguments cannot supply principal or permissions. |
| Permission | Application policy grants exact `tags:read` for `tags.lookup` version 1 on this bound resource; otherwise deny. The permission is a pilot proposal, not an existing Hyrule claim convention. |
| Resource scope | One application-owned synthetic dataset per pilot instance; bind service/database at startup, never select them from input. |
| Discovery | Show the tool only to an authorized principal; execution repeats authorization even for direct name-based calls. |
| Fields | Return the three explicit fields above; enforce enabled-only lookup server-side. |
| Writes | No insert, patch, replace, delete, approval or dry-run path exposed. |
| Audit | Record actor, tool/version, policy decision, correlation ID, duration and outcome; redact credentials and content. Mandatory audit failure prevents successful execution. |
| Failure | Separate invalid input, unauthenticated, forbidden, deadline/cancellation, unavailable/internal error and not-found result; never expose DSNs/SQL/tokens. |

The underlying tags schema has no tenant field. Do not pretend this pilot tests
shared-database tenant isolation. Reject selecting a tenant/dataset from tool
arguments. Production multi-tenancy needs a separate application decision:
server-bound per-tenant storage or a schema/filter with enforced tenant ownership,
plus isolation tests before exposure to multiple tenants.

`platform/auth.Authenticator.Authenticate` returns empty claims when auth is
disabled; that cannot satisfy the pilot's identity requirement. `auth.WithClaims`
is context storage, not proof that claims were verified. The legacy static-key
path does not itself establish all issuer/audience/required-claim checks needed
for this pilot. Select a verified issuer/audience and require expiration/subject
at the application boundary. Do not weaken existing REST authentication globally
or assume calling `Service` invokes it. There is no configured identity provider
or remote endpoint in this pilot definition.

## Implementation boundary

The local increment is a narrow application-level adapter for this one operation,
with an injected trusted identity/policy boundary and explicit audit sink. Do not
start a general-purpose registry before proving these needs. No automatic CRUD
export, new schema language, arbitrary code execution, model provider or MCP SDK
is required. Local tests can inject principals through a test-only verifier; that
is not authentication for a deployed service.

Before external use, choose and validate the actual authentication/transport
integration. A missing verifier/policy, disabled auth or an empty subject must
fail closed. Timeouts bound both the query and application handling; verify
cancellation reaches storage. Stable output/error contracts belong to this pilot
and do not replace Eve's REST envelopes.

## Acceptance cases for the next increment

- Unauthorized discovery and direct invocation are denied before any storage call.
- Missing identity, disabled auth, empty subject, wrong resource scope or absent
  permission deny access. Untrusted argument/context claims cannot grant access.
- Enabled exact slug returns only the allowlisted fields. Disabled and absent
  slugs produce the same not-found shape.
- Extra/duplicate keys, oversized payload, invalid slug/type and attempted raw
  filter/projection/tenant overrides fail before storage access.
- Query limit remains one even if resource pagination is disabled. Mandatory
  datasource filters and hooks remain effective. Hook-modified data cannot bypass
  enabled/slug/output rules.
- Deadline, cancellation, database failure, output size/type failure and audit
  failure have explicit errors and never become `found=false`.
- Storage mutation methods are never called; seeded records remain unchanged.

## Local executor implementation — 2026-11-16

`examples/standalone-tags-api/internal/taglookup` provides:

- `New(Dependencies)`, requiring a service, fixed scope, `Verify`, `Authorize` and `Audit`.
- `Describe(ctx)`, using the same authorization as direct calls, without querying storage.
- `Execute(ctx, io.Reader)`, enforcing strict JSON, extra/duplicate-key rejection,
  fixed filtering/projection and reconstructed output after hooks.
- Sentinel errors for identity, policy, input, storage, output,
  cancellation/deadline and audit, without propagating raw dependency errors.

The package is internal to the standalone module; it does not change `rest`/`app`
and is not wired into `main`. External consumers gain no new framework public API.
The example still starts as before and does not automatically expose tools.

**Explicit limits.** Resources with `OnPreGet` are rejected at construction:
these hooks can rewrite filtering/pagination and invalidate the one-row bound.
`OnFetchedResource` hooks still run, followed by slug/enabled/type/size checks.
Resource configuration must remain immutable and hooks/dependencies must be
trusted; the executor does not sandbox arbitrary Go or undo external hook effects.

Execution has a two-second budget, respecting earlier caller deadlines. Audit uses
a fresh context without inherited tokens/claims, independent of cancellation,
with an additional one-second budget,
including to record failed/canceled calls. Audit failure clears output and returns
`ErrAudit`; use `errors.Is` to classify errors that may combine audit and original
failures. Records describe operation outcome before response delivery; duration
excludes audit persistence. Readers, hooks, callbacks and storage must cooperate
with cancellation: blocked code is not forcibly interrupted. A mounted transport
must configure its own read timeout; the local server helper below does so.

Tests use the existing YAML and real SQLite. They cover denial before storage,
wrong scope, discovery, invalid input, a one-row bound with pagination disabled,
mandatory filtering, hook-added fields, invalid output, storage/audit failure,
cancellation/deadline, no mutations and concurrent calls with isolated audit.
Initial executor tests use synthetic verification; the identity tests below
exercise signed RSA tokens and JWT claim validation.

```sh
cd examples/standalone-tags-api
go test -race ./...
```

Production identity-provider configuration, remote deployment, production policy
and multi-tenant isolation remain pending; a local persistent audit sink is available. Registry/MCP work should follow evidence from a concrete
consumer rather than automatic generalization.

## JWT identity and policy integration — 2026-11-16

`NewJWTIdentity(JWTConfig)` implements the identity/policy boundary using the
already pinned `golang-jwt/jwt/v5` dependency. Configuration requires an issuer,
audience, resource scope and trusted RSA public keys of at least 2048 bits indexed
by `kid`. It copies both the key map and RSA moduli so later caller mutations do
not change trust. There is no disabled mode.

`WithToken(ctx, compactJWT)` only carries an **untrusted** credential without a
Bearer prefix; it grants no identity. `Verify` accepts RS256 only, a valid
signature from a known `kid`, exact configured issuer/audience, required valid
expiration and a nonempty `sub` up to 256 bytes. Optional `nbf` and `iat` are also
validated, with no extra clock leeway. Tokens are limited to 8 KiB.

`Authorize` revalidates the token and compares the subject, `tags.lookup`, version
`1`, `tags:read` permission and configured resource scope. The `scope` claim must
be a space-delimited string containing the exact `tags:read` scope. Wildcards,
substrings, `roles`, `scp`, usernames and context claims do not grant permission.
This is the pilot's explicit convention; providers with another claim shape need
a reviewed mapping rather than an automatic fallback.

Bind `identity.Verify` and `identity.Authorize` into executor dependencies, using
the same resource scope in both constructors. The application supplies actual
public keys, issuer/audience and received credential; tool arguments cannot select
these values. `auth.WithClaims` is not consulted. Existing legacy REST
authentication and example configuration are unchanged.

```go
identity, err := taglookup.NewJWTIdentity(taglookup.JWTConfig{
    Issuer: issuer, Audience: audience, Scope: resourceScope,
    Keys: map[string]*rsa.PublicKey{keyID: publicKey},
})
// Handle err before constructing the executor.
executor, err := taglookup.New(taglookup.Dependencies{
    Service: tagsService, Scope: resourceScope,
    Verify: identity.Verify, Authorize: identity.Authorize, Audit: auditSink,
})
// Handle err; compactJWT comes from the trusted application's credential input.
ctx = taglookup.WithToken(ctx, compactJWT)
output, err := executor.Execute(ctx, strings.NewReader(`{"slug":"go"}`))
```

This illustrates composition inside the standalone module, without registering a
route or supplying a permissive fake verifier. The package remains internal.
There is no network access, discovery/JWKS, `jku`/`x5u` interpretation or key
fallback. Rotate by constructing a new instance with application-provisioned
keys. Online revocation/introspection and grants outside the signed scope are not
implemented; policy is reevaluated on every call against current configuration.

Tests sign real tokens with temporary RSA keys and use real SQLite. They cover
invalid signature/key/algorithm, missing/incorrect claims, expiration, future
nbf/iat, forged context claims, missing permission, subject/version/tool/resource
substitution and caller mutation of key configuration. Rejected tokens never
query storage. This validates local cryptographic integration, not connectivity
to an already configured production provider.

## Provisioning and persistent audit — 2026-11-16

`LoadJWTIdentity(JWTFileConfig)` reads an application-provisioned RSA public-key
PEM file (up to 64 KiB). Issuer, audience, scope and `kid` remain explicit;
private keys, invalid files or missing configuration fail startup. Files are read
once, so replacement does not mutate an existing verifier. For overlapping key
rotation, use `NewJWTIdentity` with both provisioned keys. This example chooses no
actual production credential, issuer or filesystem path.

`OpenSQLiteAudit(ctx, path)` opens a persistent SQLite file separate from resource
data, enables WAL and `synchronous=FULL`, and creates `tags_lookup_audit`. `Write`
appends a sequence, UTC timestamp and sanitized JSON metadata. Its commit must
finish before executor output is released. The caller owns and closes the sink at
shutdown. New files use mode 0600; existing files retain deployment-owned modes.

The following composition does not register a route or change example `main`:

```go
func configuredLookup(ctx context.Context, service *rest.Service,
    cfg taglookup.JWTFileConfig, auditPath string,
) (*taglookup.Executor, *taglookup.SQLiteAudit, error) {
    identity, err := taglookup.LoadJWTIdentity(cfg)
    if err != nil {
        return nil, nil, err
    }
    audit, err := taglookup.OpenSQLiteAudit(ctx, auditPath)
    if err != nil {
        return nil, nil, err
    }
    executor, err := taglookup.New(taglookup.Dependencies{
        Service: service, Scope: cfg.Scope,
        Verify: identity.Verify, Authorize: identity.Authorize,
        Audit: audit.Write,
    })
    if err != nil {
        audit.Close()
        return nil, nil, err
    }
    return executor, audit, nil
}
```

Real SQLite tests verify persistence after reopening, concurrent appends,
recording canceled calls and withholding output when the sink closes or audit
fails. An integrated rehearsal loads a provisioned PEM and signed RSA token,
looks up a tag and rereads persisted events without credentials or arguments.

This is a locally committed SQLite log, not a tamper-proof ledger or exactly-once
delivery. Failure after commit can cause a retried read to create another event.
Retention, backup, access controls, permissions on existing files and volume
protection belong to deployment. Do not use the resource's file as the audit file.
WAL/synchronization availability depends on the filesystem; configuration failures
are rejected. Remote deployment, production provider/credentials and multi-tenant
policy remain pending. This package starts no server and performs no deployment.

## Optional local HTTP transport — 2026-11-16

`NewHTTPHandler(service, identity, audit.Write)` creates a handler using configured
`JWTIdentity`, fixed resource scope and mandatory audit. It accepts no permissive
verifier or cookie/query/inherited-context credential fallback. Each call requires
exactly one `Authorization: Bearer <compactJWT>` header; `main` still registers no
tool route and starts no tool server automatically.

| Request | Authorized result |
|---|---|
| `GET /tools/tags.lookup` | Descriptor with schemas, limits and hash; no storage query. |
| `POST /tools/tags.lookup` | JSON input `{"slug":"go"}` and the executor result. |

POST requires `application/json`. Query parameters, GET bodies and extra/duplicate
keys are rejected; the 1 KiB limit also covers unknown-length/chunked bodies.
Metadata/input failures pass through identity, policy and audit before responding,
without storage access. Unknown routes return 404; methods other than GET/POST
return 405 with `Allow`, without invoking a tool. There is no CORS, cookie auth,
redirect or REST fallback.

| Failure | HTTP |
|---|---|
| Missing/invalid credential | 401 with Bearer challenge |
| Missing permission | 403 |
| Invalid input | 400 |
| Body over 1 KiB | 413 |
| Unsupported Content-Type | 415 |
| Body-read timeout / canceled invocation | 408 |
| Executor deadline | 504 |
| Audit failure or unavailability | 503 |
| Inconsistent output | 500 |

Errors contain stable codes, never tokens, DSNs or raw dependency errors. All
responses use `Cache-Control: no-store`, `Vary: Authorization`, JSON and `nosniff`.
Audit failure takes precedence and withholds both lookup output and descriptors.
Absent/disabled tags still return HTTP 200 with `{"found":false}`.

`NewLocalHTTPServer("127.0.0.1:8081", handler)` creates but does not start an
`http.Server`. Only explicit numeric loopback addresses are accepted; wildcard,
hostname and remote binds are rejected. It sets two-second header/read budgets,
a five-second write budget, 30-second idle timeout and 16 KiB headers. The
application owns listening, shutdown and closing the audit sink. If mounted in
another server, apply equivalent read/write limits; Go context alone cannot
interrupt a blocked reader.

```go
handler, err := taglookup.NewHTTPHandler(tagsService, identity, audit.Write)
if err != nil {
    return err
}
server, err := taglookup.NewLocalHTTPServer("127.0.0.1:8081", handler)
if err != nil {
    return err
}
// The application may call server.ListenAndServe and owns shutdown.
```

Tests cover authorized/denied GET and POST, credential isolation, no cookie/query
fallback, oversized chunked bodies, sanitized failures and audit before output.
An actual loopback connection test uses a signed RSA token and SQLite audit,
including a partial-body client that times out on the server. No remote
deployment, provisioned TLS, MCP or production credentials are included.

## Contract discovery — 2026-11-16

`Executor.Discover(ctx)` and authorized HTTP GET now include, alongside `name`,
`version` and `read_only`:

- `description`: the lookup's purpose and exact matching semantics.
- `input_schema`: a JSON Schema 2020-12 object requiring only `slug`, with 1–128
  permitted ASCII characters and no additional properties.
- `output_schema`: two strict result variants: `found=false` without a tag, or
  `found=true` with `name`, `slug`, `enabled=true`. HTTP errors are not successful
  results and retain their separate codes.
- `limits`: input/output byte limits, one result and execution/audit budgets,
  derived from the executor's actual constants.
- `server_rules`: constraints not enforced by structural validation alone.
- `contract_hash`: hexadecimal SHA-256 of deterministic descriptor JSON excluding
  the hash field itself. It identifies content, not credentials, and does not
  replace the operation's semantic version.

The three field types come from registered `Resource.Schema`. Operation-specific
constraints belong to this read, not a second CRUD schema: uniqueness/defaults
and write callbacks do not become lookup arguments or validators. No generic
exporter is added to `rest`, and no tables/documents are enumerated.

Parser and descriptor share length limits and character constraints. ASCII is
expressed by excluding invalid characters without regex anchors, avoiding Go/JS
end-of-string differences for a final newline. The server compares output slug
to input; JSON Schema alone cannot express this relationship. Byte limits,
duplicate JSON keys, identity, policy, hooks and committed audit also remain
explicit server rules.

Every discovery repeats authorization and audit through `Describe`, without
querying storage. There is no cross-user cache or real data in the descriptor.
Each call returns fresh maps/slices, so caller mutation cannot change the next
response. Missing permission or audit failure yields neither contracts nor hash.
Execution still rechecks permission independently of GET.

Tests compile the published schemas and compare acceptance with the actual parser,
including boundaries, Unicode/newlines, required fields and extra properties.
Actual found/absent/disabled results validate against output schema; private fields,
missing fields and enabled=false are rejected. Tests also cover deterministic
hashing, mutation isolation, no user data, no execution of write callbacks and
GET/POST contract parity.

This is local pilot discovery, not completion of the generic catalog or Phase 8.
A future MCP adapter can reuse `Discover` and `Execute` without duplicating schema
or authorization after selecting a compatible client, transport and version.

## Local query rehearsal and its limits

A temporary program compiled the existing YAML, seeded two synthetic records into
SQLite memory and called the real `Service.List` through `restsqlite`. A write hook
added an internal note to demonstrate why the final output must be reconstructed.
No listener, credentials, production data or other infrastructure were used.

```text
go => {"found":true,"tag":{"enabled":true,"name":"Go","slug":"go"}}
hidden => {"found":false}
missing => {"found":false}
```

This proves the bounded filter/projection shape works locally. It does **not**
prove the proposed input parser, identity, permission, audit, output limits or
failure behavior: those were acceptance requirements at the rehearsal stage. The later local
executor is described above; production provider configuration and remote deployment remain pending.

To repeat, save the Go block below as `/tmp/hyrule-read-pilot/main.go` after
creating that directory, then run from the repository root:

```sh
go run /tmp/hyrule-read-pilot/main.go
```

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/Gui-Rondon/hyrule/declarative"
	"github.com/Gui-Rondon/hyrule/platform/sqlite"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restsqlite"
	"os"
	"time"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:")
	must(err)
	defer db.Close()
	raw, err := os.ReadFile("examples/standalone-tags-api/resources.yaml")
	must(err)
	file, err := declarative.Parse(bytes.NewReader(raw))
	must(err)
	res, err := file.Resources[0].Build(nil)
	must(err)
	adapter, err := restsqlite.NewAdapter(ctx, db, "tags", res)
	must(err)
	res.Adapter = adapter
	// Seed only synthetic data. The hook deliberately adds a server-only field
	// to demonstrate why the result must be allowlisted after Service hooks.
	res.Hooks.OnInsert = append(res.Hooks.OnInsert, func(_ context.Context, docs []rest.Doc) error {
		for _, d := range docs {
			d["internal_note"] = "synthetic secret"
		}
		return nil
	})
	api := rest.New(rest.Options{Transactor: sqlite.Transactor{DB: db}})
	svc := api.Register(res)
	_, err = svc.Insert(ctx, []rest.Doc{{"name": "Go", "slug": "go", "enabled": true}, {"name": "Hidden", "slug": "hidden", "enabled": false}}, rest.WriteOptions{})
	must(err)
	projection, err := adapter.ParseProjection(`{"name":1,"slug":1,"enabled":1}`)
	must(err)
	for _, slug := range []string{"go", "hidden", "missing"} {
		query, _ := json.Marshal(map[string]any{"slug": slug, "enabled": true})
		filter, err := adapter.ParseWhere(string(query), res)
		must(err)
		readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		result, err := svc.List(readCtx, &rest.ListQuery{Filter: filter, Projection: projection, Page: 1, MaxResults: 1})
		cancel()
		must(err)
		if len(result.Docs) > 1 {
			panic("unbounded result")
		}
		out := map[string]any{"found": len(result.Docs) == 1}
		if len(result.Docs) == 1 {
			d := result.Docs[0]
			out["tag"] = map[string]any{"name": d["name"], "slug": d["slug"], "enabled": d["enabled"]}
		}
		encoded, err := json.Marshal(out)
		must(err)
		fmt.Printf("%s => %s\n", slug, encoded)
	}
}
```
