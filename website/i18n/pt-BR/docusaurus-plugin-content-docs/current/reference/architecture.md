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
justamente para integrar os dois.

Uma exceção, de propósito: `restpostgres` é o único pacote com
permissão para importar tanto `rest` quanto `platform/postgres` — ele é
a ponte, não uma camada nova, implementando `rest.Adapter` (ver
[`adapter.md`](adapter.md)) do mesmo jeito que `mongoAdapter` faz
dentro do próprio `rest` para o `platform/mongodb`. Uma `Resource` escolhe
um dos dois definindo `Adapter` (`restpostgres.NewAdapter(...)`) ou
`Store` (`platform/mongodb.NewStore(...)`, que `rest` encapsula num
`mongoAdapter` automaticamente) — nunca os dois.

## Decisões que moldam o framework

- **Armazenamento e eventos compartilham a transação local.** Recursos Mongo usam
  `outbox`; Postgres usa `outboxpostgres` com mesmo pool/contexto. Hooks dependem
  do contrato Adder, mas a atomicidade não atravessa dois bancos nem publicadores
  externos. Veja [outbox](outbox.md).
- **A interface de armazenamento não unifica a linguagem de consulta.**
  `rest.Store` mantém os tipos `bson.M` e `bson.D` do Mongo, sem generics
  (ADR-0002). Para integrar Postgres, foi criado `rest.Adapter`, implementado
  por `restpostgres` sobre `platform/postgres`; veja [Adapter](adapter.md).
  Cada backend mantém sua linguagem de consulta e classificação de erros:
  Mongo usa os tipos BSON; Postgres usa `postgres.Query`, compilado por
  `platform/postgres`. Não há uma árvore de filtros compartilhada.
- **O YAML descreve dados, não código.** `declarative` representa as configurações de
  `rest.Schema`, `Field` e `Resource`. `Field.Check`,
  `Resource.Validate`, `Resource.Datasource` e todos os `Hooks` carregam
  comportamento arbitrário e ficam de fora de propósito — ver
  [`docs/resources-yaml.md`](resources-yaml.md).

Veja [integração dos dois backends](backends.md) e [revisão/evidências](integration-review.md) para o estado atual, bootstrap e limites.
