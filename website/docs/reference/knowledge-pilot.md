---
sidebar_position: 25
---

# Documentation retrieval with pgvector and MCP

**Status:** optional local pilot. `examples/standalone-tags-api/cmd/knowledge-mcp`
adds `knowledge.search` and `knowledge.fetch` using the existing official Go MCP
SDK, RS256 identity verifier and separate SQLite audit. The framework has no new
mandatory dependency. This is document retrieval for an MCP client's model;
server-side answer generation is not included.

## Scope and permission model

The initial corpus is explicitly selected Markdown documentation/ADRs. A JSON
manifest supplies a stable document ID, relative source path, revision and allowed
JWT subjects. There is no implicit public access, role expansion or wildcard.
The document root is confined with Go `os.OpenRoot`, including symlink escapes.
Each file is limited to 1 MiB and split into deterministic 1,200-rune excerpts.

Every list/call verifies a fresh owner token, including issuer, audience, expiry
and subject. Only `knowledge:read` permits these tools on the configured corpus
scope; `tags:read` does not. PostgreSQL applies the configured scope and exact
subject ACL **before** ranking/limiting or fetching by ID. A missing or forbidden
ID returns the same not-found result. Neither scope nor subject is tool input.
Search returns at most five excerpts, source paths, revisions and cosine distances.
Output is capped at 16 KiB; large results fail closed rather than silently truncate.
Input is bounded and rejects unknown/duplicate keys. Each operation has a 10-second
budget plus a separate one-second audit budget. Failed audit withholds all output.

The [versioned source/outbox path](knowledge-sync.md) now supports asynchronous
source, ACL and deletion updates. Search/fetch check the authoritative source
version and ACL, blocking stale chunks after a change commits even while the
worker is paused. The synchronous `ingest` mode remains for serialized snapshots.
Editing files or omitting manifest entries alone does not change the source;
use `enqueue`/`QueueSource`. General application producer mapping remains open.

## Run the optional executable

Provision a dedicated PostgreSQL database with the [pgvector extension](https://github.com/pgvector/pgvector).
As database administrator, run `CREATE EXTENSION vector;`. Use a provisioning
credential for `init`, a writer for `ingest`, and a SELECT-only database credential
for `serve`. The read server never creates the schema or writes knowledge rows.

From `examples/standalone-tags-api`:

```sh
go build -o /tmp/knowledge-mcp ./cmd/knowledge-mcp
export KNOWLEDGE_DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/knowledge?sslmode=disable'
export KNOWLEDGE_SCOPE='hyrule-docs'
/tmp/knowledge-mcp init

export KNOWLEDGE_SCOPE='hyrule-docs'
export KNOWLEDGE_EMBED_URL='http://127.0.0.1:11434/api/embed'
export KNOWLEDGE_EMBED_MODEL='your-pinned-embedding-model'
export KNOWLEDGE_EMBED_DIMENSIONS='768'
export KNOWLEDGE_DOCUMENT_ROOT='/absolute/path/to/hyrule'
export KNOWLEDGE_MANIFEST='/absolute/path/to/manifest.json'
/tmp/knowledge-mcp ingest
```

The model and dimensions are examples of deployment configuration, not defaults.
Provision the chosen model in Ollama first. The executable uses its
[`/api/embed` API](https://docs.ollama.com/api/embed), with truncation disabled,
timeouts, bounded responses and dimension/nonzero/finite vector validation.
It never downloads a model. Use the same pinned model artifact and dimension in
ingestion and search. A model change requires reingesting the complete corpus;
different model names are not mixed in search. Updating weights under the same
name is not detected automatically. Documents are sent to the configured endpoint.

Example manifest (replace the revision with the indexed Git commit and the
subject with the actual signed JWT `sub`):

```json
[
  {
    "id": "architecture-decisions",
    "source": "website/docs/reference/architecture-decisions.md",
    "revision": "INDEXED_GIT_COMMIT",
    "subjects": ["alice"]
  }
]
```

For serving, retain the database, scope and embedding settings and configure:

```sh
export KNOWLEDGE_PUBLIC_KEY_FILE='/absolute/path/to/public.pem'
export KNOWLEDGE_KEY_ID='provisioned-key-id'
export KNOWLEDGE_ISSUER='https://your-issuer.example'
export KNOWLEDGE_AUDIENCE='hyrule-knowledge'
export KNOWLEDGE_TOKEN_FILE='/absolute/path/to/owner.jwt'
export KNOWLEDGE_AUDIT_FILE='/absolute/path/to/knowledge-audit.sqlite'
/tmp/knowledge-mcp serve
```

Configure the MCP client to launch that binary with argument `serve` and these
environment variables. Standard output belongs exclusively to MCP. The token
file is reread per operation; replacement revokes the next request. Use one
process per local owner, as in the [tags MCP pilot](mcp-pilot.md). No network MCP
endpoint, OAuth flow or desktop-client compatibility is implied. Credentials
stay outside tool arguments and audit records. SQL and provider errors are sanitized.

Tool inputs are `{"query":"How does Hyrule authorize MCP tools?"}` and
`{"id":"CHUNK_ID_FROM_SEARCH"}`. Retrieved text is untrusted context; it cannot
change identity, policy or available tools. The caller's model produces answers
and citations from the returned references.

## Validation and evolution

The integration suite exercises real PostgreSQL/pgvector, idempotent ingestion,
Unicode chunking, transaction rollback, stale-chunk removal, ACL replacement,
subject/scope isolation, model separation, and SDK list/search/fetch. The command
suite ingests a manifest and uses an actual stdio subprocess; an Ollama-shaped
HTTP fixture makes these tests deterministic. This verifies plumbing and isolation,
**not semantic quality of a real embedding model**. A separate
[local EmbeddingGemma evaluation](knowledge-evaluation.md) now records real-model
results: 10/10 Hit@5, MRR@5 0.883 versus lexical 0.753, on ten curated questions.

Run against a disposable database, whose credential can create the extension:

```sh
cd examples/standalone-tags-api
KNOWLEDGE_TEST_DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/disposable?sslmode=disable' \
  go test -race ./internal/knowledge ./cmd/knowledge-mcp -count=1
```

Before adoption, evaluate real-model Recall@5 against lexical search using at
least these questions: which backends support transactional outbox; why dryrun
is not a sandbox; how OpenAPI PATCH handles nested required fields; how JWT tool
permissions work; and how Postgres stores resource documents. Record expected
source sections, latency and embedding/indexing costs. No general quality gain or production acceptance is claimed, and no document
is automatically ingested at startup.

`Store` separates atomic document replacement, authorized search and authorized
fetch from MCP and embeddings. A future Qdrant implementation must preserve those
semantics. Adopting its official MCP server is a separate integration decision:
identity, document ACLs and fail-closed audit must still be enforced. This pilot
uses exact cosine search; approximate indexes and hybrid retrieval need their own
measurements. See [versioned source/outbox](knowledge-sync.md) for the new local pipeline and schema upgrade; general production producer wiring remains open.

Example corpus manifest and evaluation questions are checked in under `examples/standalone-tags-api/knowledge/`; replace subject/revision placeholders before ingesting.

Optional `KNOWLEDGE_EMBED_DOCUMENT_PREFIX` and `KNOWLEDGE_EMBED_QUERY_PREFIX` support asymmetric models. The storage contract includes these settings; see [prompts and reingestion](knowledge-evaluation.md#task-prompts-and-reingestion).
