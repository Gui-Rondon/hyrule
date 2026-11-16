---
sidebar_position: 2
---

# Primeiros passos com Mongo ou Postgres

O [guia de integração em Go](/docs/reference/backends) traz serviços completos
com **Mongo e Postgres**, incluindo migrações, índices e testes. O passo a passo
em YAML abaixo usa Mongo. Ao final, há um caminho para executar a API de
comentários com Postgres, usando a mesma inicialização e os mesmos eventos
transacionais.

:::info[O que você vai aprender]
- Como subir a infraestrutura local
- Como escrever um `resources.yaml` mínimo
- Como executar a API com `cmd/hyrule-serve` e fazer requisições com `curl`
:::

## 1. Suba a infraestrutura

O MongoDB precisa funcionar como replica set para permitir transações.
Os exemplos de outbox apresentados mais adiante também usam Pub/Sub e Redis.
O repositório inclui um `docker-compose.yml` para esse ambiente:

```bash
git clone https://github.com/Gui-Rondon/hyrule.git
cd hyrule
make env-up
```

Isso inicia Mongo em `localhost:27018`, Postgres em `:5433`, o emulador do
Pub/Sub em `:8085` e Redis em `:6380`. As portas da infraestrutura ficam
acessíveis apenas na sua máquina.

## 2. Descreva um recurso

Crie o arquivo `resources.yaml`:

```yaml
resources:
  - name: tags
    collection: tags
    additional_lookup:
      field: slug
      pattern: '^[\w-]+$'
    resource_methods: [get, post]
    item_methods: [get, patch, put]
    schema:
      name:
        type: string
        required: true
      slug:
        type: string
        required: true
        unique: true
      enabled:
        type: boolean
        default: true
```

A definição cria o recurso `tags`, armazenado na coleção `tags`, com dois
comportamentos: `additional_lookup` permite buscar um item por `slug`, além
do `_id`; e `unique: true` verifica se o `slug` já existe antes da gravação.

## 3. Execute a API

```bash
echo '{"auth": {"enable": "false"}}' > config.json
RESOURCES_FILE=resources.yaml CONFIG_FILE=config.json \
  MONGO_CONNECTION_STRING='mongodb://localhost:27018/demo?directConnection=true' \
  PORT=8080 go run ./cmd/hyrule-serve
```

O `config.json` desabilita a autenticação para este teste local. O schema e as
opções do recurso ficam em `resources.yaml`; consulte a
[referência de resources.yaml](/docs/reference/resources-yaml) para conhecê-los.

## 4. Faça requisições

```bash
curl -X POST localhost:8080/tags -H 'Content-Type: application/json' \
  -d '{"name":"Go","slug":"go"}'
# {"_created":"...","_etag":"...","_id":"...","_status":"OK","_updated":"..."}

curl localhost:8080/tags/go
# {"name":"Go","slug":"go","enabled":true,"_id":"...", ...}

curl -X POST localhost:8080/tags -H 'Content-Type: application/json' \
  -d '{"name":"Golang","slug":"go"}'
# 422 Unprocessable Entity — o slug "go" já existe
```

Com três requisições e nenhuma linha de Go, você gravou um documento real no
MongoDB e exercitou uma regra de validação. O `cmd/hyrule-serve` usa o pacote
`declarative` para ler o YAML e construir um `rest.Resource`, o mesmo tipo usado
pelos serviços escritos em Go. É disso que trata a próxima página.

:::tip[Retomando]
- `make env-up` sobe Mongo, Postgres, Pub/Sub e Redis para desenvolvimento local.
- O `cmd/hyrule-serve` transforma a definição YAML de um recurso em uma API.
- `additional_lookup` e `unique` são exemplos de comportamentos definidos como
  dados. Em [Seu primeiro recurso](./your-first-resource.md), você verá outras
  possibilidades do schema.
:::

## Primeiros passos com Postgres

Siga os [comandos da comments-api](https://github.com/Gui-Rondon/hyrule/blob/main/examples/blog-apis/README.md#comments-api-postgres--pubsub):
suba a infraestrutura, crie o tópico do Pub/Sub, execute
`make comments-migrate MIGRATE_ARGS='-action prepare'` com
`POSTGRES_CONNECTION_STRING` definida e compile e execute a comments-api.
Crie um comentário com POST e consulte `/comments?where={"post_id":"post-1"}`.
O [guia de backends](/docs/reference/backends) também traz um programa Go mínimo
para quem não precisa de Pub/Sub. YAML ainda é exclusivo do Mongo: mudar a URI
do hyrule-serve não seleciona o Postgres.
