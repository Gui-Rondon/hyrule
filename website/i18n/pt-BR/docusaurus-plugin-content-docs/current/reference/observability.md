---
sidebar_position: 7
---

# Observabilidade

## Métricas

`platform/metrics` expõe em `/metrics` a duração e o tamanho da
resposta de toda requisição, por método, rota e status, além das métricas
padrão do runtime Go (`app.Build` envolve o handler com
`metrics.New(spec.Name, dryRun).Handler(...)` — por fora do prefixo de
URL, se houver um, e medindo toda requisição, incluindo as verificações de saúde).

A rota é o padrão do mux (`GET /posts/{id}`, não o caminho real da requisição), então
o caminho não gera valores ilimitados para o rótulo da métrica. O rótulo `dry_run`
(`true`/`false`) separa o tráfego em [dry run](dry-run.md) do resto nas
mesmas séries — sem precisar de um serviço paralelo só para comparar.

## `/status` e `/healthcheck`

Todo serviço expõe os dois, registrados automaticamente por `app.Build`
para a coleção (e, se houver mais de um recurso, também sob
`/<recurso>/status` e `/<recurso>/healthcheck` do primeiro recurso em
diante): `/status` verifica apenas se o processo está ativo (sempre 200 enquanto o processo roda, lê
`platform/health.Status`); `/healthcheck` executa os `health.Checker`
registrados com `env.AddCheck` — a verificação do Mongo vem de fábrica, e
cada integração externa (Elasticsearch, Pub/Sub, Redis...) soma a sua
checagem própria.

Em dry run, uma falha do `WriteGuard` (ver [`docs/dry-run.md`](dry-run.md))
deixa `/healthcheck` permanentemente vermelho sem derrubar `/status` — as
verificações de atividade (liveness) e prontidão (readiness) de um orquestrador normalmente usam rotas
diferentes para isso, de propósito: um `/healthcheck` vermelho não deve
tirar o pod da rotação, só acender um alerta.
