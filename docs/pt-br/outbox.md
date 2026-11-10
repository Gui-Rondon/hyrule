[English](../outbox.md)

# Outbox transacional

`internal/outbox` implementa o padrão outbox: um write guarda seus eventos
na coleção `outbox`, na mesma transação Mongo do write — os dois são
gravados juntos ou nenhum dos dois é. Depois do commit, um `Relay`
publica os eventos pendentes (pelo menos uma vez; cada mensagem carrega o
atributo `event_id` para quem consome deduplicar).

```go
box := outbox.New(env.DB)
res.Hooks.Use(events.Hooks(box))          // flush dos eventos anexados ao ctx
```

## Dois jeitos de decidir o que publicar

**`events.With`** — o código de negócio decide explicitamente, antes do
hook, que este write específico deve emitir um evento:

```go
ctx := events.With(r.Context(), outbox.Event{Topic: "posts-to-unpublish", Key: id, Body: id})
svc.Patch(ctx, lookup, changes, opts)
```

Use quando a condição não é "um campo chegou neste valor" — por exemplo
`examples/blog-apis/services/posts-api`'s `/unpublish`: "draft" também é o
status padrão de um post novo, então não dá para expressar "deixou de
estar published" só olhando o valor final.

**`events.ConditionalHooks` + `outbox.Condition`** — o próprio write
decide, sem `if` no código de negócio:

```go
cond := outbox.OnFieldChange("status", []string{"published"}, func(doc map[string]any) outbox.Event {
	return outbox.Event{Topic: "posts-to-publish", Type: "post.published", Key: doc["_id"].(bson.ObjectID).Hex()}
})
res.Hooks.Use(events.ConditionalHooks(box, cond))
```

`OnFieldChange` cobre o caso comum: dispara quando o valor efetivo do
campo (depois do write) é um dos valores-alvo e — fora de um insert —
representa uma transição real (o valor anterior era diferente). Uma vez
registrado, **qualquer** caminho de escrita que resulte nesse valor
dispara o evento, não só um endpoint dedicado:
`examples/blog-apis/services/posts-api/posts.go` usa exatamente essa
condição para `/publish`, e um `PATCH /posts/{id}` comum que também setar
`status: published` dispara o mesmo jeito
(`TestPlainPatchAlsoPublishes` em `posts_test.go`).

Os dois convivem no mesmo `Resource` — `events.Hooks` e
`events.ConditionalHooks` são bundles de hooks independentes, combinados
com `res.Hooks.Use(...)`.

## Destinos locais (sem Pub/Sub real)

`outbox.Dispatcher` roteia por tópico antes de chegar no `Publisher` real:

```go
outbox.Dispatcher{
	Local: map[string]outbox.PublisherFunc{
		"local:posts-schedule": func(ctx context.Context, m outbox.Message) error {
			// grava num hash do Redis, por exemplo — não sai pro Pub/Sub
		},
	},
	Next: &outbox.PubSub{Client: ps}, // tudo que não é "local:*" cai aqui
}
```

`examples/blog-apis/services/posts-api` usa isso no `/schedule`: o evento
de agendamento escreve num hash do Redis (pra um scheduler escanear) na
mesma transação do write, sem nunca tocar o Pub/Sub.

## `Outbox.InTransaction`

`Outbox.Add` recusa gravar fora de uma transação (`ErrNoTransaction`) —
checagem feita por `Outbox.InTransaction func(ctx) bool`, que `outbox.New`
preenche com o checker do driver Mongo (`mongo.SessionFromContext`). Não
está hardcoded dentro de `Add`: um backend diferente de Mongo trocaria
esse campo por seu próprio jeito de saber "estou numa transação".
`Outbox.AllowNoTransaction` desliga a checagem (só para Mongo standalone
em desenvolvimento — nunca em produção).
