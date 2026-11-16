[English](README.md)

# hyrule

[![Go Reference](https://pkg.go.dev/badge/github.com/Gui-Rondon/hyrule.svg)](https://pkg.go.dev/github.com/Gui-Rondon/hyrule)
[![codecov](https://codecov.io/gh/Gui-Rondon/hyrule/branch/main/graph/badge.svg)](https://codecov.io/gh/Gui-Rondon/hyrule)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Um framework Go para montar microsserviços rápido, e testar os fluxos
deles com a mesma facilidade: descreva um recurso (schema, métodos,
lookups) — num arquivo YAML, sem Go nenhum, ou em Go quando precisa de
lógica própria — e ganhe uma API REST rodando com:

- **CRUD completo, validação e paginação** a partir de um schema, sem
  handler escrito à mão.
- **ETags/`If-Match` nativos** — concorrência otimista, não algo que
  você monta por conta própria.
- **Mongo ou Postgres, mesmo contrato HTTP** — o caminho via YAML
  continua só-Mongo por enquanto; um recurso escrito à mão escolhe
  qualquer um dos dois backends via `rest.Adapter` (`restpostgres` pro
  Postgres, ver a referência do Adapter abaixo) pra CRUD, validação,
  ETags e queries.
- **Outbox transacional** (uma escrita e o evento que ela causa
  comitam juntos, na mesma transação, ou nenhum dos dois), **`testkit`**
  (banco isolado + cliente HTTP por teste) e **dry run** (um serviço
  inteiro contra tráfego real, toda escrita revertida). Outbox e dry run
  suportam ambos os backends. YAML e o provisionador de banco testkit continuam
  específicos do Mongo; helpers HTTP/tokens também atendem Postgres via app.Build.

Por baixo mantém o contrato HTTP do Eve (Python) (`rest`), de onde o
projeto migrou.

📖 **Documentação em português brasileiro: https://gui-rondon.github.io/hyrule/pt-BR/** —
[Guia](https://gui-rondon.github.io/hyrule/pt-BR/docs/learn/intro) traz o passo a passo;
[Plataforma](https://gui-rondon.github.io/hyrule/pt-BR/docs/platform/intro) mostra como criar projetos e serviços;
a [Referência](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/architecture)
reúne os detalhes técnicos. As três seções também estão disponíveis em inglês.

## Documentação Swagger automática

O Swagger vem habilitado por padrão para todo recurso Go/YAML: acesse
`/api/docs` (ou `/swagger`), com OpenAPI 3.1 em `/api/openapi.json`. Use
`DisableSwagger: true` em Go ou `disableSwagger: true` em YAML para ocultar
um recurso da documentação, mantendo suas rotas ativas. A documentação é
pública; operações protegidas exigem token. Veja a [referência Swagger](website/i18n/pt-BR/docusaurus-plugin-content-docs/current/reference/swagger.md).

## Integração Mongo e Postgres

Ambos usam `app.Main`/`app.Build`, o mesmo contrato de recursos HTTP e hooks de
eventos transacionais. O [guia de integração](website/i18n/pt-BR/docusaurus-plugin-content-docs/current/reference/backends.md)
traz serviços Go completos para os dois, configuração, schema/índices, testes
e opt-ins. A [revisão da integração](website/i18n/pt-BR/docusaurus-plugin-content-docs/current/reference/integration-review.md)
registra compatibilidade e gates contra `main`. Use versão/commit contendo a
integração; mudar o backend não migra dados.

| | Mongo | Postgres |
|---|---|---|
| Recurso | `Store: env.Store("items")` | `Adapter: restpostgres.NewAdapter(...)` |
| Bootstrap | `app.BackendMongo` (padrão) | `app.BackendPostgres` |
| Outbox | `outbox` | `outboxpostgres` |
| Exemplo executável | [posts-api](examples/blog-apis/services/posts-api) | [comments-api](examples/blog-apis/services/comments-api) |

## Quickstart Mongo: um recurso sem Go

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
make env-up    # Mongo (27018), Postgres (5433), Pub/Sub (8085), Redis (6380)
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
5 exemplos em [`examples/blog-apis/`](examples/blog-apis/README.pt-br.md)
— os dois caminhos registram recursos da mesma forma e convivem no mesmo
processo.

[`examples/standalone-tags-api/`](examples/standalone-tags-api/README.pt-br.md)
é o mesmo quickstart acima, mas como um módulo Go próprio (`go.mod`
próprio) que depende deste — a prova de que um repositório externo
consegue mesmo fazer `go get` neste framework, não só importar de dentro
deste monorepo.

## Quickstart Postgres: recurso Go com eventos transacionais

O serviço comments usa `app.BackendPostgres`, CAS UUID/version, JSONB,
migrations explícitas e outbox transacional. A partir deste checkout:

```sh
make env-up
curl -sf -X PUT http://localhost:8085/v1/projects/local-dev/topics/comments-to-publish
POSTGRES_CONNECTION_STRING='postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable' \
  make comments-migrate MIGRATE_ARGS='-action prepare'
make build SERVICE=comments-api
printf '%s\n' '{"auth":{"enable":false},"pubsub":{"project_id":"local-dev","to_publish":"comments-to-publish"}}' > .local/comments-api.json
POSTGRES_CONNECTION_STRING='postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable' \
  PUBSUB_EMULATOR_HOST=localhost:8085 CONFIG_FILE=.local/comments-api.json \
  PORT=8082 .local/comments-api
```

```sh
curl -X POST localhost:8082/comments -H 'Content-Type: application/json' \
  -d '{"post_id":"post-1","author_name":"Link","body":"Hello","status":"published"}'
curl -G localhost:8082/comments --data-urlencode 'where={"post_id":"post-1"}'
```

Veja [o runbook do exemplo](examples/blog-apis/README.pt-br.md) para entrega
Pub/Sub e rollout do schema. O [guia dos backends](website/i18n/pt-BR/docusaurus-plugin-content-docs/current/reference/backends.md)
mostra também serviço Postgres mínimo sem a dependência Pub/Sub do exemplo.

## Plataforma: montando um projeto com múltiplos serviços

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
O [guia da Plataforma](https://gui-rondon.github.io/hyrule/pt-BR/docs/platform/intro)
apresenta o passo a passo,
incluindo copiar o padrão de CI de build afetado e deploy do
`examples/blog-apis` pra um projeto gerado.

## Documentação

A documentação completa está em **https://gui-rondon.github.io/hyrule/pt-BR/**:

| | |
|---|---|
| [Guia](https://gui-rondon.github.io/hyrule/pt-BR/docs/learn/intro) | guia passo a passo com os exemplos de `blog-apis` |
| [Plataforma](https://gui-rondon.github.io/hyrule/pt-BR/docs/platform/intro) | criação de projetos e serviços, desenvolvimento local e CI/implantação |
| [Referência → Store](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/store) | a interface `Store`: o que ela desacopla (e o que não desacopla ainda) |
| [Referência → Outbox](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/outbox) | outbox transacional, e o primitivo condicional que dispara evento a partir da própria gravação |
| [Referência → resources.yaml](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/resources-yaml) | referência completa do YAML de recurso |
| [Referência → Dry run](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/dry-run) | rodar em dry run: escreve tudo, mas a transação sempre reverte |
| [Referência → Testkit](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/testkit) | testar um recurso (Mongo isolado, cliente HTTP, tokens) |
| [Referência → Arquitetura](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/architecture) | a camada `services → behaviors → rest → platform` e por que ela existe |
| [Referência → Consultas](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/querying) | a linguagem de consulta `where=`/`sort`/`projection` e como ela é interpretada |
| [Referência → Adapter](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/adapter) | `rest.Adapter`, o ponto de integração que um recurso com Postgres (`restpostgres`) registra em vez de `Store` |
| [Referência → Backend Postgres (histórico)](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/postgres-rfc) | o registro de design original do backend Postgres, superado pela referência do Adapter acima |

## Estrutura

    app/                           bootstrap comum (app.Main / app.Build)
    behaviors/                     hooks reutilizáveis: searchsync (ES), audit, events (outbox na escrita)
    rest/                          contrato HTTP: Service (regras, sem HTTP) + API (HTTP) + Adapter (costura de storage, Store pro Mongo)
    restpostgres/                  rest.Adapter sobre platform/postgres — o caminho de resource com Postgres
    outbox/                        transactional outbox, relay para o Pub/Sub e o primitivo condicional
    declarative/                   carrega um Resource/Schema de YAML (cmd/hyrule-serve), só Mongo hoje
    platform/                      config, mongodb, postgres, auth, search, health, server, pubsub, redis
    testkit/                       Mongo isolado por teste, ES gravador, tokens
    internal/domain/               o único pacote que continua interno (geração de slug)
    cmd/hyrule-serve/              binário genérico: registra os recursos de um YAML, sem Go de domínio
    cmd/hyrule/                    a CLI da Plataforma: hyrule new, hyrule service create
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
