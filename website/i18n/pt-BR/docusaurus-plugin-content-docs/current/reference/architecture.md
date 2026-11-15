---
sidebar_position: 8
---

# Arquitetura: as camadas e por que elas existem

```
services (ou um YAML, via cmd/hyrule-serve)
    ↓
behaviors   (audit, searchsync, events — hooks reutilizáveis)
    ↓
rest   (Resource, Schema, Service, API, Adapter — o contrato HTTP)
    ↓
platform    (mongodb, postgres, config, auth, search, pubsub, redis, health, server)
```

Cada camada só conhece a de baixo. `platform` nunca importa `rest`
nem `behaviors`, mesmo quando isso pouparia uma linha — é assim que
`platform/mongodb.Store` consegue satisfazer
`rest.Store` sem o pacote `mongodb` saber que `rest` existe (a
interface usa só tipos de `bson`/stdlib nos métodos, não structs
próprias de `rest`; ver [`docs/store.md`](store.md)). Inverter essa
direção — `platform` importando `rest` — criaria uma dependência
estrutural de um módulo de baixo nível para um de alto nível mesmo sem um
ciclo de import de verdade, e confundiria qualquer leitor futuro.

Um `behaviors/*` (ex.: `events`, que liga `outbox` a `rest.Hooks`)
pode depender tanto de `rest` quanto de `platform` — ele existe
justamente pra ser a cola entre os dois.

Uma exceção, de propósito: `restpostgres` é o único pacote com
permissão pra importar tanto `rest` quanto `platform/postgres` — ele é
a ponte, não uma camada nova, implementando `rest.Adapter` (ver
[`adapter.md`](adapter.md)) do mesmo jeito que `mongoAdapter` faz
dentro do próprio `rest` pro `platform/mongodb`. Uma `Resource` escolhe
um dos dois definindo `Adapter` (`restpostgres.NewAdapter(...)`) ou
`Store` (`platform/mongodb.NewStore(...)`, que `rest` embrulha num
`mongoAdapter` automaticamente) — nunca os dois.

## Decisões que moldam o framework

- **Storage é uma decisão separada da de eventos.** `outbox`
  grava na mesma transação Mongo do write — uma escolha própria, não uma
  consequência inevitável do Mongo. Se o storage de um recurso mudar, o
  outbox pode seguir o banco (fica mais simples) ou ficar como está (se a
  transação do novo banco não cobrir o outbox do jeito que a do Mongo
  cobre) — ver [`docs/outbox.md`](outbox.md).
- **Portabilidade de storage é a interface, não a linguagem de query.**
  `rest.Store` continuou exatamente `bson.M`/`bson.D`-tipado, no
  formato do Mongo, nunca generificado (ADR-0002) — um segundo backend
  de verdade precisou de um seam diferente na frente dele, não de uma
  nova implementação de `Store`: `rest.Adapter` (ver
  [`adapter.md`](adapter.md)), com `restpostgres` implementando ele
  sobre `platform/postgres` pra primeira resource real com Postgres
  (`examples/blog-apis/services/comments-api`). Cada backend continua
  com sua própria linguagem de query e semântica de erro — `where=`/
  `sort=` continuam `bson.M`/`bson.D` pro Mongo e um `postgres.Query`
  compilado por `platform/postgres` pro Postgres, nunca uma AST de
  filtro compartilhada.
- **O YAML cobre dado, não código.** `declarative` expressa tudo
  que já é plano em `rest.Schema`/`Field`/`Resource`; `Field.Check`,
  `Resource.Validate`, `Resource.Datasource` e todos os `Hooks` carregam
  comportamento arbitrário e ficam de fora de propósito — ver
  [`docs/resources-yaml.md`](resources-yaml.md).
