---
sidebar_position: 26
---

# Local EmbeddingGemma evaluation

On 2026-11-16, the [knowledge pilot](knowledge-pilot.md) was evaluated with the
already cached `onnx-community/embeddinggemma-300m-ONNX`, quantized to q8,
snapshot `5090578d9565bb06545b4552f76e6bc2c93e4a66`. This small model serves the
first corpus; a larger download is not justified by this measurement.

The related `site-chat` project currently calls Gemini. Its earlier local model
experiments motivated reusing these weights. No other project configuration or
private memory was modified/indexed. The corpus contains eight public Hyrule
reference documents and 113 chunks. File hashes, model hashes and base commit
`eaacf6d` are recorded in
`examples/standalone-tags-api/knowledge/evidence/2026-11-16-embeddinggemma.json`.

## Results and limits

| Metric | EmbeddingGemma + pgvector | PostgreSQL full-text baseline |
|---|---:|---:|
| Questions with a relevant excerpt in top 5 (Hit@5) | 10/10 | 10/10 |
| Mean reciprocal rank at 5 (MRR@5) | 0.883 | 0.753 |
| Warm-model MCP search p95 | 121 ms | Not measured through the same MCP path |

Indexing took 41.9 seconds. Inference used AMD Ryzen 9 7950X3D CPU, two intra-op
threads, one inter-op thread, ONNX Runtime 1.31.1, NumPy 2.5.4 and tokenizers
0.22.2, with 768-dimensional vectors. Latency includes SDK MCP, query embedding,
SQL retrieval and committed SQLite audit; model loading/cold startup are excluded.
Do not extrapolate this desktop result to Raspberry Pi or production load.

Ten curated questions cover five subjects in English and Portuguese. A relevant
excerpt must match an expected source **and** contain any expected term. This
coarse reproducible judgment is not exhaustive relevance labeling or Recall@5.
The baseline uses PostgreSQL English full-text ranking with OR-connected query
words on the same chunks, ACL, scope and model contract. Portuguese queries
retrieve English documents. The operator baseline repeats JWT authorization and
SQL ACL directly in the evaluator; it is not another MCP tool.

Both methods find relevant excerpts. The observed benefit is ordering, not a
higher Hit@5. This small sample does not establish general superiority, answer
correctness, citation quality or production acceptance. The evaluator reports
measurements without a post-hoc pass threshold. Expand independently labeled
questions and unanswerable cases before defining acceptance thresholds.

## Reproduce with existing weights

Keep model weights outside Git and observe the [model's published terms and
prompt instructions](https://huggingface.co/onnx-community/embeddinggemma-300m-ONNX).
In an isolated Python environment, install `onnxruntime==1.31.1`, `numpy==2.5.4`
and `tokenizers==0.22.2` to reproduce this runtime. From the standalone module:

```sh
python knowledge/eval_onnx.py --snapshot /absolute/path/to/cached/snapshot
```

The helper prints its loopback URL, model ID and artifact hashes. It only serves
`/api/embed`, rejects truncation and input beyond 2,048 tokens, and downloads
nothing. This is an evaluation bridge, not production inference serving.

With a disposable pgvector database and the printed URL/model:

```sh
KNOWLEDGE_TEST_DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/disposable?sslmode=disable' \
KNOWLEDGE_EVAL_EMBED_URL='http://127.0.0.1:PRINTED_PORT/api/embed' \
KNOWLEDGE_EVAL_MODEL='embeddinggemma-300m-onnx-q8@5090578d9565bb06545b4552f76e6bc2c93e4a66' \
KNOWLEDGE_EVAL_REPORT='/tmp/hyrule-evaluation.json' \
  go test ./internal/knowledge -run '^TestRealModelEvaluation$' -count=1 -v
```

The opt-in test reads eight explicitly named repository documents, creates a
scoped test corpus and signed local identity, calls the real model through SDK
MCP with committed audit, then deletes its rows. Default CI uses deterministic
embeddings, with no model weights, API keys or private data. Source changes alter
the corpus: compare the hashes when comparing measurements. Stop the helper
and disposable database when finished.

## Task prompts and reingestion

Set these identically for ingestion and serving with EmbeddingGemma:

```sh
export KNOWLEDGE_EMBED_DOCUMENT_PREFIX='title: none | text: '
export KNOWLEDGE_EMBED_QUERY_PREFIX='task: search result | query: '
```

Both optional settings default to empty; other models may need other values.
The storage contract hashes model ID, dimensions, both prefixes and chunker
version. Incompatible settings cannot search/fetch old vectors. **Reingest data
from the previous raw-model-ID pilot**, and after any contract change. Weight
changes behind an unchanged model ID remain undetectable: pin the artifact revision.

Tests explicitly verify that a request denied `knowledge:read` never reaches the
embedding provider. Document ACL filtering follows embedding of an authorized
query. Permission changes still need reingestion; live ACL checks/outbox remain
separate work. This measurement does not complete those consistency guarantees.
