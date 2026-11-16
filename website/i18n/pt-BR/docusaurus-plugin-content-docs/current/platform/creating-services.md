---
sidebar_position: 3
---

# Crie serviços

:::info[O que você vai aprender]
- Como gerar um serviço com `hyrule service create`
- Quais arquivos são criados e como personalizá-los
- Como aplicar o [guia de aprendizado](/docs/learn/intro) ao serviço gerado
:::

## Gere o serviço

Na raiz de um projeto criado por `hyrule new`, execute:

```bash
hyrule service create articles
```

O comando procura o diretório `services/` e falha se ele não existir.
Em `services/articles/`, serão criados:

- `main.go`, com `app.Main(spec())`, a mesma inicialização apresentada em
  [Seu primeiro recurso](/docs/learn/your-first-resource).
- `resource.go`, com uma função `setup` que registra um `rest.Resource` chamado
  `articles`, um campo obrigatório `name` e os hooks de `behaviors/audit`.
- `resource_test.go`, com um teste de criação e consulta usando `testkit`, como
  na página [Testes](/docs/learn/testing).

## Personalize o recurso

O exemplo inicial é pequeno: um campo, `GET` e `POST` na coleção, `GET` e `PATCH`
no item e um comportamento de auditoria. A partir dele, você aplica o mesmo
framework apresentado no guia:

- Acrescente campos e validações ao `Schema`. Consulte
  [Seu primeiro recurso](/docs/learn/your-first-resource) ou a
  [referência de resources.yaml](/docs/reference/resources-yaml) para definições
  declarativas.
- Acrescente relações e hooks. Veja
  [Relações e hooks](/docs/learn/relations-and-hooks).
- Integre a outbox transacional. Veja
  [Outbox transacional](/docs/learn/the-outbox).

`hyrule service create` gera os arquivos iniciais para você. O código resultante
é desenvolvido da mesma forma que um serviço escrito à mão.

:::tip[Retomando]
- `hyrule service create <name>` gera `app.Main`, um `rest.Resource` e um teste
  funcionais, como ponto de partida para sua API.
- Recursos, hooks, relações e outbox funcionam da mesma forma nos serviços
  gerados e nos escritos à mão.
:::
