---
sidebar_position: 6
---

# Testes

Até aqui, usamos `curl` para exercitar a API. Agora vamos transformar essas
verificações em testes Go com `testkit`, o pacote usado pelos exemplos e pela
suíte do framework.

:::info[O que você vai aprender]
- Como inicializar um serviço com MongoDB isolado
- Como verificar o documento persistido, além da resposta HTTP
- Como testar um hook sem usar HTTP
:::

## Inicialize o serviço

`testkit.Service` monta o `app.Spec` do recurso — o mesmo passado a `app.Main`
no `main.go` — com um banco temporário e devolve um cliente HTTP em memória:

```go
func newService(t *testing.T) *env {
	keys := testkit.NewKeys(t)
	client, db := testkit.Service(t, spec(), cfg, keys.Authenticator(t))
	client.Header.Set("Authorization", "Basic: "+keys.TokenWith(t, map[string]any{"preferred_username": "writer@example.com"}))
	return &env{c: client, db: db}
}
```

Esse é um recorte da preparação dos testes de
`examples/blog-apis/services/posts-api/posts_test.go`.
`testkit.Service` não abre uma porta: `client.Do(method, path, body)` chama o
handler diretamente.

## Verifique o documento persistido

A resposta de uma gravação traz apenas metadados, como `_id` e `_etag`.
Para conferir os campos de negócio, consulte a coleção usando o
`*mongo.Database` que `Service` também devolve:

```go
func TestPublish(t *testing.T) {
	e := newService(t)
	id, _ := e.create(t, map[string]any{"title": "Hello", "slug": "hello"})

	res := e.c.Do("POST", "/posts/"+id+"/publish", map[string]any{})
	if res.Status != http.StatusOK {
		t.Fatalf("publish = %d %s", res.Status, res.Raw)
	}

	var doc bson.M
	oid, _ := bson.ObjectIDFromHex(id)
	e.db.Collection("posts").FindOne(context.Background(), bson.M{"_id": oid}).Decode(&doc)
	if doc["status"] != "published" {
		t.Errorf("status = %v", doc["status"])
	}
}
```

A coleção de outbox também pode ser consultada com
`e.db.Collection(outbox.Collection)`. É assim que `TestPlainPatchAlsoPublishes`
verifica se a ação de publicação e um PATCH comum geram o mesmo evento, como
vimos na [página anterior](./the-outbox.md).

## Teste um hook sem HTTP

Nem todo teste precisa inicializar o serviço completo. Nos testes de
`events.ConditionalHooks`, o pacote `behaviors/events` cria um `rest.Service`
diretamente e chama suas operações:

```go
db := testkit.Mongo(t)
tx, replicaSet, _ := mongodb.TransactorFor(context.Background(), db.Client())
if !replicaSet {
	t.Skip("Mongo is not a replica set")
}

res := &rest.Resource{
	Name:   "items",
	Store:  testkit.Store(t, "items"),
	Schema: rest.Schema{"status": {Type: rest.TypeString}},
}
res.Hooks.Use(events.ConditionalHooks(box, cond))
svc := rest.NewService(res, tx)

svc.Insert(ctx, []rest.Doc{{"status": "draft"}}, rest.WriteOptions{})
// ... svc.Patch, svc.Replace, svc.Delete — a mesma API Service dos
// endpoints personalizados, sem a camada HTTP.
```

`testkit.Store(t, "items")` equivale a
`mongodb.NewStore(testkit.Mongo(t).Collection("items"))`. É a mesma interface
`Store` de [Seu primeiro recurso](./your-first-resource.md), com um banco que
será removido ao final do teste.

Os testes que dependem de Mongo são ignorados quando `MONGO_TEST_URI` não está
definida. Para executá-los de fato, use `make env-up` e depois `make test`.

:::tip[Retomando]
- `testkit.Service` inicializa o recurso com um Mongo isolado e um cliente HTTP
  em memória, sem ocupar uma porta.
- Consulte `e.db.Collection(...)` para verificar o que foi persistido.
- Combine `testkit.Store` e `rest.NewService` para testar hooks sem HTTP.
- A seguir, [Próximos passos](./going-further.md): quando usar Go em vez de YAML
  e onde encontrar mais detalhes.
:::
