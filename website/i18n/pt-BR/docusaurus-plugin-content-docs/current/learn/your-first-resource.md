---
sidebar_position: 3
---

# Seu primeiro recurso

Em [Primeiros passos](./quick-start.md), você criou um recurso a partir de YAML.
Agora vamos montar um recurso em Go, usando a `authors-api` de
`examples/blog-apis`, para entender o papel de cada campo de `Resource`.

:::info[O que você vai aprender]
- O que são `rest.Resource` e `rest.Schema`
- As opções de campo mais usadas: `Required`, `Unique`, `Regex` e `Default`
- Como `ResourceMethods`, `ItemMethods` e `PublicMethods` controlam o acesso
- Como registrar o recurso em um serviço
:::

## Um recurso é um valor Go

A definição do recurso `authors` fica em um único valor `rest.Resource`: nome,
armazenamento, schema e métodos HTTP disponíveis.

```go
res := &rest.Resource{
	Name:      "authors",
	ItemTitle: "author",
	Store:     env.Store("authors"),
	Schema:    rest.Schema{ /* próxima seção */ },
}
```

`Store` é a interface pela qual esse recurso acessa o MongoDB. Ela não depende de
um `*mongo.Collection` concreto, o que permite usar um banco isolado nos testes.
A [referência de Store](/docs/reference/store) explica os limites desse
desacoplamento e como ele se relaciona com `Adapter`.
`env.Store("authors")` cria a implementação Mongo para a coleção `authors`.

## O schema, campo a campo

`rest.Schema` é um `map[string]rest.Field`. Comece pelos campos básicos:

```go
Schema: rest.Schema{
	"name": {Type: rest.TypeString, Required: true},
	"bio":  {Type: rest.TypeString},
},
```

Com `Required: true`, um `POST /authors` sem `name` recebe status 422 durante a
validação, antes da gravação. Sem `Required`, o campo é opcional.

Agora acrescente regras que dependem do valor informado:

```go
"email": {Type: rest.TypeString, Unique: true, Regex: regexp.MustCompile(`^.+\@.+\..+$`)},
```

`Unique: true` consulta a coleção antes de inserir ou atualizar, rejeitando um
segundo autor com o mesmo e-mail. `Regex` verifica o formato; neste exemplo,
apenas se o texto se parece com um e-mail, sem implementar toda a RFC 5322.

A `authors-api` reaproveita a mesma expressão regular em dois campos:

```go
var handle = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// ...
"twitter":   {Type: rest.TypeString, Regex: handle},
"instagram": {Type: rest.TypeString, Regex: handle},
```

`Field.Regex` é um `*regexp.Regexp` comum: você pode usá-lo como faria em qualquer
programa Go.

<details>
<summary>Saiba mais: o que um campo pode definir?</summary>

Há valores padrão (`Default`), listas de valores aceitos (`Allowed`), limites
de tamanho (`MinLength` e `MaxLength`), limites numéricos (`Min` e `Max`),
aceitação de nulos (`Nullable`) e controle de valores vazios (`NotEmpty` e
`AllowEmpty`). Em campos `dict` e `list`, `Schema` e `Items` validam a estrutura
interna. Um exemplo simples é o campo `enabled` da `tags-api`:

```go
"enabled": {Type: rest.TypeBoolean, Default: true},
```

Essas opções têm equivalentes em YAML. Veja a
[referência de resources.yaml](/docs/reference/resources-yaml) para a lista
completa e para as exceções que precisam de Go, como a função de validação
`Field.Check`.

</details>

## Controle os métodos e a autenticação

```go
ResourceMethods:   []string{http.MethodGet, http.MethodPost},
ItemMethods:       []string{http.MethodGet, http.MethodPatch, http.MethodPut},
PublicMethods:     rest.PublicGET,
PublicItemMethods: rest.PublicGET,
```

`ResourceMethods` se aplica à URL da coleção (`/authors`), enquanto `ItemMethods`
se aplica a um documento (`/authors/{id}`). `PublicMethods` e `PublicItemMethods`
definem quais métodos dispensam autenticação. Aqui, apenas `GET` é público:
qualquer pessoa pode consultar, mas criar ou editar exige um token.

## Registre o recurso

O recurso passa a atender requisições quando é registrado no serviço:

```go
func setup(_ context.Context, env *app.Env) error {
	res := &rest.Resource{ /* ... */ }
	env.API.Register(res)
	return nil
}

func main() {
	app.Main(app.Spec{Name: "authors-api", DefaultDB: "authors", Version: version, Setup: setup})
}
```

Essa é a estrutura de `examples/blog-apis/services/authors-api`: uma definição
`Resource` e uma chamada a `Register`. Não há endpoints próprios; o comportamento
adicional é o hook de [auditoria](./relations-and-hooks.md). Por isso, sua
definição também pode vir de YAML, acrescentando o hook em Go antes do registro.

:::tip[Retomando]
- Um `Resource` reúne nome, `Store`, `Schema` e métodos permitidos.
- As opções de `Field` atendem às validações mais comuns sem lógica condicional
  escrita à mão.
- `ResourceMethods`, `ItemMethods` e as opções `Public*` definem os métodos
  disponíveis e quais exigem autenticação.
- A seguir, [Relações e hooks](./relations-and-hooks.md) mostra referências
  entre recursos e reações às gravações.
:::
