---
sidebar_position: 1
---

# Introdução

O hyrule é um framework Go para criar microsserviços e testar seus fluxos com
rapidez. Você descreve um **recurso** — seus campos, regras de validação e
métodos HTTP disponíveis — e obtém uma API REST pronta para funcionar. Na prática,
isso inclui:

- **CRUD completo, validação e paginação** a partir de um schema, sem escrever
  handlers para cada operação.
- **Controle de concorrência otimista integrado**, com ETags e `If-Match`.
- **Mongo ou Postgres com o mesmo contrato HTTP**. Um recurso escrito em Go
  escolhe o backend por meio de `rest.Adapter`, mantendo CRUD, validação, ETags
  e consultas. Veja a [referência de Adapter](/docs/reference/adapter).
- **Outbox transacional**: a gravação e o evento correspondente são confirmados
  na mesma transação. Ou ambos são persistidos, ou nenhum é. Não há uma tarefa
  separada cujo resultado possa divergir do que foi gravado.
- **Dry run**: o serviço recebe tráfego real, mas todas as transações de escrita
  são revertidas. Assim, você exercita fluxos completos, inclusive aqueles com
  várias gravações e eventos de outbox, sem simular o armazenamento.
- **`testkit`**: um banco isolado e um cliente HTTP por teste, sem precisar subir
  o serviço em uma porta ou criar mocks do armazenamento.

Mongo e Postgres oferecem inicialização em Go, outbox transacional e dry run.
O carregamento por YAML, a geração de projetos e o provisionamento de bancos
pelo testkit ainda são específicos do Mongo. No Postgres, o registro é feito
em Go, com um pool real isolado e os mesmos utilitários HTTP de teste. O guia
[Integração com Mongo e Postgres](/docs/reference/backends) apresenta configuração,
programas executáveis e testes para os dois bancos.

Há duas formas de descrever um recurso. Em Go:

```go
res := &rest.Resource{
	Name:  "tags",
	Store: env.Store("tags"),
	Schema: rest.Schema{
		"name": {Type: rest.TypeString, Required: true},
		"slug": {Type: rest.TypeString, Required: true, Unique: true},
	},
}
env.API.Register(res)
```

Ou, nos casos mais comuns, em um arquivo YAML, sem escrever Go:

```yaml
resources:
  - name: tags
    collection: tags
    schema:
      name: {type: string, required: true}
      slug: {type: string, required: true, unique: true}
```

```bash
RESOURCES_FILE=resources.yaml go run ./cmd/hyrule-serve
```

As duas formas registram o mesmo tipo `Resource`. A proposta é começar pela
definição dos dados e usar Go nas partes que precisam de lógica própria.

:::info[O que você vai aprender]
- O que é um `Resource` e o que a interface `Store` desacopla
- Como relacionar recursos e em que momento os hooks são executados
- Como a outbox transacional mantém a gravação e o registro de eventos na mesma
  transação, fazendo o evento nascer da própria alteração dos dados
- Como testar um recurso sem subir um serviço completo
- Quando faz mais sentido usar Go em vez de YAML
:::

## O exemplo usado neste guia

O guia usa o **blog-apis**, exemplo incluído no framework. `authors-api` e
`tags-api` oferecem CRUD; `categories-api` organiza uma hierarquia; e `posts-api`
reúne relações, hooks e um fluxo de publicação com outbox. Já `comments-api`
demonstra Postgres com a mesma inicialização e publicação transacional no
Pub/Sub. Os trechos apresentados vêm do exemplo ou do próprio framework.

Se preferir explorar o código diretamente, acesse
[`examples/blog-apis`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/blog-apis).
Para seguir o passo a passo, continue em [Primeiros passos](./quick-start.md).

:::tip[Retomando]
- O hyrule transforma a definição de um recurso em uma API com CRUD completo.
- Recursos podem ser definidos em Go ou YAML, com o mesmo contrato de API.
- O blog-apis será o exemplo ao longo do guia, com um conceito de cada vez.
:::
