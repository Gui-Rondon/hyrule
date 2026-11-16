---
sidebar_position: 4
---

# Relações e hooks

`authors-api` e `tags-api` podem funcionar sozinhas. A `posts-api` reúne
referências a autor, categoria e tags, além de executar ações nas gravações,
como registrar quem alterou o documento e atualizar o índice de busca.
Nesta página, vamos conhecer esse recurso.

:::info[O que você vai aprender]
- Como um recurso referencia outro e quais verificações ficam por sua conta
- A estrutura de `rest.Hooks` e a ordem de execução
- Como combinar hooks reutilizáveis com `Hooks.Use`
:::

## Uma relação é um campo

```go
objectID := rest.Field{Type: rest.TypeObjectID}
objectIDs := func(max int) rest.Field {
	return rest.Field{Type: rest.TypeList, MaxLength: max, Default: []any{}, Items: &objectID}
}

Schema: rest.Schema{
	"author_id":   objectID,
	"category_id": rest.Field{Type: rest.TypeObjectID, Nullable: true},
	"tags":        objectIDs(50),
	// ...
},
```

`author_id` aceita um ObjectID e não aceita `null`; isso não torna sua presença
obrigatória — para isso seria necessário `Required: true`. `category_id` aceita
ObjectID ou `null`, e `tags` contém até 50 IDs. O hyrule verifica o formato desses
valores, mas **não verifica se o autor, a categoria ou as tags existem**.

Se precisar dessa verificação, use `Field.Check`: uma função executada após as
regras de validação, que pode consultar outro serviço para confirmar a
referência. Essa opção só existe em Go, pois expressa lógica de negócio, não
apenas dados. Veja a [referência de resources.yaml](/docs/reference/resources-yaml).

## Hooks participam do fluxo de gravação

A ordem é: validação → `OnPrepare` → hooks de pré-gravação
(`OnInsert`, `OnUpdate`, `OnReplace` ou `OnDelete`) e gravação dentro da transação
→ commit → hooks de pós-gravação (`OnInserted`, `OnUpdated`, `OnReplaced` ou
`OnDeleted`) fora da transação.

Antes do commit, um erro ainda pode cancelar a gravação. Depois dele, os hooks
não conseguem reverter o que foi persistido; são úteis, por exemplo, para
atualizar um índice de busca externo à transação.

A `posts-api` define o status inicial de um post com um hook `OnInsert`:

```go
func draftHook() rest.Hooks {
	return rest.Hooks{OnInsert: []func(context.Context, []rest.Doc) error{
		func(_ context.Context, docs []rest.Doc) error {
			for _, doc := range docs {
				if s, _ := doc["status"].(string); s == "" {
					doc["status"] = "draft"
				}
			}
			return nil
		},
	}}
}
```

O hook altera o documento diretamente e retorna apenas um erro, quando houver.
Ele é executado a cada inserção, para cada documento do lote, e preenche `status`
somente quando o cliente não informou um valor.

## Hooks reutilizáveis

Para não repetir a mesma lógica em vários recursos, pacotes como
`behaviors/audit` oferecem conjuntos de hooks prontos para uso:

```go
res.Hooks.Use(draftHook(), audit.Hooks())
```

`audit.Hooks()` preenche `_created_user` na inserção e `_updated_user` nas
gravações, usando o nome de usuário do token da requisição. A mesma chamada
funciona em `authors-api`, `tags-api` e `posts-api`.

`Hooks.Use` acrescenta os hooks ao recurso na ordem informada, permitindo
combinar vários comportamentos:

```go
res.Hooks.Use(draftHook(), audit.Hooks(), events.Hooks(box), events.ConditionalHooks(box, cond))
```

Os dois últimos conjuntos, `events.Hooks` e `events.ConditionalHooks`, registram
eventos na outbox transacional. É o assunto da próxima página.

:::tip[Retomando]
- Uma relação usa campos ObjectID ou listas de ObjectID. Para verificar se o
  destino existe, acrescente uma validação com `Field.Check`.
- `Required` exige a presença de um campo; `Nullable` controla a aceitação de
  `null`. São regras diferentes.
- Hooks têm ordem de execução definida. Os de pré-gravação podem cancelar a
  transação; os de pós-gravação não podem revertê-la.
- Os pacotes `behaviors/*` oferecem hooks reutilizáveis, combinados com
  `Hooks.Use`.
- A seguir: [Outbox transacional](./the-outbox.md).
:::
