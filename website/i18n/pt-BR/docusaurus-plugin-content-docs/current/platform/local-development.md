---
sidebar_position: 4
---

# Desenvolvimento e testes locais

:::info[O que você vai aprender]
- Como executar e testar um serviço gerado usando o Makefile
- Como adicionar um segundo serviço ao projeto
- Quais componentes são compartilhados entre os serviços
:::

## Compile, execute e teste

Na raiz do projeto, depois de [criar o serviço](./creating-services.md)
`services/articles/`, execute:

```bash
make env-up                 # projeto gerado: Mongo em :27017
make build SERVICE=articles
make run SERVICE=articles   # :8080, autenticação desabilitada
```

O Compose do projeto gerado sobe apenas Mongo, normalmente em `:27017`.
Ele é diferente do Compose do repositório Hyrule, que também oferece Postgres,
Pub/Sub e Redis. Confira as portas no `docker-compose.yml` do seu projeto.

```bash
curl -X POST localhost:8080/articles -H 'Content-Type: application/json' \
  -d '{"name":"hello"}'
curl localhost:8080/articles/<id-retornado-na-resposta>
```

```bash
make test
```

`make test` executa `go test ./...`, incluindo os testes de todos os serviços.
A chamada a `testkit.Service` em `resource_test.go` fornece um banco Mongo
temporário para cada teste. Veja [Testes](/docs/learn/testing).

## Adicione um segundo serviço

```bash
hyrule service create authors
make build SERVICE=authors
make run SERVICE=authors GO_PORT=8081
```

Cada serviço tem seu próprio binário, gerado com
`go build -o .local/<name> ./services/<name>`. Eles podem rodar ao mesmo tempo em
portas diferentes; `GO_PORT` escolhe a porta de `make run`.

A CLI não gera um gerenciador de processos nem uma entrada Compose por serviço.
Para desenvolvimento local, execute os serviços em terminais separados.
O Compose iniciado por `make env-up` fornece a infraestrutura compartilhada.

## O que é compartilhado

Os serviços de um projeto compartilham:

- O módulo Go (`go.mod` e `go.sum`) e o Mongo local do `docker-compose.yml`.
- Os alvos do Makefile, parametrizados por `SERVICE=`.

Ainda não há um `hyrule dev` para iniciar todos os serviços juntos; veja o
[ADR-0004](/docs/reference/architecture-decisions). As relações entre serviços
também são definidas pelo seu código, como o campo `author_id` da `posts-api`
apresentado em [Relações e hooks](/docs/learn/relations-and-hooks).

:::tip[Retomando]
- Use `make build SERVICE=<name>` e `make run SERVICE=<name>` para cada serviço.
- `make test` executa os testes de todos os serviços.
- Para rodar serviços em paralelo, use um terminal por serviço e portas
  diferentes.
- Um comando de desenvolvimento que coordene vários serviços ainda está no
  [roteiro de evolução](/docs/reference/roadmap).
:::

## Serviços com Mongo e Postgres

O projeto gerado ainda usa Mongo. Para configurar um serviço Go com Mongo ou
Postgres, consulte o [guia de backends](/docs/reference/backends): ele apresenta
registro, variáveis de conexão, preparação do schema, testes e opções adicionais.
Recursos Postgres também usam `app.Main`, mas trocar apenas a URI não converte um
serviço Mongo ou YAML. O laboratório de alta disponibilidade é manual, local e
separado de `make env-up`.
