---
sidebar_position: 7
---

# Observability

## Metrics

`internal/platform/metrics` exposes at `/metrics` the duration and
response size of every request, by method, route and status, plus the
standard Go runtime metrics (`app.Build` wraps the handler with
`metrics.New(spec.Name, dryRun).Handler(...)` — outside the URL prefix,
if there is one, and measuring every request, probes included).

The route is the mux pattern (`GET /posts/{id}`, not the real path), so a
path never becomes an unbounded label value. The `dry_run` label
(`true`/`false`) separates [dry-run](dry-run.md) traffic from the rest in
the same series — no need for a parallel service just to compare.

## `/status` and `/healthcheck`

Every service exposes both, registered automatically by `app.Build` for
the collection (and, if there's more than one resource, also under
`/<resource>/status` and `/<resource>/healthcheck` from the first
resource on): `/status` is liveness only (always 200 while the process
runs, reads `internal/platform/health.Status`); `/healthcheck` runs the
registered `health.Checker`s — the Mongo check comes for free, and every
external integration (Elasticsearch, Pub/Sub, Redis...) adds its own.

In dry run, a `WriteGuard` failure (see [`dry-run.md`](dry-run.md)) turns
`/healthcheck` permanently red without taking `/status` down with it —
liveness/readiness probes in an orchestrator usually use different
routes for exactly this reason: a red `/healthcheck` shouldn't pull the
pod out of rotation, only raise an alert.
