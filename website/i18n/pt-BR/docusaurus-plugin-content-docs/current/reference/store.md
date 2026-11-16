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

A única implementação hoje é `platform/mongodb.Store`, uma
camada simples sobre `*mongo.Collection`:

```go
store := mongodb.NewStore(db.Collection("tags"))
res := &rest.Resource{Store: store, ...}
```

Em um serviço (`app.Env`), use `env.Store("nome-da-colecao")` em vez de
montar o adaptador manualmente.

`platform/postgres.Store` segue a mesma forma — `NewStore`
encapsula um cliente, `Find`/`FindOne`/... — para um banco relacional; só
não é uma implementação da interface acima, de propósito (ver
[backend Postgres](postgres-rfc.md) para entender o motivo):

```go
store := postgres.NewStore(pool, "tags")
docs, err := store.Find(ctx, postgres.Filter{"status": "draft"}, 0, 10, nil, nil)
```

O `platform/postgres.Store` é uma camada de nível mais baixo.
`restpostgres.NewAdapter` o combina com um `Transactor` e um schema para
implementar `rest.Adapter`. É esse adapter que um recurso Postgres registra,
em vez de `Store`; veja a [referência de Adapter](adapter.md).

## O que isso desacopla — e o que não desacopla

Esta interface decide **quem fala com o banco**, não **em que linguagem a
consulta é escrita**: `filter`/`sort`/`update` continuam `bson.M`/`bson.D`, e
os erros que `rest` verifica diretamente (`mongo.ErrNoDocuments`,
`mongo.IsDuplicateKeyError`, timeouts) continuam no formato do driver
Mongo. Isso foi uma decisão explícita: a portabilidade de armazenamento descrita aqui é a interface, não a linguagem de consulta nem a semântica de erro.

Na prática isso quer dizer:

- Trocar a *implementação* de `Store` (outro client Mongo, uma camada de
  instrumentação, um cache) é direto: satisfaça a interface.
- Trocar de Mongo para outro banco exige mais que implementar `Store`: as
  consultas (`bson.M`) e os erros checados em `rest/service.go`
  assumiam Mongo até o `rest.Adapter` (ver [`adapter.md`](adapter.md))
  dar a cada backend sua própria implementação disso.
  `platform/postgres.Store` (seu próprio tipo no formato de `Store`,
  não uma implementação desta interface — ver
  [backend Postgres](postgres-rfc.md) para entender o motivo) continua sendo a
  peça de nível mais baixo sobre a qual `restpostgres.postgresAdapter`
  é construído, não algo que uma `Resource` vê direto.

## Desempenho das consultas {/* #performance-de-query-hoje */}

`where=` (ver [`querying.md`](querying.md) para sintaxe do filtro em si)
não tem custo exposto nem `explain()` — a única alavanca é o índice
declarado para coleção que ele atinge. Índices são declarados por serviço
num arquivo `indices.conf`, ou adicionados/sobrescritos via a chave de
config `mongo.indexes`, e construídos no processo de inicialização por
`platform/mongodb.EnsureIndexes` (`mongo.indexes_mode`: `create`
constrói, `check` só registra no log o que falta, `off` ignora tudo isso):

```
# coleção     chaves                             opções
settings      product:1                          unique
articles      classification_id:1,_updated:-1    name=by_classification
articles      friendly_title:1                   unique sparse
sessions      created:1                          ttl=3600
```

As chaves são `campo:direção`, direção sendo `1`, `-1`, `text`, `hashed`
ou `2dsphere`; as opções são `unique`, `sparse`, `name=`, `ttl=`
(segundos). Um filtro `where=` sem índice correspondente degenera num
scan completo da coleção — não há retorno do planejador de consultas hoje, então
declarar o índice certo é toda a história de otimização de um recurso em
Mongo.

Isso é específico do Mongo do início ao fim (as direções de índice acima, o
`ttl=`, o próprio formato de `indices.conf`) e não vale como está para o
Postgres: o índice padrão do Postgres é btree, com `gin` para colunas
`jsonb`/array/full-text, `gist` para tipos geométricos/de intervalo e
`brin` para tabelas grandes e naturalmente ordenadas; não há `ttl` nativo
(precisa de cron ou trigger), e `EXPLAIN ANALYZE` dá um plano de consulta de
verdade onde o `where=` do Mongo não dá nenhum.
`platform/postgres.EnsureIndexes`/`IndexSpec` é o equivalente para o
Postgres — formato próprio, deliberadamente mais estreito (sem TTL/
text/hashed/2dsphere), chamado explicitamente por quem configura a
tabela de um recurso, mesmo precedente do `EnsureFunctions` (ver
[`adapter.md`](adapter.md#construindo-uma-resource-sobre-postgres)).

### Índices Postgres explícitos para ordenação tipada

`IndexField.Mode` tem `postgres.IndexFieldLegacy` como valor padrão: o DDL
JSONB existente é preservado, incluindo o utilitário seguro de datetime. A ordem
JSONB difere da ordenação tipada do `restpostgres`, especialmente para nulls
e misturas de tipos JSON. Para indexar a expressão escalar atual, opte por campo:

```go
postgres.IndexSpec{
    Table: "items", Name: "items_rank_typed_v1",
    Fields: []postgres.IndexField{{
        Field: "rank", Kind: postgres.KindInteger,
        Mode: postgres.IndexFieldTypedSort,
    }},
}
```

Isso gera `((doc->>'rank')::int8)`. Os tipos suportados são `KindString`
(`text`), `KindInteger` (`int8`), `KindNumber` (`numeric`) e `KindBoolean`
(`boolean`). Índices compostos e `Desc` são suportados; valem os padrões de
NULLS e collation do banco. Para páginas estáveis entre empates, declare um
termo de desempate na ordenação. Consultas, parâmetros vinculados e semântica não mudam.
Datetime, array/object, tipos/modos desconhecidos e índices tipados com
`Unique` ou `Sparse` são rejeitados. Caminhos aninhados e metacampos estão
fora desse modo.

Audite os dados antes da criação e valide futuras escritas: casts inválidos
impedem criar o índice e rejeitam escritas depois. JSON null e campos ausentes
viram SQL NULL; uma string numérica pode gerar o mesmo valor de um número JSON.
Cada índice extra tem custo de armazenamento e escrita. Use um **nome novo**
explícito; `EnsureIndexes`/`CREATE IF NOT EXISTS` não migram definições antigas
nem provam equivalência do DDL de um índice existente com o mesmo nome.

Campos parametrizados ainda podem impedir uso do índice em planos genéricos.
Um índice de sort não transforma filtros JSONB em condições tipadas de índice,
e offsets profundos ainda podem exigir Sort. O teste de validação manual/local
`make postgres-ha-typed-sort-test POSTGRES_HA_PORT=5540` usa `EnsureIndexes`
real e o SELECT capturado de `restpostgres.Find`, verifica planos auto/custom
e registra controles generic/offset profundo. Artefatos ficam em
`.local/postgres-typed-sort/`. Não provisiona infraestrutura nem é habilitado
no GitHub Actions. A adoção inicial fica nas fixtures do teste de validação, sem novo índice
ou migração de comments-api.

## Pontos que ainda usam Mongo direto, de propósito

`outbox` grava na coleção `outbox` com `*mongo.Collection` direto
— é uma decisão própria (ver [`docs/outbox.md`](outbox.md)), separada da
decisão de armazenamento de um `Resource`. Um recurso que precisa de uma consulta
que `Store` não expõe (ex.: `GET /categories/tree` da `categories-api`, em
`examples/blog-apis/services/categories-api/categories.go`) usa `Store`
mesmo assim — `Find`/`FindOne` já bastam para isso — mas um agregador
complexo ainda exigiria estender a interface ou cair para o driver.

O pacote Mongo outbox permanece específico do backend; `outboxpostgres` fornece a contraparte transacional Postgres. Veja [integração](backends.md) e [outbox](outbox.md).
