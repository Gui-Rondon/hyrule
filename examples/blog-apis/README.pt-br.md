[English](README.md)

# blog-apis

A práticas do framework hyrule aplicadas a um domínio genérico (um blog),
em vez de teoria: 5 serviços pequenos, cada um demonstrando um conjunto de
capacidades diferente.

| Serviço | Demonstra |
|---|---|
| [`authors-api`](services/authors-api) | CRUD 100% genérico: só schema + `env.API.Register`, zero Go de negócio |
| [`tags-api`](services/tags-api) | CRUD genérico com campo `Unique` e lookup por slug — o mesmo schema, sem reescrever nada, é [`cmd/hyrule-serve/example.yaml`](../../cmd/hyrule-serve/example.yaml) |
| [`categories-api`](services/categories-api) | Recurso hierárquico: caminho materializado recalculado a cada escrita, endpoint `GET /categories/tree` construído direto a partir da interface `Store` (sem `*mongo.Collection` em lugar nenhum) |
| [`posts-api`](services/posts-api) | O exemplo mais completo: relações (`author_id`, `category_id`, `tags`), outbox transacional, endpoints custom `/publish` e `/schedule` (este último grava evento no outbox na transação; o relay atualiza Redis após commit), indexação opcional no Elasticsearch |
| [`comments-api`](services/comments-api) | Postgres via `app.Main`/`restpostgres`, índice declarativo e outbox transacional publicando `comment.published` no Pub/Sub |

## Rodando localmente

```bash
# infraestrutura genérica (mongo/postgres/pubsub/redis) + Elasticsearch deste exemplo
docker compose -f examples/blog-apis/docker-compose.yml --profile debug up -d --wait

SERVICES_DIR=examples/blog-apis/services make build SERVICE=posts-api
echo '{"auth": {"enable": "false"}, "pubsub": {"project_id": "local-dev"}}' > config.json
MONGO_CONNECTION_STRING='mongodb://localhost:27018/posts?directConnection=true' \
  PUBSUB_EMULATOR_HOST=localhost:8085 \
  PORT=8081 CONFIG_FILE=config.json .local/posts-api
```

Sem `CONFIG_FILE` apontando pra um arquivo com `{"auth": {"enable": false}}`,
os endpoints que exigem token (`/publish`, `/schedule`) vão pedir
autenticação — veja `testkit` para gerar um token de teste, ou
desative `auth.enable` na config local.

## CI: buildando só o que mudou

[`.github/workflows/blog-apis-affected.yml`](../../.github/workflows/blog-apis-affected.yml)
roda o [`ci/affected`](../../ci) em todo push/PR, builda só os serviços
que ele reporta como afetados, e chama o
[`deploy/<service>/deploy.sh`](deploy) de cada um — um stub hoje, a
costura onde o comando de deploy real de um provedor entra (ver o
ADR-0006 na página de
[Decisões arquiteturais](https://gui-rondon.github.io/hyrule/pt-BR/docs/reference/architecture-decisions)).
Esse workflow é parte do próprio exemplo — copie e adapte, do jeito que
um projeto criado por `hyrule new` faria.

## Como isso se encaixa no framework

Este exemplo não é o framework — é só um *uso* dele. A documentação do
próprio hyrule (interface `Store`, outbox condicional, resources via YAML)
fica em **https://gui-rondon.github.io/hyrule/pt-BR/**.

## comments-api: Postgres + Pub/Sub

```bash
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

Uma escrita que leva `status` a `published` (inclusive criação já publicada)
grava `comment.published` na mesma transação Postgres. O corpo é o documento
do comentário; a chave de ordenação é seu UUID. `pubsub.to_publish` tem default
`comments-to-publish`; provisione o tópico antes de iniciar o relay. A entrega
é at-least-once: consumidores deduplicam por `event_id`. Dry run desfaz ambas
as escritas e não inicia o relay. `/healthcheck` verifica Postgres, atraso do
outbox e tópico. O harness e2e verifica a entrega real numa assinatura isolada,
além do comportamento HTTP.

As migrations rodam explicitamente antes do startup; veja o [runbook da A5](internal/commentsschema/README.md).

Mongo (`posts-api`) e Postgres (`comments-api`) têm bootstrap, hooks/outbox e dry run. O evento e o recurso comitam no banco local; Redis/PubSub recebem pelo relay após commit, sem transação distribuída. Veja [guia dos backends](../../website/i18n/pt-BR/docusaurus-plugin-content-docs/current/reference/backends.md).
