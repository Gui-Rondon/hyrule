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
| Números num `where=` declarado | `json.Number` no caminho do filtro, convertido ad hoc | o `CompileWhere` de `platform/postgres` precisa do `Kind` (`KindInteger`/`KindNumber`/`KindDateTime`) de cada campo — ver [`querying.md`](querying.md) |
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

`app.Main` ganhou suporte a Postgres (`app.Spec.Backend`,
`app.Env.Postgres`/`PostgresTx`) nas Streams A/B do plano de produção.
`comments-api` usa esse bootstrap em `main.go`: configuração, wiring de
`platform/server`, `/status`/`/healthcheck` e dry run
(`postgres.Transactor.Rollback`) funcionam para serviços Postgres.

## Ordenação datetime segura por opção explícita

A ordenação datetime de documentos mantém o cast direto para `timestamptz`
por padrão, incluindo falhas de consulta com valores inválidos armazenados.
Para optar pelo helper já usado pelos filtros e índices datetime em um
adapter/resource:

```go
opts := restpostgres.Options{
    Schema: schema,
    Retry: rest.DefaultRetryOptions(),
    SafeDateTimeSort: true,
}
```

A opção substitui apenas expressões de sort datetime de campo superior por
`hyrule_safe_timestamptz(doc->>$N)`. Campos continuam como parâmetros vinculados.
Caminhos aninhados mantêm ordenação textual; metacampos e outros tipos não mudam.
É uma opção de registro em Go, não um parâmetro de query HTTP. Execute
`postgres.EnsureFunctions` no setup; `app.Build` já faz isso para Postgres.

Valores rejeitados pelo helper existente ordenam como SQL NULL, junto com campos
ausentes e JSON null: no fim em ASC, no começo em DESC. Isso inclui valores
malformados, strings vazias, datas de calendário inválidas e formatos legados
como datas sem horário, timestamps sem fuso explícito e `infinity`, que o cast
padrão pode aceitar. Audite os formatos armazenados antes de optar pelo modo.
Os valores retornados são preservados; a opção não reescreve nem valida os dados.
O helper mantém sua normalização atual pelo PostgreSQL (incluindo entradas
24:00/leap second aceitas); não é um novo validador RFC3339 estrito.
Timestamps válidos suportados preservam a ordem cronológica entre offsets
explícitos, com a precisão já usada pelo PostgreSQL. Instantes iguais e grupos
NULL exigem termo de sort único explícito para paginação estável; nenhum
desempate é acrescentado automaticamente.

Os índices datetime legados (`IndexField{Field: "published_at", Kind:
postgres.KindDateTime}`) já usam esse helper. A opção pode alinhar a consulta
a eles sem alterar o DDL. `IndexFieldTypedSort` continua rejeitando datetime.
O índice segue sendo escolha do planner, especialmente em planos genéricos
com campos parametrizados e offsets profundos. Nenhum índice ou migration
é adicionado a recursos de produção. Remover a opção restaura o cast direto e
seus erros sem alterar dados ou índices.

`make postgres-ha-datetime-sort-test POSTGRES_HA_PORT=5540` é um gate manual/local
com índices reais e SELECTs capturados do adapter. Verifica corpora de 20 mil
linhas válidas e mistas em UTC e America/Sao_Paulo, ASC/DESC, desempates compostos
explícitos e paginação: 48 planos e 24 verificações de índice sem Sort em
auto/custom. Verifica o erro padrão e a paridade de resultados no modo seguro.
Generic e offsets profundos são observacionais. Artefatos ficam em
`.local/postgres-datetime-sort/` após cleanup do schema isolado. O gate não
provisiona infraestrutura nem roda no GitHub Actions.

## Planos preparados e política explícita por pool

O padrão atual do pgx mantém statements preparados em cache. O adapter usa
bindings tanto para campos do documento quanto para valores; um plano genérico
nem sempre consegue associar os parâmetros de campo a um índice de expressão
literal. Assim, `auto` pode manter um plano ineficiente mesmo com índice
declarado. No controle local com 20 mil linhas, cinco sorts por um campo inteiro
sem índice fizeram o próximo sort por um campo inteiro indexado (mesma forma
SQL) usar Seq Scan + Sort genérico. O plano customizado usou o índice tipado
sem Sort e preservou os resultados. Isso não prevê a escolha de `auto` em todo
workload.

O operador pode optar por planos customizados através da URI **já existente**,
sem alterar SQL do compilador nem adicionar opção ao adapter:

```text
postgres://user:password@host:5432/database?sslmode=require&plan_cache_mode=force_custom_plan
```

Configure a connection string que vence a precedência:
`POSTGRES_CONNECTION_STRING`, depois `postgres.uri`, depois campos individuais
de conexão. Acrescente `&plan_cache_mode=force_custom_plan` quando já houver
query string, ou comece com `?plan_cache_mode=force_custom_plan`.
`postgres.Connect` encaminha o parâmetro no startup de cada conexão física;
não existe nova chave `postgres.plan_cache_mode`. Sem a opção na URI, vale a
configuração existente do servidor/role (normalmente `auto`).

A opção abrange **todo o pool**, incluindo transações e outros consumidores,
como escritas e consultas do outbox. Statements parametrizados têm custo de
planejamento a cada execução; o índice continua sendo escolha do planner, e
a opção não resolve índices ausentes nem filtros JSONB desalinhados. Consultas
UUID sem parâmetros ainda podem usar planos genéricos e a chave primária.
Meça planejamento, execução, buffers e latência da aplicação no workload real
antes da adoção. Para reverter, remova a opção da URI e recrie o pool.
O padrão da aplicação, transporte, bindings e índices de produção não mudam.

`make postgres-ha-plan-policy-test POSTGRES_HA_PORT=5540` é um gate manual/local:
cinco warmups pelo adapter real, sexta chamada real na mesma conexão e
`EXPLAIN EXECUTE` **do próprio statement em cache do pgx**. Doze planos comparam
`auto`, `force_custom_plan` e `force_generic_plan` para igualdade, datetime,
sort tipado e UUID. Deltas dos contadores identificam o plano explicado;
resultados devem coincidir entre políticas. Duas conexões simultâneas abertas
por `postgres.Connect` também verificam propagação da URI. As escolhas auto
continuam observacionais; índice seletivo/ordenação são exigidos em custom.
Artefatos ficam em `.local/postgres-plan-policy/` após cleanup do schema isolado.
O gate não provisiona infraestrutura nem é habilitado no GitHub Actions.

Ver [regras de planos preparados](https://www.postgresql.org/docs/16/sql-prepare.html)
e [plan_cache_mode](https://www.postgresql.org/docs/16/runtime-config-query.html#GUC-PLAN-CACHE-MODE).

## O que ainda é Mongo-only

- `rest.Resource.Datasource.Filter`/`DefaultSort`/`Projection` são
  tipados `bson.M`/`bson.D` — uma resource Postgres é assumida, hoje,
  como nunca os definindo (o próprio comentário de
  `restpostgres/query.go` nomeia isso explicitamente).
- Um operador de comparação (`$gt`/`$lt`/...) num filtro `where=`
  contra um campo declarado precisa que o `Kind` desse campo seja
  conhecido pelo compilador de `platform/postgres` — não existe um
  equivalente ao filtro permissivo "qualquer tipo BSON que esteja lá"
  do Mongo.
