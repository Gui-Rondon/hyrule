[English](README.md)

# hyrule

Um framework Go para construir APIs REST sobre Mongo: descreva um recurso
(schema, métodos, lookups) e ganhe CRUD completo com validação, ETags,
paginação e um outbox transacional — em Go, ou sem Go nenhum, via YAML.
Por baixo mantém o contrato HTTP do Eve (Python) (`internal/evecompat`),
de onde o projeto migrou.

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

Isso é o `cmd/hyrule-serve` + `internal/declarative` — cobre o caso
comum. Um recurso com lógica própria (hooks, endpoints custom, validação
condicional) continua sendo um binário Go pequeno com `app.Main`, como os
4 exemplos em [`examples/blog-apis/`](examples/blog-apis/README.pt-br.md)
— os dois caminhos registram recursos da mesma forma e convivem no mesmo
processo.

## Documentação

A documentação completa está em **https://gui-rondon.github.io/hyrule/**:

| | |
|---|---|
| [Reference → Store](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/store) | a interface `Store`: o que ela desacopla (e o que não desacopla ainda) |
| [Reference → Outbox](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/outbox) | outbox transacional, e o primitivo condicional que dispara evento a partir do próprio write |
| [Reference → resources.yaml](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/resources-yaml) | referência completa do YAML de recurso |
| [Reference → Dry run](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/dry-run) | rodar em dry run: escreve tudo, mas a transação sempre reverte |
| [Reference → Testkit](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/testkit) | testar um recurso (Mongo isolado, cliente HTTP, tokens) |
| [Reference → Architecture](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/architecture) | a camada `services → behaviors → evecompat → platform` e por que ela existe |

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
    website/                  código-fonte de https://gui-rondon.github.io/hyrule/ (Docusaurus)

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
