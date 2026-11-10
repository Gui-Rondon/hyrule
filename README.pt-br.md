[English](README.md)

# hyrule

[![Go Reference](https://pkg.go.dev/badge/github.com/Gui-Rondon/hyrule.svg)](https://pkg.go.dev/github.com/Gui-Rondon/hyrule)
[![codecov](https://codecov.io/gh/Gui-Rondon/hyrule/branch/main/graph/badge.svg)](https://codecov.io/gh/Gui-Rondon/hyrule)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Um framework Go para montar microsserviços rápido, e testar os fluxos
deles com a mesma facilidade: descreva um recurso (schema, métodos,
lookups) num arquivo YAML — sem Go nenhum — e ganhe uma API REST
rodando, com CRUD completo, validação, ETags, paginação e um outbox
transacional, sobre Mongo (suporte a Postgres em andamento). Um recurso
com lógica própria continua sendo um binário Go pequeno. Dos dois
jeitos, o `testkit` dá a cada teste um banco isolado e um cliente HTTP,
e o dry run deixa um serviço inteiro rodar contra tráfego real com toda
escrita revertida — então você testa os fluxos de verdade de um serviço
sem levantar a stack real nem simular na mão. Por baixo mantém o
contrato HTTP do Eve (Python) (`rest`), de onde o projeto
migrou.

📖 **Documentação completa: https://gui-rondon.github.io/hyrule/** — a
seção Reference já está em português
([`/pt-BR/docs/reference/...`](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/architecture));
o guia Learn (passo a passo) ainda só existe em inglês.

## Quickstart: um recurso sem Go

```yaml
# resources.yaml
resources:
  - name: tags
    collection: tags
    additional_lookup: {field: slug, pattern: '^[\w-]+$'}
    resource_methods: [get, post]
    item_methods: [get, patch, put]
    schema:
      name: {type: string, required: true}
      slug: {type: string, required: true, unique: true}
      enabled: {type: boolean, default: true}
```

```bash
make env-up    # Mongo (27018), Pub/Sub (8085), Redis (6380)
echo '{"auth": {"enable": "false"}}' > config.json
RESOURCES_FILE=resources.yaml CONFIG_FILE=config.json \
  MONGO_CONNECTION_STRING='mongodb://localhost:27018/demo?directConnection=true' \
  PORT=8080 go run ./cmd/hyrule-serve
```

```bash
curl -X POST localhost:8080/tags -d '{"name":"Go","slug":"go"}'
curl localhost:8080/tags/go
```

Isso é o `cmd/hyrule-serve` + `declarative` — cobre o caso
comum. Um recurso com lógica própria (hooks, endpoints custom, validação
condicional) continua sendo um binário Go pequeno com `app.Main`, como os
4 exemplos em [`examples/blog-apis/`](examples/blog-apis/README.pt-br.md)
— os dois caminhos registram recursos da mesma forma e convivem no mesmo
processo.

[`examples/standalone-tags-api/`](examples/standalone-tags-api/README.pt-br.md)
é o mesmo quickstart acima, mas como um módulo Go próprio (`go.mod`
próprio) que depende deste — a prova de que um repositório externo
consegue mesmo fazer `go get` neste framework, não só importar de dentro
deste monorepo.

## Platform: montando um projeto com múltiplos serviços

```bash
go install github.com/Gui-Rondon/hyrule/cmd/hyrule@main

hyrule new my-platform
cd my-platform
go get github.com/Gui-Rondon/hyrule@main
hyrule service create articles

make env-up
make build SERVICE=articles
make run SERVICE=articles
```

`hyrule new`/`hyrule service create` são os dois únicos comandos hoje
(ADR-0004) — scaffolding, não um orquestrador de build/deploy. O
projeto gerado tem seu próprio `go.mod`, `Makefile` e
`docker-compose.yml`, no mesmo formato deste repositório; os
subcomandos `dev`/`build --affected`/`deploy` são trabalho futuro (ver
o [roadmap](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/roadmap)).

## Documentação

A documentação completa está em **https://gui-rondon.github.io/hyrule/**:

| | |
|---|---|
| [Reference → Store](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/store) | a interface `Store`: o que ela desacopla (e o que não desacopla ainda) |
| [Reference → Outbox](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/outbox) | outbox transacional, e o primitivo condicional que dispara evento a partir do próprio write |
| [Reference → resources.yaml](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/resources-yaml) | referência completa do YAML de recurso |
| [Reference → Dry run](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/dry-run) | rodar em dry run: escreve tudo, mas a transação sempre reverte |
| [Reference → Testkit](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/testkit) | testar um recurso (Mongo isolado, cliente HTTP, tokens) |
| [Reference → Architecture](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/architecture) | a camada `services → behaviors → rest → platform` e por que ela existe |
| [Reference → Querying](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/querying) | a linguagem de query `where=`/`sort`/`projection` e como ela é interpretada |
| [Reference → Backend Postgres](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/postgres-rfc) | as decisões de design abertas para um segundo backend, além do Mongo |

## Estrutura

    app/                           bootstrap comum (app.Main / app.Build)
    behaviors/                     hooks reutilizáveis: searchsync (ES), audit, events (outbox na escrita)
    rest/                          contrato HTTP: Service (regras, sem HTTP) + API (HTTP) + Store (interface de storage)
    outbox/                        transactional outbox, relay para o Pub/Sub e o primitivo condicional
    declarative/                   carrega um Resource/Schema de YAML (cmd/hyrule-serve)
    platform/                      config, mongodb, auth, search, health, server, pubsub, redis
    testkit/                       Mongo isolado por teste, ES gravador, tokens
    internal/domain/               o único pacote que continua interno (geração de slug)
    cmd/hyrule-serve/              binário genérico: registra os recursos de um YAML, sem Go de domínio
    cmd/hyrule/                    o CLI da Platform: hyrule new, hyrule service create
    ci/                            ci/affected (seleção de serviços afetados por um diff)
    examples/blog-apis/            uso de exemplo do framework (ver o README desse diretório)
    examples/standalone-tags-api/  um módulo Go externo (go.mod próprio) dependendo deste
    website/                       código-fonte de https://gui-rondon.github.io/hyrule/ (Docusaurus)

## Desenvolvimento

Requer Go 1.27 e Docker.

    make env-up              # infra genérica: Mongo rs (27018), Pub/Sub (8085), Redis (6380)
    make test                # todos os testes (usa o Mongo do env-up)
    make unit                # só os que não precisam de serviços externos
    make cover                # cobertura, falha abaixo de 85%
    make build SERVICE=tags-api
    make run SERVICE=tags-api  # :8081, auth desligada

Para rodar o exemplo `blog-apis` (que também precisa de Elasticsearch):
veja [`examples/blog-apis/README.pt-br.md`](examples/blog-apis/README.pt-br.md).

## Licença

[MIT](LICENSE) — © 2026 Guilherme Rondon.
