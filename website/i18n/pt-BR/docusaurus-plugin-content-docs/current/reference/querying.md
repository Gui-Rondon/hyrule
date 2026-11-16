---
sidebar_position: 2
---

# Consultas: `where=`, `sort`, `projection`

As rotas de listagem leem `where`, `sort`, `projection`, `page` e
`max_results` dos parâmetros da URL, seguindo o contrato do Eve. Isso vale
para a coleção e para rotas personalizadas com `Service.WriteList` ou
`WriteListFallback`. O processamento fica em `rest/query.go`, nas funções
`ParseListQuery`, `parseWhere` e `ParseSort`. Esta página descreve as sintaxes
aceitas pelo código atual.

## `where=`: a linguagem de consulta do Mongo {/* #where-a-linguagem-de-query-do-mongo-não-uma-dsl-própria */}

`where=` é um objeto JSON, decodificado e passado para o `Store` quase sem
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
padrão do próprio Eve (`MONGO_QUERY_BLACKLIST`) também bloqueia
`$regex`; um recurso que quer esse comportamento define isso
explicitamente em `QueryDenylist`. Um operador negado em qualquer parte
do filtro falha a requisição inteira com `400`, como `ErrDeniedOperator`.

JSON malformado também é `400` — não há recuperação parcial.

### O que fica fora de escopo

`where=` chega em `Store.Find`/`FindOne`/`CountDocuments`, que recebem um
*filtro* do MongoDB, não um pipeline de agregação — não há como expressar
`$lookup`, `$group` ou `$facet` pelos parâmetros da URL. Um recurso que precisa
disso lê direto do driver, contornando `Store` só para essa consulta — ver
`GET /categories/tree` do `categories-api`
(`examples/blog-apis/services/categories-api/categories.go`) e
[`store.md`](store.md#pontos-que-ainda-usam-mongo-direto-de-propósito).

## `sort`

Três sintaxes, as mesmas que o código cliente do Eve historicamente
mandava:

| Sintaxe | Exemplo |
|---|---|
| Lista separada por vírgula, `-` para descendente | `sort=-_published,title` |
| Lista JSON/Python de pares | `sort=[("_published", -1), ("title", 1)]` |
| Dict JSON/Python | `sort={"_published": -1, "title": 1}` (a ordem do dicionário é preservada) |

Uma lista vazia (`[]`) ou um dicionário vazio (`{}`) desabilita a ordenação. Qualquer outra
coisa que não bate com uma das três formas é `400`.

No Postgres, `sort=_id` e `sort=-_id` ordenam pela coluna UUID física.
O adapter continua retornando `_id` como UUID; a conversão textual usada
na leitura da linha não determina a ordenação. Isso permite usar a chave
primária, mas filtros e offsets altos ainda podem favorecer scan e Sort.
Nenhuma ordenação padrão é adicionada.

## `projection`

Um objeto JSON passado diretamente como projeção do Mongo
(`{"title": 1, "body": 0}`) — sem validação além de ser JSON válido.

## Como o filtro do servidor se combina com o do cliente

O `Datasource.Filter` de um recurso, e qualquer lookup que uma rota
personalizada passe para `WriteList`/`WriteListFallback`, são combinados com
o `where=` do cliente via `$and` — nunca um merge simples, então o
cliente não consegue sobrescrever uma chave que o servidor já definiu:

```go
q.Filter = bson.M{"$and": bson.A{clientWhere, serverLookup}}
```

O `where=` do cliente é sempre avaliado; ele restringe o filtro do
servidor, nunca o substitui.

## Opções de paginação

`page` e `max_results` seguem `Resource.Pagination` (`Default`, `Limit`,
`Disabled`) com os mesmos casos de borda do Eve: um `max_results`
não-numérico ou não-positivo usa o valor padrão, `max_results` é limitado a
`Limit`, `page` usa seu valor absoluto e um `page` inválido é ignorado
(cai para `1`). Com `Pagination.Disabled`, o padrão é `0` (sem limite),
não há teto, e `page` nem é lido — todo documento que bate vem na mesma
resposta.

`show_deleted` (presença, não o valor) inclui documentos marcados como excluídos num
recurso com `SoftDelete` — convenção do próprio Eve.

## Em um backend relacional

Em um recurso Postgres, `restpostgres.ParseWhere` decodifica `where=` em um
`postgres.Query`. `platform/postgres.CompileWhere` compila os operadores
`$eq`, `$ne`, `$gt`, `$gte`, `$lt`, `$lte`, `$in`, `$nin`, `$exists`, `$has`,
`$hasSome`, `$hasEvery`, `$not`, `$and` e `$or` para a coluna `jsonb` do documento,
com os tipos `Kind` declarados pelo schema. Veja [Adapter](adapter.md) para
registrar um recurso com Postgres.

`CompileWhere` é específico para cláusulas SQL `WHERE`: comparações positivas
podem produzir SQL NULL, tratado como não correspondência. A DSL `$not` usa
`IS NOT TRUE` para preservar ausência/JSON null e negações aninhadas. Não
concatene `NOT` ao SQL retornado; use a DSL. `CompileFilter` continua disponível
com seu booleano normalizado e comportamento anterior. Os dois compartilham
validação e parâmetros vinculados, mantendo campos e valores parametrizados.

Esse caminho permite que filtros seletivos virem `Index Cond` em planos
personalizados e, nos cenários medidos, em `auto`. Planos genéricos com nome de
campo parametrizado ainda podem não aproveitar o índice de expressão. Para
índices sparse, peça presença explícita, por exemplo:
`{"slug":{"$eq":"slug-15001","$exists":true}}`. Em `CompileWhere`, `$exists`
positivo no nível superior expõe `doc ? $N`, permitindo provar o predicado
parcial em planos personalizados. Igualdade sozinha não expõe esse predicado.
O campo continua parametrizado. Nenhuma guarda é acrescentada implicitamente
às comparações; as declarações de índices e configurações do planejador de consultas são iguais.

Isso é uma coisa diferente, de nível mais alto, que o
`platform/postgres.Filter` abaixo, que continua só igualdade de
propósito (`coluna: valor`, combinados com AND, um valor `nil`
significando `IS NULL`), sem `$gt`/`$in`/denylist para falar — é para o
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

## Otimize as consultas

Nada do que foi dito acima torna uma consulta rápida por si só — ver
[Desempenho das consultas](store.md#performance-de-query-hoje) para entender como
indexação (e suas diferenças Postgres-vs-Mongo) entra nessa.
