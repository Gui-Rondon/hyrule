---
sidebar_position: 7
---

# Próximos passos

Você conheceu duas formas de usar o framework: um recurso definido inteiramente
em YAML em [Primeiros passos](./quick-start.md) e recursos construídos em Go nas
páginas seguintes. Agora vamos ver como escolher entre elas e como aplicar o
que você aprendeu em um projeto próprio.

:::info[O que você vai aprender]
- Quando a definição de um recurso pode ficar em YAML
- Como combinar recursos YAML e Go no mesmo processo
- Onde encontrar as opções e detalhes que não foram abordados no guia
:::

## Go ou YAML?

Uma regra prática: **se o `setup` apenas monta um `Resource` e chama `Register`,
a definição pode ser YAML no Mongo.** O registro de recursos Postgres é feito
em Go; veja o [guia de backends](/docs/reference/backends).
As definições de `authors-api` e `tags-api` são bons exemplos. Hooks como o de
auditoria podem ser acrescentados em Go ao recurso carregado do YAML.

A `posts-api` precisa de Go: seu `setup` registra endpoints de publicação e
agendamento, a condição de [outbox](./the-outbox.md) e o
[`draftHook`](./relations-and-hooks.md). `Field.Check`, `Resource.Validate` e
os hooks não fazem parte do carregador YAML porque carregam código, não apenas
configuração. É nessas partes que entra a lógica Go.

## As duas formas no mesmo processo

O carregador YAML produz um `*rest.Resource`, exatamente como um `setup` escrito
em Go. Um mesmo serviço pode registrar recursos das duas formas, ou carregar um
recurso YAML e acrescentar hooks em Go antes do registro.

O teste `TestBuiltResourceComposesWithConditionalOutbox`, em
`declarative/declarative_test.go`, faz isso: carrega o YAML e acrescenta o hook
condicional apresentado em [Outbox transacional](./the-outbox.md).

## Outros recursos do framework

- **Dry run**: execute o serviço com tráfego real e reverta todas as transações
  de escrita para validar uma versão antes de colocá-la em produção. Veja a
  [referência de dry run](/docs/reference/dry-run).
- **Observabilidade**: os serviços expõem métricas Prometheus em `/metrics` e
  verificações em `/healthcheck`. Veja
  [Observabilidade](/docs/reference/observability).
- **Arquitetura**: entenda a divisão `services → behaviors → rest → platform`
  e por que `platform` não importa `rest`. Veja
  [Arquitetura](/docs/reference/architecture).

## Continue explorando

A seção de Referência detalha campos e opções apresentados de forma resumida
neste guia. Comece por [resources.yaml](/docs/reference/resources-yaml) para
definir seu recurso, ou por [Store](/docs/reference/store) para entender os
limites da interface de armazenamento.

Em [`examples/blog-apis`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/blog-apis),
você encontra os trechos do guia em seu contexto original, com testes.

Se pretende reunir vários serviços no mesmo repositório, continue em
[Plataforma](/docs/platform/intro). Os comandos `hyrule new` e
`hyrule service create` geram a estrutura de projeto, o `Makefile` e os recursos
que você aprendeu a escrever à mão.

:::tip[Retomando]
- YAML atende aos recursos declarativos Mongo; hooks, endpoints próprios e
  validações com código são acrescentados em Go.
- As duas formas produzem o mesmo `Resource` e podem ser combinadas no mesmo
  processo.
- A seção de Referência reúne os detalhes que complementam este guia.
:::
