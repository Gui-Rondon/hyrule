---
sidebar_position: 7
---

# Arquitetura: as camadas e por que elas existem

```
services (ou um YAML, via cmd/hyrule-serve)
    ↓
behaviors   (audit, searchsync, events — hooks reutilizáveis)
    ↓
evecompat   (Resource, Schema, Service, API, Store — o contrato HTTP)
    ↓
platform    (mongodb, config, auth, search, pubsub, redis, health, server)
```

Cada camada só conhece a de baixo. `platform` nunca importa `evecompat`
nem `behaviors`, mesmo quando isso pouparia uma linha — é assim que
`internal/platform/mongodb.Store` consegue satisfazer
`evecompat.Store` sem o pacote `mongodb` saber que `evecompat` existe (a
interface usa só tipos de `bson`/stdlib nos métodos, não structs
próprias de `evecompat`; ver [`docs/store.md`](store.md)). Inverter essa
direção — `platform` importando `evecompat` — criaria uma dependência
estrutural de um módulo de baixo nível para um de alto nível mesmo sem um
ciclo de import de verdade, e confundiria qualquer leitor futuro.

Um `behaviors/*` (ex.: `events`, que liga `outbox` a `evecompat.Hooks`)
pode depender tanto de `evecompat` quanto de `platform` — ele existe
justamente pra ser a cola entre os dois.

## Decisões que moldam o resto

- **Storage é uma decisão separada da de eventos.** `internal/outbox`
  grava na mesma transação Mongo do write — uma escolha própria, não uma
  consequência inevitável do Mongo. Se o storage de um recurso mudar, o
  outbox pode seguir o banco (fica mais simples) ou ficar como está (se a
  transação do novo banco não cobrir o outbox do jeito que a do Mongo
  cobre) — ver [`docs/outbox.md`](outbox.md).
- **Portabilidade de storage é a interface, não a linguagem de query.**
  `evecompat.Store` desacopla o cliente concreto; filtros continuam
  `bson.M` e os erros que `evecompat` verifica diretamente continuam no
  formato do driver Mongo. Um segundo backend de verdade é a próxima
  rodada, não algo que a interface já entrega — ver
  [`docs/store.md`](store.md).
- **O YAML cobre dado, não código.** `internal/declarative` expressa tudo
  que já é plano em `evecompat.Schema`/`Field`/`Resource`; `Field.Check`,
  `Resource.Validate`, `Resource.Datasource` e todos os `Hooks` carregam
  comportamento arbitrário e ficam de fora de propósito — ver
  [`docs/resources-yaml.md`](resources-yaml.md).
