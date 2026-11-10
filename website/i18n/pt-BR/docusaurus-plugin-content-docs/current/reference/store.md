---
sidebar_position: 1
---

# A interface `Store`

`rest.Resource.Store` (`rest/store.go`) é a única porta
de entrada para dados que `rest.Service` usa — `Find`, `CountDocuments`,
`FindOne`, `InsertMany`, `UpdateOne`, `ReplaceOne`, `DeleteOne`. Nenhum
`*mongo.Collection` aparece em `rest/service.go`.

```go
type Store interface {
	Find(ctx context.Context, filter bson.M, skip, limit int64, sort bson.D, projection bson.M) ([]Doc, error)
	CountDocuments(ctx context.Context, filter bson.M) (int64, error)
	FindOne(ctx context.Context, filter bson.M, projection bson.M) (Doc, error)
	InsertMany(ctx context.Context, docs []any) error
	UpdateOne(ctx context.Context, filter, update bson.M) (matched int64, err error)
	ReplaceOne(ctx context.Context, filter bson.M, replacement any) (matched int64, err error)
	DeleteOne(ctx context.Context, filter bson.M) (deleted int64, err error)
}
```

A única implementação hoje é `platform/mongodb.Store`, um
embrulho fino sobre `*mongo.Collection`:

```go
store := mongodb.NewStore(db.Collection("tags"))
res := &rest.Resource{Store: store, ...}
```

Em um serviço (`app.Env`), use `env.Store("nome-da-colecao")` em vez de
montar o wrapper na mão.

`platform/postgres.Store` segue a mesma forma — `NewStore`
embrulha um client, `Find`/`FindOne`/... — pra um banco relacional; só
não é uma implementação da interface acima (ver
[backend Postgres](postgres-rfc.md) pro porquê), então nada aqui ainda
liga isso a um `rest.Resource`:

```go
store := postgres.NewStore(pool, "tags")
docs, err := store.Find(ctx, postgres.Filter{"status": "draft"}, 0, 10, nil, nil)
```

## O que isso desacopla — e o que não desacopla

Esta interface decide **quem fala com o banco**, não **em que linguagem a
query é escrita**: `filter`/`sort`/`update` continuam `bson.M`/`bson.D`, e
os erros que `rest` verifica diretamente (`mongo.ErrNoDocuments`,
`mongo.IsDuplicateKeyError`, timeouts) continuam no formato do driver
Mongo. Isso foi uma decisão explícita: a portabilidade de storage desta
rodada é a interface, não a linguagem de query nem a semântica de erro.

Na prática isso quer dizer:

- Trocar a *implementação* de `Store` (outro client Mongo, uma camada de
  instrumentação, um cache) é direto: satisfaça a interface.
- Trocar de Mongo para outro banco exige mais que implementar `Store`: as
  queries (`bson.M`) e os erros checados em `rest/service.go`
  (`queryError`, `duplicateKey`) ainda assumem Mongo.
  `platform/postgres` já tem um primeiro esqueleto (seu próprio
  tipo no formato de `Store`, não uma implementação desta interface — ver
  [backend Postgres](postgres-rfc.md) pro porquê e pro que ainda está
  aberto), mas nenhum recurso está ligado a ele ainda.

## Performance de query hoje

`where=` (ver [`querying.md`](querying.md) pra sintaxe do filtro em si)
não tem custo exposto nem `explain()` — a única alavanca é o índice
declarado pra coleção que ele atinge. Índices são declarados por serviço
num arquivo `indices.conf`, ou adicionados/sobrescritos via a chave de
config `mongo.indexes`, e construídos no boot por
`platform/mongodb.EnsureIndexes` (`mongo.indexes_mode`: `create`
constrói, `check` só loga o que falta, `off` ignora tudo isso):

```
# collection  keys                               options
settings      product:1                          unique
articles      classification_id:1,_updated:-1    name=by_classification
articles      friendly_title:1                   unique sparse
sessions      created:1                          ttl=3600
```

As chaves são `campo:direção`, direção sendo `1`, `-1`, `text`, `hashed`
ou `2dsphere`; as opções são `unique`, `sparse`, `name=`, `ttl=`
(segundos). Um filtro `where=` sem índice correspondente degenera num
scan completo da coleção — não há retorno do query planner hoje, então
declarar o índice certo é toda a história de otimização de um recurso em
Mongo.

Isso é Mongo-shaped do início ao fim (as direções de índice acima, o
`ttl=`, o próprio formato de `indices.conf`) e não vai valer como está
pro Postgres: o índice padrão do Postgres é btree, com `gin` pra colunas
`jsonb`/array/full-text, `gist` pra tipos geométricos/de intervalo e
`brin` pra tabelas grandes e naturalmente ordenadas; não há `ttl` nativo
(precisa de cron ou trigger), e `EXPLAIN ANALYZE` dá um plano de query de
verdade onde o `where=` do Mongo não dá nenhum. Ver
[backend Postgres](postgres-rfc.md) pra onde isso está indo.

## Pontos que ainda usam Mongo direto, de propósito

`outbox` grava na coleção `outbox` com `*mongo.Collection` direto
— é uma decisão própria (ver [`docs/outbox.md`](outbox.md)), separada da
decisão de storage de um `Resource`. Um recurso que precisa de uma query
que `Store` não expõe (ex.: `categories-api`'s `GET /categories/tree`, em
`examples/blog-apis/services/categories-api/categories.go`) usa `Store`
mesmo assim — `Find`/`FindOne` já bastam para isso — mas um agregador
complexo ainda exigiria estender a interface ou cair para o driver.
