---
sidebar_position: 1.5
---

# A interface `Adapter`

`rest.Resource.Adapter` (`rest/adapter.go`) é o ponto de contato que
`rest.Service` usa pra tudo que uma requisição precisa: parsear e
combinar `where`/`sort`/`projection`, gerar e parsear ids, CRUD,
`MergePatch`, checagem de unicidade, e classificar os erros de um
backend no status HTTP que eles significam. O `Store` de
[`store.md`](store.md) — tipado em `bson.M`/`bson.D`, moldado pelo
Mongo — continua exatamente o que sempre foi; `Adapter` fica na
frente dele, não no lugar dele. Uma `Resource` com `Adapter` definido
nunca chama `Store` (`Resource.setDefaults`, `rest/resource.go`, só
constrói um `mongoAdapter` envolvendo `Store` quando `Adapter` é
`nil` — o padrão de uma resource Mongo que nunca precisou pensar
nisso).

Isso é a revisão do ADR-0002 (ver
[`architecture-decisions.md`](architecture-decisions.md)): o rework
de generics que aquele ADR adiou continua adiado; esta é a quarta
opção que ele não considerava originalmente.

```go
// rest/adapter.go
type Adapter interface {
	QueryAdapter  // where=/sort=/projection=, CombineFilter/Sort/Projection, WriteLookup, IDFilter, RetryPolicy
	IDCodec       // NewID, IDString, LookupByID, LookupID
	ErrorClassifier // IsNotFound, IsDuplicateKey, IsClientError, IsTimeout, ClassifiesWriteTimeouts

	Find(ctx context.Context, filter, sort, projection any, skip, limit int64) ([]Doc, error)
	CountDocuments(ctx context.Context, filter any) (int64, error)
	FindOne(ctx context.Context, lookup, projection any) (Doc, error)
	InsertMany(ctx context.Context, docs []Doc) error
	UpdateOne(ctx context.Context, lookup any, updated, changes Doc) (matched int64, err error)
	ReplaceOne(ctx context.Context, lookup any, replacement Doc) (matched int64, err error)
	DeleteOne(ctx context.Context, lookup any) (deleted int64, err error)

	MergePatch(original, changes Doc) (Doc, error)
	ValidatesMergedDocument() bool
	EqualsFilter(field string, value any, excludeID any) (any, error)
}
```

`QueryAdapter`/`IDCodec`/`ErrorClassifier` foram extraídas primeiro,
como três interfaces separadas e testáveis de forma independente
(espelhando o que `rest/service.go`/`query.go`/`http.go` já faziam
pro Mongo, zero mudança de comportamento); `Adapter` embute as três e
adiciona os métodos de escrita/merge/unicidade que uma `Resource`
precisa pra nunca chegar direto em `Store`. `mongoAdapter`
(`rest/mongoadapter.go`) e o `postgresAdapter` de `restpostgres`
implementam ela por completo, cada um com sua própria asserção
`var _ rest.Adapter = (*...)(nil)`.

## Mongo vs Postgres, lado a lado

| | `mongoAdapter` | `restpostgres.postgresAdapter` |
|---|---|---|
| Formato de armazenamento | um documento BSON por `_id` | uma linha: `id uuid`, `doc jsonb`, colunas `created_at`/`updated_at`/`version`/`deleted` |
| `_etag` | hash de conteúdo, computado em Go antes da escrita | a coluna `version` — `UpdateOne`/`ReplaceOne`'s `RETURNING version` confirma o que foi realmente escrito, nunca computado no cliente |
| Concorrência otimista | `_etag` comparado no filtro da escrita (CAS), uma só tentativa — `RetryPolicy()` é o `RetryOptions` zero | `version` comparado no `WHERE` (CAS), com um laço de retentativa real sob `If-Match: *` (`RetryPolicy()`/`rest.RetryOptions`, o orçamento de retentativa do Patch, seção 9.7 do plano de portabilidade) |
| `MergePatch` | merge profundo do Eve (`MERGE_NESTED_DOCUMENTS`) — um `null` literal é guardado, nunca remove uma chave | RFC 7396 JSON Merge Patch — `null` remove a chave; `rest.Resource.RequireMergePatchContentType` exige `application/merge-patch+json` quando ligado |
| `ValidatesMergedDocument()` | `false` — só o corpo do PATCH do cliente é validado | `true` — o documento mesclado é checado pelas chaves `Required` que o RFC 7396 pode ter removido (seção 20's Fase 4c) |
| Soft delete | um campo `_deleted` dentro do documento | uma coluna física `deleted`, nunca dentro de `doc` |
| Números num `where=` declarado | `json.Number` no caminho do filtro, convertido ad hoc | o `CompileFilter` de `platform/postgres` precisa do `Kind` (`KindInteger`/`KindNumber`/`KindDateTime`) de cada campo — ver [`querying.md`](querying.md) |
| Classificação de erro | `mongo.ErrNoDocuments`/`IsDuplicateKeyError`/códigos de erro do servidor | códigos SQLSTATE do Postgres (`23505` violação de unicidade, `57014`/`40P01`/`40001` transitórios) |

## Construindo uma resource sobre Postgres

`restpostgres.NewAdapter` é o que um serviço registra em vez de um
`Store`:

```go
tx := postgres.Transactor{Pool: pool}
adapter, err := restpostgres.NewAdapter(postgres.NewStore(pool, "comments"), tx, restpostgres.Options{
	Schema: schema,                        // o mesmo rest.Schema que a Resource declara
	Retry:  rest.DefaultRetryOptions(),
	// SoftDelete: true,                   // só se a Resource também for SoftDelete — nada mantém os dois sincronizados
})
res := &rest.Resource{
	Name:    "comments",
	Schema:  schema,
	Adapter: adapter,
	// sem Store
}
api := rest.New(rest.Options{Transactor: tx}) // o mesmo Transactor — as escritas do Service e o CRUD do adapter compartilham uma transação real
api.Register(res)
```

Antes da primeira requisição, a tabela precisa existir (`id uuid
primary key default gen_random_uuid()`, `doc jsonb not null default
'{}'`, `created_at`/`updated_at timestamptz`, `version bigint`,
`deleted boolean`), `platform/postgres.EnsureFunctions` precisa ter
rodado ao menos uma vez contra esse banco (as funções SQL
`hyrule_safe_*` que filtros/ordenações/índices de `KindDateTime`
dependem), e qualquer índice declarativo
(`platform/postgres.EnsureIndexes`/`IndexSpec`) precisa ser garantido
explicitamente — nada disso acontece dentro do próprio `NewAdapter`,
de propósito, o mesmo precedente que o próprio comentário de
`EnsureFunctions` cita.
[`examples/blog-apis/services/comments-api`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/blog-apis/services/comments-api)
é um exemplo completo e real fazendo exatamente isso na
inicialização — a primeira (e, até hoje, única) resource do repo
servida de ponta a ponta sobre HTTP por esse caminho, em vez de só
nos próprios testes de `restpostgres`.

Não existe suporte de `app.Main` pra uma resource Postgres ainda
(`app.Env`/`app.Spec` são bootstrap Mongo-only) — `comments-api`
conecta `platform/server`, `platform/postgres` e `restpostgres`
diretamente no próprio `main()`. Conectar Postgres no bootstrap
compartilhado dos serviços é trabalho separado, ainda não começado,
não uma lacuna do `Adapter` em si.

## O que ainda é Mongo-only

- O bootstrap completo de `app.Env`/`app.Main` (config, auth, health,
  `/status`), como acima.
- `rest.Resource.Datasource.Filter`/`DefaultSort`/`Projection` são
  tipados `bson.M`/`bson.D` — uma resource Postgres é assumida, hoje,
  como nunca os definindo (o próprio comentário de
  `restpostgres/query.go` nomeia isso explicitamente).
- Um operador de comparação (`$gt`/`$lt`/...) num filtro `where=`
  contra um campo declarado precisa que o `Kind` desse campo seja
  conhecido pelo compilador de `platform/postgres` — não existe um
  equivalente ao filtro permissivo "qualquer tipo BSON que esteja lá"
  do Mongo.
