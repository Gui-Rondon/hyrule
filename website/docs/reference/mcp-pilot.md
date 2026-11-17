---
sidebar_position: 24
---

# Local MCP pilot

**Status:** optional local adapter implemented on 2026-11-16. It reuses the
[tags pilot](read-pilot.md) executor and discovery. Production credentials,
desktop-client adoption, MCP HTTP/OAuth and multi-user deployment remain open.

## Protocol and client selected

The standalone example pins the official `github.com/modelcontextprotocol/go-sdk`
**v1.8.0**, compatible with the example's Go version. The pilot selects protocol
**2025-11-25**, covering `tools/list` and `tools/call`. SDK dependencies remain
inside the standalone module; the framework and `taglookup` import no MCP package.

| Client and transport | Verified behavior |
|---|---|
| Official Go SDK v1.8.0 client, in-memory wire transport, proposed 2025-11-25 | Negotiation, authorized listing, invocation and cancellation. |
| Same client, actual stdio subprocess and compiled `cmd/tags-mcp`, proposed 2025-11-25 | Negotiation, listing, signed-token lookup and persistent SQLite audit. |
| Same client, proposed 2026-07-28, in-memory transport | SDK negotiates 2025-11-25 and discovery succeeds. This is fallback, not validation of the newer protocol. |

This is an explicit pilot compatibility matrix, not a claim about ChatGPT,
Claude Desktop or every MCP client. Applications must honor the negotiated version.
The SDK handles protocol negotiation, framing, request decoding and cancellation.
See [SDK v1.8.0 server documentation](https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/docs/server.md)
and the [2025-11-25 tool schema](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/2025-11-25/schema/2025-11-25/schema.ts).

## Composition

`examples/standalone-tags-api/internal/tagmcp` is a separate internal package.
`NewServer(Config)` requires a registered service, JWT identity, fixed resource
scope, mandatory audit sink and a local credential supplier. `RunStdio(ctx, cfg)`
creates the SDK server and runs it on stdin/stdout with a 64 KiB inbound frame cap.
The tool's argument cap remains 1 KiB and its output cap remains 16 KiB.

```go
cfg := tagmcp.Config{
    Service: tagsService, Identity: identity, Scope: resourceScope,
    Audit: audit.Write,
    Token: func(ctx context.Context) (string, error) {
        return credentialStore.Token(ctx)
    },
}
return tagmcp.RunStdio(ctx, cfg)
```

The application provisions identity/credential storage and owns resource/audit
shutdown. `Token` supplies an untrusted compact JWT for this server's local owner;
it must honor context and be concurrency-safe. Credential retrieval shares the
executor's two-second budget, respecting an earlier caller deadline. Each operation validates its
signature, issuer, audience, expiration, subject and exact scope again. Use one
server per local owner. Neither being on stdin nor completing initialization
establishes an end-user identity. The handshake advertises generic implementation
and capability metadata; protected tool definitions require authorization.

Stdio reserves stdout for SDK protocol frames; application logs belong on stderr.
The example's `main` is unchanged and no tool process starts automatically. The
application can construct a different transport using `NewServer`, but then owns
its frame/read limits and authentication design. This pilot does not implement
an MCP HTTP authorization server, OAuth discovery or token delegation.

## Operation mapping

`tools/list` calls `Executor.Discover` for every request. Authorized callers receive
one tool with the existing input/output schemas, read-only hint and namespaced
application metadata containing version, contract hash, limits and server-only
rules. No second schema inference or static registry is maintained. Discovery
runs policy and commits audit without querying resource storage; its result is
not cached as a session grant. This single-tool catalog does not paginate;
nonempty cursors are rejected after authorization/audit.

`tools/call` passes raw JSON arguments directly to `Executor.Execute`. Duplicate
keys and oversize arguments therefore receive the same validation as HTTP/local
calls. Successful results include both structured content and matching JSON text.
Unknown tools also traverse identity/policy/audit before returning a sanitized
protocol error, without storage access. Requests needing multi-round-trip state
are unsupported. Resource/prompt methods are not exposed.

MCP output schemas declare `type: object` at the root. The shared discovery
contract now states this explicitly while retaining the same found/not-found
variants. Its content hash changes accordingly, without changing lookup semantics.
Read-only annotations are descriptive hints; policy and the executor enforce access.

## Credential and error boundaries

The credential supplier is application configuration, not `_meta`, tool arguments
or another local owner's session state. Requests carrying forged client identity
metadata cannot grant permissions. Empty/failed suppliers produce an authentication
failure and a denied-attempt audit record. Permission changes or token expiration
take effect on the next operation, including an already initialized session.

| Condition | Result |
|---|---|
| Missing/invalid identity | JSON-RPC server error -32001, `unauthenticated`. |
| Missing permission/resource mismatch | JSON-RPC server error -32003, `forbidden`. |
| Unknown tool or unsupported cursor | Invalid-params protocol error after authorization/audit. |
| Invalid arguments, query failure, invalid output, deadline or audit failure during a known call | Tool result with `isError=true`, safe text, no structured lookup data. |
| Discovery/audit failure | Protocol error without tool schema/hash. |
| Unsupported method | Method-not-found protocol error. |

Audit failure takes precedence even when combined with invalid input. Audit receives
a fresh context without credentials, and its own one-second budget. It must commit
before metadata or lookup results are released. Raw dependency errors, tokens and
private fields are not returned. Malformed protocol frames are handled by the SDK;
they are not successful tool invocations or promises of a domain audit record.

## Verification

```sh
cd examples/standalone-tags-api
go test -race ./internal/tagmcp
```

Tests use the real SDK client/server over in-memory framing and an actual stdio
subprocess, real RSA-signed tokens, the existing YAML resource, real SQLite and
persistent audit. They exercise schema/hash equivalence with HTTP/local discovery,
found/absent/disabled results, repeated authorization, separate owner servers,
forged client metadata, duplicate/oversize arguments, unknown tools, audit failures,
negotiation and cancellation reaching storage. Existing HTTP tests still require
permission to open a loopback socket when running the whole standalone module.

The selected SDK is scanned with the module's existing vulnerability workflow.
Use a patched Go toolchain for local scans: the older local Go 1.27.2 reports
standard-library advisories independently of the MCP dependency. No global Go
configuration is changed by this pilot.

## Executable command

The standalone module now provides `cmd/tags-mcp`. It starts MCP stdio only;
the original REST server and `main` remain unchanged. From the example directory:

```sh
mkdir -p .local
go build -o .local/tags-mcp ./cmd/tags-mcp
.local/tags-mcp --help
```

`--help` writes to stderr. Other arguments are rejected without echoing their
values. All configuration is explicit environment/files, with no command-line
credentials. Use absolute paths in clients whose working directories may differ.

| Required variable | Contents |
|---|---|
| `TAGS_MCP_RESOURCES_FILE` | Existing YAML with one `name: tags`, `collection: tags` resource and expected fields. An omitted backend is explicitly bound to SQLite by this command; another explicit backend is rejected. |
| `TAGS_MCP_DATA_FILE` | Existing SQLite file with a matching table/index already provisioned through Hyrule. It opens with `mode=ro`; the command creates no data, seeds or migrations. |
| `TAGS_MCP_AUDIT_FILE` | Separate audit file with an existing writable parent directory. It may be created during startup. |
| `TAGS_MCP_PUBLIC_KEY_FILE` | Regular file containing a trusted RSA public-key PEM. |
| `TAGS_MCP_KEY_ID` | `kid` corresponding to the provisioned public key. |
| `TAGS_MCP_ISSUER` | Expected issuer, equal to the `iss` claim. |
| `TAGS_MCP_AUDIENCE` | Expected audience, present in `aud`. |
| `TAGS_MCP_SCOPE` | Fixed resource scope, such as `local:tags`; separate from the permission claim. |
| `TAGS_MCP_TOKEN_FILE` | Regular file containing the local owner's compact JWT, without a Bearer prefix; its `scope` must contain the exact `tags:read` permission. |

The canonical example schema remains `resources.yaml`. This launcher's explicit
SQLite binding does not change the standalone REST API's Mongo default. Prepare
the database through existing SQLite APIs/adapters first; see [SQLite](sqlite.md)
and [YAML backends](yaml-backends.md). Startup checks compatibility on a read-only
connection and fails if a table/index would need to be created. Experimental
SQLite still offers no production migration/parity guarantee.

Configuration, regular input files and initial token authorization are checked
before starting the protocol. Audit cannot reuse data/schema/key/token files;
existing-file symlink/hardlink aliases are rejected too. Public keys load at
startup; the token file is reread per operation and bounded to 8 KiB plus optional
newline. Prefer atomic file replacement for token refresh and restrict access to
token contents. An invalid/expired replacement denies the next operation without
returning data.

The process handles EOF, interruption and SIGTERM, closing the session, audit and
data connection. Startup has a five-second budget; requests retain executor/audit
budgets. File operations/hooks must cooperate with the local environment; this
is not a filesystem sandbox or forced-interruption mechanism.

## Client configuration

This format applies to clients accepting `mcpServers` with `command`, `args` and
`env`, as described in the [official local-server guide](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/docs/2026-07-28/develop/connect-local-servers.mdx).
Replace all paths and identity settings with application-provisioned values;
the JSON contains neither a token nor a private key.

```json
{
  "mcpServers": {
    "hyrule-tags": {
      "command": "/absolute/path/to/tags-mcp",
      "args": [],
      "env": {
        "TAGS_MCP_RESOURCES_FILE": "/absolute/path/to/resources.yaml",
        "TAGS_MCP_DATA_FILE": "/absolute/path/to/tags.db",
        "TAGS_MCP_AUDIT_FILE": "/absolute/path/to/audit.db",
        "TAGS_MCP_PUBLIC_KEY_FILE": "/absolute/path/to/public.pem",
        "TAGS_MCP_KEY_ID": "replace-with-provisioned-key-id",
        "TAGS_MCP_ISSUER": "https://issuer.example.test",
        "TAGS_MCP_AUDIENCE": "replace-with-configured-audience",
        "TAGS_MCP_SCOPE": "local:tags",
        "TAGS_MCP_TOKEN_FILE": "/absolute/path/to/owner.jwt"
      }
    }
  }
}
```

The verified matrix remains the official SDK client, now launching the compiled
executable from a different working directory. This configuration format is
integration guidance; no Claude Desktop/ChatGPT UI session was run here. Clients
must negotiate the pilot's supported 2025-11-25 protocol.

## Command troubleshooting

| Symptom | Check |
|---|---|
| Process exits for missing configuration | Read stderr and set all nine variables in the client process, not only another terminal. |
| Invalid key/identity | Check RSA public PEM, kid, issuer, audience, sub, exp and exact `tags:read` permission; omit `Bearer` from the file. |
| Incompatible/read-only database | Verify an existing file and adapter-provisioned table/index; the command does not initialize or migrate data. |
| Audit provisioning fails | Check a writable directory and a file separate from data/credentials, including aliases. |
| Calls become unauthenticated after working | Check token expiration/replacement; there is no cached session grant. |
| Process appears idle in a terminal | Stdio waits for MCP client frames; use a compatible client rather than curl. |
| Client cannot decode stdout | Launch the binary directly without wrappers printing banners/logs to stdout. |

`go test -race ./cmd/tags-mcp` builds the actual binary and provisions temporary
key/token/database fixtures only. It checks discovery, lookup, live token refresh,
read-only source data, persisted audit, protocol-only stdout, startup errors, EOF
and interruption. Production credentials are not bundled.

## Remaining scope

Phase 9 has a local interoperability proof, not a production rollout. Selecting
an actual application/desktop client, provisioning credentials, operating audit,
multi-tenant isolation and any remote MCP transport still need application evidence.
Controlled MCP mutations remain a future increment. Retrieval is implemented in
the separate [knowledge pilot](knowledge-pilot.md), with source/outbox and an
article producer. The adapter creates no
general agent framework, automatic CRUD export or production deployment.

The optional [documentation retrieval pilot](knowledge-pilot.md) adds pgvector-backed knowledge tools in a separate local executable.
