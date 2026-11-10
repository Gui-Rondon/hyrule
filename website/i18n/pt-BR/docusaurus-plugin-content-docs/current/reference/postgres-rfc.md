---
sidebar_position: 9
---

# RFC: um backend Postgres

Esta página é um documento de design, não uma referência de feature: ela
registra as decisões abertas para um segundo backend, além do Mongo, e
uma recomendação pra cada uma — não algo já construído. Hoje existe um
esqueleto (`internal/platform/postgres`: um tipo no formato de `Store`,
com seu próprio tipo de filtro, provado que compila e segue o mesmo
padrão de `internal/platform/mongodb`) e nada ligado a `rest` ainda.
Trate cada "recomendado" abaixo como ponto de partida pra discussão, não
como decisão já tomada; os dois pontos marcados como abertos estão
explicitamente não decididos.

## Por que isso existe

[`store.md`](store.md) e [`architecture.md`](architecture.md) já dizem,
explicitamente, que a portabilidade de storage de hoje é a *interface*
`Store`, não a linguagem de query nem a semântica de erro: os métodos de
`rest.Store` são tipados em `bson.M`/`bson.D`, e a tradução de erro de
`rest.Service` (`queryError`, `duplicateKey` em
`internal/rest/service.go`) checa tipos do driver Mongo direto
(`mongo.IsDuplicateKeyError`, códigos de `mongo.ServerError`,
`mongo.IsTimeout`). Isso foi uma escolha deliberada pra primeira rodada,
não um descuido — esta RFC é a "próxima rodada" que essas páginas
apontam.

## 1. A forma de um `Store` do Postgres

**Decidido.** `internal/platform/postgres.Store` não tenta satisfazer
`rest.Store` — essa interface é Mongo-shaped de propósito. Em vez disso
tem seu próprio método-set, inspirado nele (`Find`, `CountDocuments`,
`FindOne`, `InsertMany`, `UpdateOne`, `ReplaceOne`, `DeleteOne`), com
tipos nativos de Postgres pro filtro/update em vez de
`bson.M`/`bson.D`. Isso espelha como `mongodb.Store` já "satisfaz
estruturalmente" `rest.Store` sem importar `rest` — o mesmo padrão, um
pacote diferente, com seus próprios tipos.

Explicitamente **não** perseguido: uma AST de filtro compartilhada que
tanto `mongodb.Store` quanto `postgres.Store` compilam a partir dela.
`where=` continua sendo a linguagem de query do Mongo como está; um
recurso em Postgres define sua própria convenção de filtro, no seu
próprio tempo — não uma tradução de operadores Mongo pra SQL.

## 2. Como um recurso de fato usaria isso — aberto

Esta é a pergunta que a separação de interface do ponto 1 não responde:
`rest.Resource.Store` é tipado na interface `Store` Mongo-shaped hoje,
então um `Store` do Postgres não entra no lugar por troca simples. Dois
caminhos, nenhum escolhido ainda:

- **Contornar `rest` inteiramente (custo menor, ponto de partida
  recomendado).** Um recurso em Postgres fala com
  `platform/postgres.Store` direto do seu próprio handler HTTP, do
  jeito que `internal/outbox` já fala com `*mongo.Collection` direto, de
  propósito, contornando `rest.Store` (`architecture.md`: "Storage é
  uma decisão separada da de eventos"). Não ganha nada da máquina do
  `rest` de graça — sem `where=`, sem paginação/links/etags estilo Eve
  — e define sua própria convenção de query string.
- **Generificar `rest.Store`/`Service`/`Resource` (corte maior, não
  tentado ainda).** Generics do Go (`Store[F, U any]`) poderiam deixar
  `rest` continuar dirigindo paginação/links/etags pros dois backends,
  cada backend fornecendo seu próprio tipo de filtro `F` e de update
  `U`. Isso toca a maior parte de `internal/rest/service.go` e força
  uma decisão sobre o que mais precisa virar plugável por backend —
  começando pelo ponto 3 abaixo, já que `queryError`/`duplicateKey` não
  podem continuar hardcoded em tipos de erro do Mongo quando um segundo
  backend for real.

## 3. Erros

`queryError`/`duplicateKey` de `internal/rest/service.go` checam tipos
do driver Mongo direto hoje. Um equivalente em Postgres checaria
`*pgconn.PgError`:

| Mongo hoje | Equivalente Postgres | Significado |
|---|---|---|
| `mongo.IsDuplicateKeyError` | `Code == "23505"` (`unique_violation`) | → 409 no insert, 400 no update/replace, igual ao `duplicateKey` de hoje |
| códigos de erro `2`, `9`, `51091`, `51108` (`clientQueryErrors`) | `22P02` (`invalid_text_representation`), `42601` (`syntax_error`) e similares | → 400, causado pelo cliente |
| `mongo.IsTimeout` / `context.DeadlineExceeded` | timeout de statement/context | → 503, igual a hoje |

Não perseguido ainda: uma interface de classificação de erro
compartilhada entre backends. O ponto 2 acima precisa se resolver
primeiro — não há um lugar único pra pendurar isso até então.

## 4. Índices

O formato `IndexSpec`/`indices.conf` de `internal/platform/mongodb`
(direções `1`, `-1`, `text`, `hashed`, `2dsphere`; opções `unique`,
`sparse`, `name=`, `ttl=`) é Mongo-shaped do início ao fim — ver
[Performance de query hoje](store.md#performance-de-query-hoje). Um
equivalente em Postgres precisa do seu próprio formato, não um reuso
deste parser: *tipo* de índice (`btree` padrão, `gin` pra
`jsonb`/array/full-text, `gist` pra tipos geométricos/de intervalo,
`brin` pra tabelas grandes e naturalmente ordenadas) em vez de direção,
mais índices parciais e de expressão, que não têm equivalente no Mongo.
Sem `ttl` nativo — Postgres precisa de cron job ou trigger pra expiração.

## 5. Forma dos dados — aberto, sem recomendação ainda

Mongo guarda documentos sem schema; `rest.Doc` é `map[string]any` em
todo `rest`/`declarative`. Postgres é relacional, e há dois caminhos
genuinamente diferentes até lá, registrados aqui sem recomendação:

- **Colunar.** O `rest.Schema`/YAML declarativo de um recurso
  precisaria gerar DDL de tabela real (e migrations). Ganha tipos
  nativos e indexação — o motivo real de usar Postgres — mas abre mão
  da suposição de `Doc` sem schema que hoje está embutida em
  `rest`/`internal/declarative`.
- **Uma única coluna `jsonb`.** Tradução quase zero — `Doc =
  map[string]any` mapeia pra `jsonb` quase direto — mas enfraquece o
  motivo de usar Postgres: você estaria comprando principalmente
  garantias transacionais/relacionais, não colunas tipadas ou indexação
  de verdade.

## 6. Agregações — não é uma lacuna de paridade

Diferente do Mongo, Postgres não precisa de uma válvula de agregação
como a que `GET /categories/tree` do `categories-api` precisa hoje (ver
[`store.md`](store.md#pontos-que-ainda-usam-mongo-direto-de-propósito)):
joins, window functions e CTEs são SQL nativo. Registrado aqui pra não
ser confundido com uma feature faltando depois — é uma diferença de
natureza, não algo que o suporte a Postgres ainda deve.

## 7. Driver

**Recomendado:** [`github.com/jackc/pgx/v5`](https://github.com/jackc/pgx)
(`pgxpool` pro pooling). Segue o padrão já existente no projeto de driver
direto e idiomático sem ORM no meio (`mongo-driver/v2`, `go-redis/v9`), e
`pgconn.PgError` dá os códigos de erro do ponto 3 nativamente, sem uma
segunda camada de parsing.

## O que existe hoje

- `internal/platform/postgres.Store`: o método-set acima, sobre
  `pgxpool.Pool`, com um filtro só de igualdade (`map[string]any` lido
  como `coluna: valor`, sem operadores) — real, mas intencionalmente
  mínimo; não é o lugar de descanso do design de filtro do ponto 1, só o
  suficiente pra provar que a forma compila e tem onde crescer.
- `internal/platform/postgres.IsDuplicateKeyError`/`IsTimeout`: as duas
  checagens da tabela do ponto 3, espelhando os helpers de mesmo nome do
  `mongo-driver`.
- Um serviço `postgres` no `docker-compose.yml`, junto de `mongo`,
  `redis` e `pubsub`, pros testes do esqueleto rodarem contra um banco
  real, não um mock.
- Nenhuma mudança em `internal/rest` nem em nenhum recurso existente —
  é um pacote novo e isolado; nada acima carrega risco de regressão pro
  que já está em produção hoje.
