---
sidebar_position: 27
---

# Versioned knowledge source and outbox

The optional documentation pilot now supports asynchronous indexing through the
existing `outboxpostgres` relay. It adds an authoritative Postgres document source,
not an automatic subscription to every Hyrule resource or filesystem change.
The MCP server still exposes only `knowledge.search` and `knowledge.fetch`.

## Consistency and permission boundary

`hyrule_knowledge_documents` stores source text, ACL, opaque revision label,
monotonically increasing numeric version and deletion tombstone. Source changes
and a minimal `{scope,id,version}` outbox event commit in **one local transaction**.
Neither source text nor ACL lists are included in events. Each corpus has a
separate outbox table, derived from its scope, so its relay cannot consume another
corpus's work.

Search and fetch join chunks to the current source and require its ACL, a live
(non-deleted) document and matching numeric version. **Queries starting after a
revocation/deletion commit cannot return the old chunks**, even if the provider
is down or the relay is paused. In-flight SQL reads use their statement snapshot.
Newly permitted users also wait for the current version to be indexed: consistency
fails closed, at the cost of temporary missing results. JWT authentication and
`knowledge:read` still precede query embedding and retrieval.

The worker rereads the current source, embeds outside source/index transactions,
then compares its version under the document lock before atomically replacing
chunks. A source change during embedding discards obsolete work. Old events
index the latest source rather than replay old contents; tombstones prevent
resurrection. Already-indexed versions/contracts skip embedding on redelivery,
including a crash after indexing but before outbox acknowledgement. Concurrent
workers may compute duplicate embeddings; delivery is at least once, not exactly
once. Empty ACLs deny everyone and are purged without embedding, as are deletions.

The existing relay supplies retry/backoff, a Postgres DLQ, health queries and
`Replay`. Its claiming transaction holds the **outbox row lock** during the
publisher call; source/index transactions remain short. The command polls every
second with a five-minute publish budget for a complete document. Provision at
least two pool connections, and one embedding contract per corpus's workers.
Schema setup and operational DLQ monitoring/replay remain operator responsibilities.

## Provision and use

From `examples/standalone-tags-api`, build `cmd/knowledge-mcp` as described in
[the pilot setup](knowledge-pilot.md). Set database URL and scope **before** init:

```sh
export KNOWLEDGE_SCOPE='hyrule-docs'
/tmp/knowledge-mcp init
```

`init` creates/upgrades source/chunk tables and the corpus's outbox. It does not
infer trust for legacy chunks: they have source version zero and cannot be read.
Reimport prior pilot data using `ingest`, or create versioned sources with
`enqueue` and run `worker`. Reapply the read role's SELECT grants to both
`hyrule_knowledge_documents` and `hyrule_knowledge_chunks` after provisioning.
Use separate credentials for provisioning, trusted source writing, indexing and
MCP reading. No new network write endpoint or public ingestion tool is created.

For asynchronous import, a manifest entry adds `expected_version`:

```json
[
  {
    "id": "architecture-decisions",
    "source": "website/docs/reference/architecture-decisions.md",
    "revision": "INDEXED_GIT_COMMIT",
    "subjects": ["alice"],
    "expected_version": 0
  }
]
```

Zero means source creation. For updates use its current **numeric** source version,
not its Git revision. A stale conflicting update is rejected; an identical snapshot
may be submitted again without changing its version or adding another event.
Read `version` from `hyrule_knowledge_documents` by scope/document ID or use the
trusted Go `LoadSource` API. With the same confined document root/manifest settings:

```sh
/tmp/knowledge-mcp enqueue
/tmp/knowledge-mcp worker
```

`enqueue` needs database URL, scope, document root and manifest; it never calls the
embedding provider. `worker` needs database URL, scope and the embedding model,
dimension and task-prompt settings from the pilot; it does not require a JWT or
audit file because it is an operator process with database credentials. `serve`
retains mandatory JWT authorization and independent request audit. These commands
must use the same embedding contract. Stop the worker with interrupt/SIGTERM.

To revoke everyone, enqueue an existing document with `subjects: []` and its
expected version. To delete it, enqueue a tombstone (no source file needed):

```json
[
  {"id":"architecture-decisions","revision":"withdrawn","deleted":true,"expected_version":1}
]
```

A recreation requires the tombstone's current version. Omitting an entry from a
manifest or editing a file is **not** a source update. A producer must call
`enqueue`/`QueueSource`. Manifests commit per document; a failed later entry does
not roll back earlier entries. The synchronous `ingest` command is retained for
trusted, serialized snapshot imports; do not mix it with concurrent versioned
producers because it intentionally replaces the authoritative snapshot.

## Application integration and tests

`Postgres.QueueSource(ctx, scope, document, deleted, expectedVersion)` requires
an active `platform/postgres.Transactor` transaction on the exact same pool.
Applications can call it inside their own transaction/resource hook after
materializing permitted document fields and ACLs. The API rejects autocommit and
foreign-pool contexts. No cross-database atomicity, Mongo pipeline, automatic
resource-to-document mapping or application-specific ACL inference is claimed.
The source table is authoritative for this consumer; applications must keep it
transactionally aligned with their actual permission authority.

Real pgvector tests cover source/event rollback together, failed event insertion,
optimistic writer conflicts, immediate denial while stale physical chunks remain,
empty ACL/deletion purge, reordered events, duplicate delivery, recreation after
deletion, changes during embedding, provider failure, DLQ and replay. A real stdio
process test enqueues an ACL change without embeddings, verifies denial before
worker processing, starts the worker, retrieves with the new subject and verifies
a tombstone before the worker resumes. Legacy unversioned chunks fail closed.

This closes the local Postgres source/outbox path. Production producer wiring,
DLQ operations, retention/tombstone policy and larger quality/load evaluation are
still deployment work. A future Qdrant index must recheck this authoritative source
before returning results; its native MCP server alone does not supply that policy.

For a model/contract rebuild, stop old-contract workers, configure the new contract
on worker/server, and enqueue each source with a new opaque revision label and
its current expected version (or use serialized synchronous `ingest`). An identical
snapshot intentionally creates no new event, so rerunning unchanged `enqueue`
is not a model-reindex trigger. Source versions change and old chunks stay hidden
until the new contract is indexed.

A [concrete article producer](knowledge-producer.md) now connects Hyrule Service writes and pre-write hooks to this pipeline, with curator permission and explicit reader ACLs.
