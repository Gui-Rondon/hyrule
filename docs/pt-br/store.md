[English](../store.md)

# A interface `Store`

`evecompat.Resource.Store` (`internal/evecompat/store.go`) é a única porta
de entrada para dados que `evecompat.Service` usa — `Find`, `CountDocuments`,
`FindOne`, `InsertMany`, `UpdateOne`, `ReplaceOne`, `DeleteOne`. Nenhum
`*mongo.Collection` aparece em `internal/evecompat/service.go`.

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

A única implementação hoje é `internal/platform/mongodb.Store`, um
embrulho fino sobre `*mongo.Collection`:

```go
store := mongodb.NewStore(db.Collection("tags"))
res := &evecompat.Resource{Store: store, ...}
```

Em um serviço (`app.Env`), use `env.Store("nome-da-colecao")` em vez de
montar o wrapper na mão.

## O que isso desacopla — e o que não desacopla

Esta interface decide **quem fala com o banco**, não **em que linguagem a
query é escrita**: `filter`/`sort`/`update` continuam `bson.M`/`bson.D`, e
os erros que `evecompat` verifica diretamente (`mongo.ErrNoDocuments`,
`mongo.IsDuplicateKeyError`, timeouts) continuam no formato do driver
Mongo. Isso foi uma decisão explícita: a portabilidade de storage desta
rodada é a interface, não a linguagem de query nem a semântica de erro.

Na prática isso quer dizer:

- Trocar a *implementação* de `Store` (outro client Mongo, uma camada de
  instrumentação, um cache) é direto: satisfaça a interface.
- Trocar de Mongo para outro banco exige mais que implementar `Store`: as
  queries (`bson.M`) e os erros checados em `internal/evecompat/service.go`
  (`queryError`, `duplicateKey`) ainda assumem Mongo. Um segundo backend
  real é a próxima rodada natural, não algo que esta interface já entrega.

## Pontos que ainda usam Mongo direto, de propósito

`internal/outbox` grava na coleção `outbox` com `*mongo.Collection` direto
— é uma decisão própria (ver [`docs/outbox.md`](outbox.md)), separada da
decisão de storage de um `Resource`. Um recurso que precisa de uma query
que `Store` não expõe (ex.: `categories-api`'s `GET /categories/tree`, em
`examples/blog-apis/services/categories-api/categories.go`) usa `Store`
mesmo assim — `Find`/`FindOne` já bastam para isso — mas um agregador
complexo ainda exigiria estender a interface ou cair para o driver.
