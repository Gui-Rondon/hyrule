---
sidebar_position: 1
---

# Introdução à plataforma Hyrule

O [guia de aprendizado](/docs/learn/intro) apresenta um recurso e um serviço por
vez: é o uso direto do **framework**. A **plataforma** acrescenta ferramentas
para equipes que querem vários serviços no mesmo repositório, gerando arquivos
como `go.mod`, `Makefile`, infraestrutura local e um recurso inicial.

Os dois caminhos usam o mesmo `rest.Resource` e a mesma inicialização com
`app.Main`. A plataforma automatiza a criação da estrutura do projeto, sem
introduzir uma segunda implementação do framework.

:::info[O que você vai aprender]
- Como criar um projeto com vários serviços usando `hyrule new`
- Como acrescentar um serviço com `hyrule service create`
- Como executar e testar o projeto com `make env-up` e `make test`
- Como adaptar o fluxo de CI e implantação do exemplo `blog-apis`
:::

## O que está disponível hoje

A CLI oferece dois comandos:

```bash
hyrule new <project> --layout=platform
hyrule service create <name>
```

Esse é o escopo atual, definido no
[ADR-0004](/docs/reference/architecture-decisions). Os comandos `hyrule dev`,
`hyrule build --affected` e `hyrule deploy` ainda estão no
[roteiro de evolução](/docs/reference/roadmap).

Algumas ferramentas que esses comandos poderiam reunir já funcionam
separadamente. A detecção de serviços afetados e os scripts de implantação são
usados no CI de
[`examples/blog-apis`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/blog-apis).
A página [CI e implantação](./ci-and-deploy.md) mostra como adaptar esse fluxo
para um projeto gerado por `hyrule new`.

## Estrutura de um projeto gerado

```
my-platform/
├── go.mod              # dependência github.com/Gui-Rondon/hyrule, após go get
├── Makefile             # build/run/test/env-up, como neste repositório
├── docker-compose.yml   # Mongo em replica set para desenvolvimento local
├── README.md
└── services/
    └── articles/        # hyrule service create articles
        ├── main.go
        ├── resource.go
        └── resource_test.go
```

São arquivos comuns de um projeto Go, com os mesmos tipos e interfaces
apresentados no guia. O projeto gerado não depende da CLI para continuar sendo
desenvolvido.

:::tip[Retomando]
- A plataforma oferece ferramentas de apoio ao framework.
- A CLI gera projetos e serviços com `hyrule new` e `hyrule service create`.
- CI e implantação são configurados com as ferramentas e os scripts do projeto,
  como veremos ao final desta seção.
:::
