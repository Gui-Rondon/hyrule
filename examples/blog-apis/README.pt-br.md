[English](README.md)

# blog-apis

A práticas do framework hyrule aplicadas a um domínio genérico (um blog),
em vez de teoria: 4 serviços pequenos, cada um demonstrando um conjunto de
capacidades diferente.

| Serviço | Demonstra |
|---|---|
| [`authors-api`](services/authors-api) | CRUD 100% genérico: só schema + `env.API.Register`, zero Go de negócio |
| [`tags-api`](services/tags-api) | CRUD genérico com campo `Unique` e lookup por slug — o mesmo schema, sem reescrever nada, é [`cmd/hyrule-serve/example.yaml`](../../cmd/hyrule-serve/example.yaml) |
| [`categories-api`](services/categories-api) | Recurso hierárquico: caminho materializado recalculado a cada escrita, endpoint `GET /categories/tree` construído direto a partir da interface `Store` (sem `*mongo.Collection` em lugar nenhum) |
| [`posts-api`](services/posts-api) | O exemplo mais completo: relações (`author_id`, `category_id`, `tags`), outbox transacional, endpoints custom `/publish` e `/schedule` (este último escreve num hash do Redis e no outbox na mesma transação), indexação opcional no Elasticsearch |

## Rodando localmente

```bash
# infraestrutura genérica (mongo/pubsub/redis) + Elasticsearch deste exemplo
docker compose -f examples/blog-apis/docker-compose.yml --profile debug up -d --wait

SERVICES_DIR=examples/blog-apis/services make build SERVICE=posts-api
MONGO_CONNECTION_STRING='mongodb://localhost:27018/posts?directConnection=true' \
  PORT=8081 CONFIG_FILE=/dev/null .local/posts-api
```

Sem `CONFIG_FILE` apontando pra um arquivo com `{"auth": {"enable": false}}`,
os endpoints que exigem token (`/publish`, `/schedule`) vão pedir
autenticação — veja `internal/testkit` para gerar um token de teste, ou
desative `auth.enable` na config local.

## Como isso se encaixa no framework

Este exemplo não é o framework — é só um *uso* dele. A documentação do
próprio hyrule (interface `Store`, outbox condicional, resources via YAML)
fica em [`docs/pt-br/`](../../docs/pt-br/README.md) na raiz do repo.
