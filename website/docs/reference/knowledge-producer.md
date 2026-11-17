---
sidebar_position: 28
---

# Concrete article producer

The standalone module now includes `internal/knowledgedocs` and the optional
`cmd/knowledge-docs` curator CLI. A concrete Hyrule `rest.Resource` uses the
Postgres adapter and normal `Service.Insert/Patch/Replace/Delete` operations.
Its pre-write hooks materialize the knowledge source and call `QueueSource`.
The article row, source/ACL update and outbox event share the same transaction.
The existing [worker and MCP reader](knowledge-sync.md) consume these changes.
The default tags REST application is unchanged.

## Explicit mapping and access

An article contains `title`, `body`, `readers` and optional `private_notes`.
Only title and body become retrieval text. Readers are exact JWT subjects, with
an empty list denying everyone. Private notes are retained in the article only;
they are absent from the source projection, events, embeddings and MCP excerpts.
Source IDs/references are `articles/UUID`; a deterministic hash of projected
fields supplies the source revision. Edits to private notes do not enqueue work.

The curator must have a valid JWT with exact `knowledge:write`, pinned issuer,
audience, key ID and configured corpus. Authentication/authorization precede input
parsing, and the CLI checks them before opening the database or document file.
Hooks repeat authorization inside the transaction. This permission lets a trusted
curator manage all articles and their ACLs in that configured corpus. It does not
imply `knowledge:read`; readers still require that permission and document ACL.

PATCH/PUT/DELETE require a specific matching ETag. Validation remains enabled;
clients cannot choose an ID, corpus or validation/concurrency bypass. Inputs reject
unknown/duplicate fields and are bounded. PATCH uses the adapter's actual merge
semantics; PUT uses the original ID. Delete creates a knowledge tombstone.
Later hook/adapter failures roll back the projection and event too.

Each configured corpus uses a physically separate article table derived from its
scope. The current Postgres adapter does not apply `Resource.Datasource.Filter`,
so this producer does not rely on that property for isolation. Scope is trusted
process configuration, never an input field. The source and outbox remain scoped
as described in the synchronization guide.

## Run

Provision the vector extension as database administrator first. From the standalone
module, set database URL and scope, then explicitly provision the producer:

```sh
go build -o /tmp/knowledge-docs ./cmd/knowledge-docs
export KNOWLEDGE_DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/knowledge?sslmode=disable'
export KNOWLEDGE_SCOPE='hyrule-docs'
/tmp/knowledge-docs init
```

`init` uses database provisioning privileges to create the source, chunk, outbox
and corpus article tables. Normal write commands never create schemas. Apply
appropriate grants separately; the existing MCP reader needs no article-table
access. Use a trusted issuer to provision a curator JWT with `knowledge:write`.
Configure the same pinned identity fields as the other pilot, with its token file:

```sh
export KNOWLEDGE_PUBLIC_KEY_FILE='/absolute/path/to/public.pem'
export KNOWLEDGE_KEY_ID='provisioned-key-id'
export KNOWLEDGE_ISSUER='https://your-issuer.example'
export KNOWLEDGE_AUDIENCE='hyrule-knowledge'
export KNOWLEDGE_TOKEN_FILE='/absolute/path/to/curator.jwt'
export KNOWLEDGE_DOCUMENT_FILE='/absolute/path/to/article.json'
/tmp/knowledge-docs create
```

Example `article.json`:

```json
{"title":"Transactions","body":"Source and outbox commit together.","readers":["alice"],"private_notes":"Internal editorial note"}
```

The command returns only operation, UUID and ETag, never document contents. Save
these values for subsequent writes. For example, put `{"readers":["bob"]}` in a
separate patch file and use:

```sh
export KNOWLEDGE_DOCUMENT_ID='UUID_RETURNED_BY_CREATE'
export KNOWLEDGE_IF_MATCH='ETAG_RETURNED_BY_CREATE'
export KNOWLEDGE_DOCUMENT_FILE='/absolute/path/to/patch.json'
/tmp/knowledge-docs patch
```

Use the latest returned ETag for each next operation. `replace` accepts a complete
article document; `delete` needs ID/ETag and reads no document file. Run the existing
`knowledge-mcp worker` with matching scope/embedding contract to index events, and
`knowledge-mcp serve` with a reader token. ACL/deletion denial starts at source
commit, independently of when the worker finishes.

This is a concrete application producer in the example. It creates no HTTP write
endpoint and adds no MCP mutation tool. Production deployments still need their
own issuer configuration, credentials, operational supervision and domain audit
policy. Controlled MCP mutations and their approval/idempotency protocol remain
Phase 10 work.

## Evidence

Real Postgres/pgvector integration tests exercise article creation, body/ACL edits,
private-field exclusion, ETag rejection, PUT/delete, rollback after a later domain
hook fails and cross-corpus isolation. The pipeline ends in an authenticated SDK
MCP search with persistent audit. A separate CLI test proves authorization occurs
before database/body processing and verifies provisioning, creation and deletion.
The pgvector CI job runs both packages. No production application was deployed.
