---
sidebar_position: 5
---

# Outbox transacional

Ao publicar um post, a `posts-api` precisa persistir duas coisas juntas: o
status `published` no documento e um evento anunciando a publicação. Se apenas
a gravação ocorrer, os consumidores não saberão da mudança. Se apenas o evento
for enviado, eles reagirão a uma publicação que não foi confirmada. A outbox
transacional garante que o documento e o registro do evento sejam persistidos
juntos, ou que nenhum dos dois seja.

:::info[O que você vai aprender]
- Por que publicar o evento depois do commit não basta
- Como associar explicitamente um evento à gravação com `events.With`
- Como `outbox.OnFieldChange` e `events.ConditionalHooks` fazem uma alteração
  gerar um evento automaticamente
:::

## O risco de publicar depois de gravar

Gravar o documento e, em seguida, chamar o cliente Pub/Sub deixa uma janela de
falha: o processo pode encerrar entre as duas operações. Com a outbox, o evento
é gravado na coleção `outbox` **na mesma transação MongoDB** que altera o
documento. Depois do commit, um `Relay` lê a coleção e publica no Pub/Sub,
tentando novamente em caso de falha.

```go
box := outbox.New(env.DB)
res.Hooks.Use(events.Hooks(box))
```

`events.Hooks(box)` é um conjunto de hooks, como os da
[página anterior](./relations-and-hooks.md). Ele grava em `box` os eventos
associados ao contexto da operação, dentro da transação.

## Associe um evento explicitamente

Quando a decisão depende da lógica de negócio, o código associa o evento ao
contexto antes da gravação:

```go
ctx := events.With(r.Context(), outbox.Event{Topic: "posts-to-unpublish", Key: id, Body: id})
e.svc.Patch(ctx, lookup, changes, opts)
```

É assim que a ação de despublicação da `posts-api` funciona. O status `draft`
não basta para identificar essa ação, pois também é usado em posts novos.
O endpoint sabe que se trata de uma despublicação e registra essa intenção.

## Faça a alteração gerar o evento

Na publicação, a própria mudança de estado é suficiente: sempre que `status`
passar a `"published"`, deve ser gerado um evento `post.published`, tanto pelo
endpoint de publicação quanto por um PATCH comum. Essa regra pode ser expressa
com `outbox.Condition`:

```go
func publishCondition(topic string) outbox.Condition {
	return outbox.OnFieldChange("status", []string{"published"}, func(doc map[string]any) outbox.Event {
		id, _ := doc["_id"].(bson.ObjectID)
		return outbox.Event{Topic: topic, Type: "post.published", Key: id.Hex(), Body: doc}
	})
}

res.Hooks.Use(events.Hooks(box), events.ConditionalHooks(box, publishCondition(toPublish)))
```

`OnFieldChange` gera o evento quando o novo valor de `status` é `"published"` e
o anterior era diferente. Isso inclui criar um documento já publicado ou mudar
de `draft` ou `scheduled` para `published`. Enviar `published` novamente para um
documento que já está publicado não gera outro evento por essa condição.

Com o hook registrado, o endpoint de publicação cuida apenas da alteração:

```go
func (e *endpoints) publish(w http.ResponseWriter, r *http.Request) {
	_, lookup, _, ok := e.target(w, r)
	if !ok {
		return
	}
	changes := rest.Doc{"status": "published", "_published": time.Now().UTC().Format(rest.DateFormat)}
	if _, err := e.svc.Patch(r.Context(), lookup, changes, rest.WriteOptions{SkipConcurrency: true}); err != nil {
		rest.WriteError(w, r, err)
		return
	}
	e.respond(w, r, lookup)
}
```

A função não chama `box.Add`. O evento é consequência da gravação, então um
`PATCH /posts/{id}` que define `status: published` gera o mesmo evento.
O teste `TestPlainPatchAlsoPublishes`, em
`examples/blog-apis/services/posts-api/posts_test.go`, verifica esse comportamento.

<details>
<summary>Saiba mais: destinos locais além do Pub/Sub</summary>

A ação de agendamento da `posts-api` grava um evento de outbox na mesma transação
Mongo que altera o status. Após o commit, o relay atualiza um hash no Redis,
permitindo que um agendador consulte Redis em vez de varrer Mongo.
O Redis não participa da transação original.

`outbox.Dispatcher` encaminha tópicos como `"local:posts-schedule"` para essa
gravação local, em vez de publicá-los no Pub/Sub. Veja a
[referência de outbox](/docs/reference/outbox) para os detalhes.

</details>

:::tip[Retomando]
- `outbox.New` e `events.Hooks` persistem os eventos na transação da gravação;
  um `Relay` os entrega depois.
- Use `events.With` quando a condição depende da lógica de negócio.
- Use `outbox.OnFieldChange` com `events.ConditionalHooks` quando a condição
  pode ser identificada pela mudança do documento.
- Em [Testes](./testing.md), você vai verificar esses hooks sem abrir uma porta
  HTTP.
:::

## O mesmo fluxo com Postgres

Use `outboxpostgres.Outbox` com o pool do adapter e `env.PostgresTx`.
`events.Hooks`, `events.ConditionalHooks` e os publicadores são compartilhados.
O relay Postgres permite workers com `SKIP LOCKED` e reenvio de eventos esgotados,
mas não garante ordenação por `Key` entre workers. Veja as
[integrações com Mongo e Postgres](/docs/reference/outbox) e o exemplo executável
da comments-api. O contrato dos eventos continua o mesmo.
