---
sidebar_position: 7
---

# Observabilidade

## Métricas

`internal/platform/metrics` expõe em `/metrics` a duração e o tamanho da
resposta de toda requisição, por método, rota e status, além das métricas
padrão do runtime Go (`app.Build` envolve o handler com
`metrics.New(spec.Name, dryRun).Handler(...)` — por fora do prefixo de
URL, se houver um, e medindo toda requisição, probes incluídas).

A rota é o padrão do mux (`GET /posts/{id}`, não o path de verdade), então
um path nunca se torna um valor de label sem limite. O label `dry_run`
(`true`/`false`) separa o tráfego em [dry run](dry-run.md) do resto nas
mesmas séries — sem precisar de um serviço paralelo só pra comparar.

## `/status` e `/healthcheck`

Todo serviço expõe os dois, registrados automaticamente por `app.Build`
para a coleção (e, se houver mais de um recurso, também sob
`/<recurso>/status` e `/<recurso>/healthcheck` do primeiro recurso em
diante): `/status` é só liveness (sempre 200 enquanto o processo roda, lê
`internal/platform/health.Status`); `/healthcheck` roda os `health.Checker`
registrados com `env.AddCheck` — a verificação do Mongo vem de fábrica, e
cada integração externa (Elasticsearch, Pub/Sub, Redis...) soma a sua
checagem própria.

Em dry run, uma falha do `WriteGuard` (ver [`docs/dry-run.md`](dry-run.md))
deixa `/healthcheck` permanentemente vermelho sem derrubar `/status` — os
probes de liveness/readiness de um orquestrador normalmente usam rotas
diferentes para isso, de propósito: um `/healthcheck` vermelho não deve
tirar o pod da rotação, só acender um alerta.
