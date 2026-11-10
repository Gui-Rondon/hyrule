[English](../testkit.md)

# Testar um recurso

`internal/testkit` monta o que um teste de recurso precisa: um Mongo
isolado, um cliente HTTP em processo e tokens de teste. Testes que
precisam de Mongo pulam sozinhos sem `MONGO_TEST_URI` (ex.:
`mongodb://localhost:27018/?directConnection=true`, o `make env-up`).

## Subir o serviço inteiro

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
res := &evecompat.Resource{
	Name:  "items",
	Store: testkit.Store(t, "items"), // mongodb.NewStore(testkit.Mongo(t).Collection("items"))
	Schema: evecompat.Schema{"name": {Type: evecompat.TypeString}},
}
svc := evecompat.NewService(res, tx) // tx: ver mongodb.TransactorFor
```

`internal/behaviors/events/conditional_test.go` e
`internal/outbox/outbox_test.go` seguem esse padrão para testar hooks e o
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
os dois formatos de claim que `internal/platform/auth` aceita.
