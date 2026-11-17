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
| Same client, actual stdio subprocess, proposed 2025-11-25 | Negotiation, listing, signed-token lookup and persistent SQLite audit. |
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

## Remaining scope

Phase 9 has a local interoperability proof, not a production rollout. Selecting
an actual application/desktop client, provisioning credentials, operating audit,
multi-tenant isolation and any remote MCP transport still need application evidence.
Mutations and RAG remain separate, deferred increments. The adapter creates no
general agent framework, automatic CRUD export or production deployment.
