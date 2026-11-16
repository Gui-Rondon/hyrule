---
sidebar_position: 6
---

# Testando recursos Mongo e Postgres

`testkit` monta o que um teste de recurso precisa: um Mongo
isolado, um cliente HTTP em processo e tokens de teste. Helpers HTTP/tokens
servem para ambos; Service/Mongo/Store/DryRun provisionam Mongo. Para Postgres,
forneça um pool real isolado ao app.Build, como abaixo. Testes que
precisam de Mongo pulam sozinhos sem `MONGO_TEST_URI` (ex.:
`mongodb://localhost:27018/?directConnection=true`, o `make env-up`).

## Mongo: subindo o serviço inteiro

```go
c, db := testkit.Service(t, spec(), config.Config{}, testkit.DisabledAuth(t))

res := c.Do("POST", "/tags", map[string]any{"name": "Go", "slug": "go"})
if res.Status != http.StatusCreated {
	t.Fatalf("POST = %d %s", res.Status, res.Raw)
}
id := res.String("_id")
```

`spec()` é o `app.Spec` do serviço (o mesmo que `app.Main` usa); `Service`
chama `app.Build` numa base isolada e devolve um cliente HTTP em processo
(`c.Do`, sem subir uma porta de verdade) junto com o `*mongo.Database`
para inspeção direta quando o teste precisa olhar o documento gravado:

```go
var doc bson.M
db.Collection("tags").FindOne(ctx, bson.M{"_id": oid}).Decode(&doc)
```

Para autenticação real, troque `testkit.DisabledAuth(t)` por
`testkit.NewKeys(t).Authenticator(t)`, e assine um token com
`keys.TokenWith(t, claims)`.

## Testar o `Store` isolado, sem subir o serviço

Para testar um hook ou um `Resource` sem HTTP:

```go
res := &rest.Resource{
	Name:  "items",
	Store: testkit.Store(t, "items"), // mongodb.NewStore(testkit.Mongo(t).Collection("items"))
	Schema: rest.Schema{"name": {Type: rest.TypeString}},
}
svc := rest.NewService(res, tx) // tx: ver mongodb.TransactorFor
```

`behaviors/events/conditional_test.go` e
`outbox/outbox_test.go` seguem esse padrão para testar hooks e o
outbox sem precisar de HTTP nem de um serviço inteiro.

## Dry run

```go
dryRun := testkit.DryRun(t, spec(), cfg, authn, db) // db: o mesmo de Service, com dados já preparados
```

Builda o serviço de novo, em dry run, sobre o `*mongo.Database` que
`Service` já populou — o teste prepara os dados com o serviço real e
repete os writes nesse segundo cliente para confirmar que nada foi
persistido e que nenhuma tarefa de fundo (`env.Go`) foi iniciada (ver
[`docs/dry-run.md`](dry-run.md)).

## Elasticsearch e tokens

`testkit.NewES(t)` devolve um gravador HTTP fake (`es.Calls()` registra o
que foi indexado, sem precisar de um Elasticsearch real) — use
`es.Hosts()` na config do recurso. `testkit.NewKeys(t)` assina tokens RSA
de teste; `keys.Token(t, "username")` e `keys.TokenWith(t, claims)` cobrem
os dois formatos de claim que `platform/auth` aceita.


## Postgres: subindo o serviço inteiro

Use `POSTGRES_TEST_URI` (dev local: `postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable`).
`testkit.Service` é específico do Mongo; use `app.Build` e `testkit.NewClient`
com schema Postgres real isolado. Não reutilize schema de produção nem execute
DDL numa tabela compartilhada. O padrão requer imports de pgx/v5, pgxpool,
google/uuid, app, config, testkit e biblioteca padrão:

```go
// spec() is your Postgres app.Spec, registering table "items".
ctx := context.Background()
uri := os.Getenv("POSTGRES_TEST_URI")
if uri == "" { t.Skip("POSTGRES_TEST_URI not set") }
admin, err := pgxpool.New(ctx, uri)
if err != nil { t.Fatal(err) }
t.Cleanup(admin.Close)
schemaName := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")
quoted := pgx.Identifier{schemaName}.Sanitize()
if _, err := admin.Exec(ctx, "CREATE SCHEMA " + quoted); err != nil { t.Fatal(err) }
t.Cleanup(func() {
    cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    if _, err := admin.Exec(cleanup, "DROP SCHEMA " + quoted + " CASCADE"); err != nil { t.Error(err) }
})
cfg := admin.Config()
cfg.ConnConfig.RuntimeParams["search_path"] = schemaName
pool, err := pgxpool.NewWithConfig(ctx, cfg)
if err != nil { t.Fatal(err) }
t.Cleanup(pool.Close)
// Apply your real resource migration to this schema before Build.
handler, err := app.Build(ctx, spec(), app.Deps{
    Postgres: pool, Config: config.Config{}, Auth: testkit.DisabledAuth(t),
})
if err != nil { t.Fatal(err) }
c := testkit.NewClient(t, handler)
res := c.Do("POST", "/items", map[string]any{"name": "Go"})
if res.Status != http.StatusCreated { t.Fatalf("POST = %d %s", res.Status, res.Raw) }
```

O pool fecha antes da remoção do schema isolado. Aplique as migrations reais do
serviço, incluindo outbox quando necessário. Reutilize `env.PostgresTx` no Setup;
app.Build instala funções compartilhadas e garante `Spec.PostgresIndexes`.
Build não gerencia a vida do pool injetado nem inicia workers sem `Deps.Start`.
Use `httptest.NewServer(handler)` quando precisar de socket HTTP real. Veja os
testes concretos em `app/backend_contract_test.go` e
`examples/blog-apis/services/comments-api/migrations_test.go`.

Para dry run Postgres, chame app.Build novamente com o **mesmo pool populado**
e `Deps{Postgres: pool, DryRun: true, Auth: authn}`. Use testkit.NewClient nesse
handler, escreva e consulte pelo cliente/pool normal para verificar rollback.
Não precisa de WriteGuard Mongo. Teste rollback tanto de recurso quanto de
outbox; escritas diretas no pool e DDL de setup estão fora dessa proteção.

## Comandos de validação para ambos

```sh
make env-up
make test GOFLAGS='-count=1 -p=1'
make vet
make cover GOFLAGS=-p=1
make standalone-example-test
# Após terminar testes/cobertura, sem builds Docker concorrentes:
make e2e
```

A suíte usa MONGO_TEST_URI e POSTGRES_TEST_URI exportados pelo Makefile. Sem
eles, testes de banco pulam; execução verde sem infraestrutura não comprova
integração. Limitamos concorrência de pacotes porque há teste real com orçamento
de retry sensível à disputa de CPU. A cobertura não inclui gates HA manuais
sem habilitação explícita: veja [evidências](integration-review.md).

## Cobertura do framework

Após `make env-up`, execute `make cover GOFLAGS=-p=1`: o gate exige **95%**
do código de runtime em `app`, `behaviors`, `declarative`, `outbox`,
`outboxpostgres`, `platform`, `rest` e `restpostgres`, incluindo ambos os backends.
Todos os testes normais de `./...` executam; exemplos, CLI, ferramentas HA
manuais e `testkit` ficam fora do denominador. Não é cobertura apenas unitária:
os testes de integração usam MongoDB e PostgreSQL reais.

`make cover-all GOFLAGS=-p=1` preserva o escopo agregado anterior, com exemplos e
ferramentas, e gate de 85%. Os perfis são `.local/coverage.out` e
`.local/coverage-all.out`, respectivamente. Execute os dois sequencialmente.
Percentuais desses escopos não são comparáveis diretamente.
