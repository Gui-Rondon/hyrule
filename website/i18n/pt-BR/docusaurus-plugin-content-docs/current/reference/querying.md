---
sidebar_position: 2
---

# Queries: `where=`, `sort`, `projection`

Toda rota de listagem (a URL raiz do recurso, e qualquer rota de lista
customizada feita com `Service.WriteList`/`WriteListFallback`) lê
`where`, `sort`, `projection`, `page` e `max_results` da query string, do
mesmo jeito que o Eve. O parsing vive em `rest/query.go`
(`ParseListQuery`/`parseWhere`/`ParseSort`) — esta página documenta esse
parsing, não uma linguagem de query aspiracional: o que vem abaixo é
exatamente o que o código aceita hoje.

## `where=`: a linguagem de query do Mongo, não uma DSL própria

`where=` é um objeto JSON, decodificado e passado pro `Store` quase sem
mudança — **é** um documento de filtro do MongoDB, operadores incluídos
(`$gt`, `$in`, `$exists`, ...), não uma abstração sobre um:

```
GET /posts?where={"status":"published"}&sort=-_published&max_results=10
```

Duas conversões acontecem na entrada (`mongotize`, espelhando o
`_mongotize` do Eve):

- Uma string no formato `DateFormat` (`Mon, 02 Jan 2006 15:04:05 GMT`, o
  `DATE_FORMAT` do Eve) se torna um `time.Time`.
- Uma string de 24 caracteres hex se torna um `ObjectID` do BSON — a
  menos que o recurso defina `QueryObjectIDAsString` (o
  `IDS_ARE_HEX_AND_OBJECTID_COMPATIBLE` do Eve), caso em que fica como
  string.

```
GET /posts?where={"category_id":"507f1f77bcf86cd799439011"}
```

bate com documentos em que `category_id` é igual a esse `ObjectID`, não
à string.

### O que é rejeitado

Antes da conversão, toda chave do JSON decodificado (recursivamente,
inclusive dentro de arrays) é checada contra `Resource.QueryDenylist`.
`nil` significa `DefaultQueryDenylist`: `$where`, `$function`,
`$accumulator` — os operadores que rodam JavaScript no servidor. O
default do próprio Eve (`MONGO_QUERY_BLACKLIST`) também bloqueia
`$regex`; um recurso que quer esse comportamento define isso
explicitamente em `QueryDenylist`. Um operador negado em qualquer parte
do filtro falha a requisição inteira com `400`, como `ErrDeniedOperator`.

JSON malformado também é `400` — não há recuperação parcial.

### O que fica fora de escopo

`where=` chega em `Store.Find`/`FindOne`/`CountDocuments`, que recebem um
*filtro* do MongoDB, não um pipeline de agregação — não há como expressar
`$lookup`, `$group` ou `$facet` pela query string. Um recurso que precisa
disso lê direto do driver, contornando `Store` só pra essa query — ver
`GET /categories/tree` do `categories-api`
(`examples/blog-apis/services/categories-api/categories.go`) e
[`store.md`](store.md#pontos-que-ainda-usam-mongo-direto-de-propósito).

## `sort`

Três sintaxes, as mesmas que o código cliente do Eve historicamente
mandava:

| Sintaxe | Exemplo |
|---|---|
| Lista separada por vírgula, `-` pra descendente | `sort=-_published,title` |
| Lista JSON/Python de pares | `sort=[("_published", -1), ("title", 1)]` |
| Dict JSON/Python | `sort={"_published": -1, "title": 1}` (a ordem do dict é preservada) |

Uma lista (`[]`) ou dict (`{}`) vazio significa sem sort. Qualquer outra
coisa que não bate com uma das três formas é `400`.

## `projection`

Um objeto JSON, passado direto como a projection do Mongo
(`{"title": 1, "body": 0}`) — sem validação além de ser JSON válido.

## Como o filtro do servidor se combina com o do cliente

O `Datasource.Filter` de um recurso, e qualquer lookup que uma rota
customizada passe pra `WriteList`/`WriteListFallback`, são combinados com
o `where=` do cliente via `$and` — nunca um merge simples, então o
cliente não consegue sobrescrever uma chave que o servidor já definiu:

```go
q.Filter = bson.M{"$and": bson.A{clientWhere, serverLookup}}
```

O `where=` do cliente é sempre avaliado; ele restringe o filtro do
servidor, nunca o substitui.

## Botões de paginação que interagem com isso

`page` e `max_results` seguem `Resource.Pagination` (`Default`, `Limit`,
`Disabled`) com os mesmos casos de borda do Eve: um `max_results`
não-numérico ou não-positivo cai pro default, `max_results` é limitado a
`Limit`, `page` usa seu valor absoluto e um `page` inválido é ignorado
(cai pra `1`). Com `Pagination.Disabled`, o default é `0` (sem limite),
não há teto, e `page` nem é lido — todo documento que bate vem na mesma
resposta.

`show_deleted` (presença, não o valor) inclui documentos soft-deleted num
recurso com `SoftDelete` — convenção do próprio Eve.

## Num backend relacional

Um `where=` contra uma `rest.Resource` com Postgres carrega os
operadores sim — o `ParseWhere` de `restpostgres` decodifica ele num
`postgres.Query`, e `platform/postgres.CompileWhere` compila o mesmo
conjunto de operadores `$eq/$ne/$gt/$gte/$lt/$lte/$in/$nin/$exists/
$has/$hasSome/$hasEvery/$not/$and/$or` contra a coluna `jsonb` do
documento da resource, pra qualquer campo que o `Schema` da resource
declare um `Kind`. Ver [`adapter.md`](adapter.md) pra como uma
resource com Postgres é registrada.

`CompileWhere` é específico para cláusulas SQL `WHERE`: comparações positivas
podem produzir SQL NULL, tratado como não correspondência. A DSL `$not` usa
`IS NOT TRUE` para preservar ausência/JSON null e negações aninhadas. Não
concatene `NOT` ao SQL retornado; use a DSL. `CompileFilter` continua disponível
com seu booleano normalizado e comportamento anterior. Os dois compartilham
validação e bindings, mantendo campos e valores parametrizados.

Esse caminho permite que filtros seletivos virem `Index Cond` em planos
customizados e, nos cenários medidos, em `auto`. Planos genéricos com nome de
campo parametrizado ainda podem não aproveitar o índice de expressão. Isso
não altera índices sparse, ordenação ou configurações globais do planner.

Isso é uma coisa diferente, de nível mais alto, que o
`platform/postgres.Filter` abaixo, que continua só igualdade de
propósito (`coluna: valor`, combinados com AND, um valor `nil`
significando `IS NULL`), sem `$gt`/`$in`/denylist pra falar — é pro
CRUD de colunas planas do próprio `Store`, a peça sobre a qual
`restpostgres` é construído, não algo que uma requisição `where=`
chega a alcançar direto:

```go
docs, err := store.Find(ctx, postgres.Filter{"status": "published"}, 0, 10,
	[]postgres.SortField{{Column: "published_at", Desc: true}}, nil)
```

Mesma forma do skip/limit/sort/colunas do `Find` acima, uma linguagem de
filtro mais estreita de propósito: uma mais rica existe, só que como o
`postgres.Query` acima, nunca como uma extensão deste tipo `Filter` em
si.

## Otimizando essas queries

Nada do que foi dito acima torna uma query rápida por si só — ver
[Performance de query hoje](store.md#performance-de-query-hoje) pra como
indexação (e suas diferenças Postgres-vs-Mongo) entra nessa.
