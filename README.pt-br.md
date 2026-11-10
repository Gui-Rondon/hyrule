[English](README.md)

# hyrule

Um framework Go para construir APIs REST sobre Mongo: descreva um recurso
(schema, métodos, lookups) e ganhe CRUD completo com validação, ETags,
paginação e um outbox transacional — em Go, ou sem Go nenhum, via YAML.
Por baixo mantém o contrato HTTP do Eve (Python) (`internal/evecompat`),
de onde o projeto migrou.

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
RESOURCES_FILE=resources.yaml CONFIG_FILE=/dev/null \
  MONGO_CONNECTION_STRING='mongodb://localhost:27018/demo?directConnection=true' \
  PORT=8080 go run ./cmd/hyrule-serve
```

```bash
curl -X POST localhost:8080/tags -d '{"name":"Go","slug":"go"}'
curl localhost:8080/tags/go
```

Isso é o `cmd/hyrule-serve` + `internal/declarative` (ver
[`docs/pt-br/resources-yaml.md`](docs/pt-br/resources-yaml.md)) — cobre o
caso comum. Um recurso com lógica própria (hooks, endpoints custom,
validação condicional) continua sendo um binário Go pequeno com
`app.Main`, como os 4 exemplos em
[`examples/blog-apis/`](examples/blog-apis/README.pt-br.md) — os dois
caminhos registram recursos da mesma forma e convivem no mesmo processo.

## Documentação

| | |
|---|---|
| [`docs/pt-br/store.md`](docs/pt-br/store.md) | a interface `Store`: o que ela desacopla (e o que não desacopla ainda) |
| [`docs/pt-br/outbox.md`](docs/pt-br/outbox.md) | outbox transacional, e o primitivo condicional que dispara evento a partir do próprio write |
| [`docs/pt-br/resources-yaml.md`](docs/pt-br/resources-yaml.md) | referência completa do YAML de recurso |
| [`docs/pt-br/dry-run.md`](docs/pt-br/dry-run.md) | rodar em dry run: escreve tudo, mas a transação sempre reverte |
| [`docs/pt-br/testkit.md`](docs/pt-br/testkit.md) | testar um recurso (Mongo isolado, cliente HTTP, tokens) |
| [`docs/pt-br/arquitetura.md`](docs/pt-br/arquitetura.md) | a camada `services → behaviors → evecompat → platform` e por que ela existe |

## Estrutura

    internal/app/              bootstrap comum (app.Main / app.Build)
    internal/behaviors/        hooks reutilizáveis: searchsync (ES), audit, events (outbox na escrita)
    internal/evecompat/        contrato HTTP: Service (regras, sem HTTP) + API (HTTP) + Store (interface de storage)
    internal/outbox/           transactional outbox, relay para o Pub/Sub e o primitivo condicional
    internal/declarative/      carrega um Resource/Schema de YAML (cmd/hyrule-serve)
    internal/platform/         config, mongodb, auth, search, health, server, pubsub, redis
    internal/testkit/          Mongo isolado por teste, ES gravador, tokens
    cmd/hyrule-serve/          binário genérico: registra os recursos de um YAML, sem Go de domínio
    ci/                        ci/affected (seleção de serviços afetados por um diff)
    examples/blog-apis/        uso de exemplo do framework (ver o README desse diretório)

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
