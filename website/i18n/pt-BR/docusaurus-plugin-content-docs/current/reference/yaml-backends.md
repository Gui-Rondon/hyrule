---
sidebar_position: 5
---

# Execute recursos YAML com Mongo ou Postgres

Execute os comandos na raiz do repositório, com Go e Docker Compose
instalados. Escolha um backend e execute um servidor por vez na porta 8080.
Os dois exemplos expõem as mesmas rotas `/tags`.

## Descreva o recurso

Salve como `resources-mongo.yaml`:

```yaml
resources:
  - name: tags
    collection: tags
    backend: mongo
    resource_methods: [get, post]
    item_methods: [get, patch]
    schema:
      name: {type: string, required: true}
      slug: {type: string, required: true, unique: true}
```

Para Postgres, salve uma cópia como `resources-postgres.yaml` e troque apenas
`backend: mongo` por `backend: postgres`. Nesse caso, `collection` é o nome
da tabela SQL. O `backend` de cada recurso precisa corresponder ao `BACKEND`
do processo. Omitir o campo YAML ou a variável de ambiente faz aquela opção
assumir Mongo independentemente; defina os dois explicitamente para Postgres.

## Mongo {/* #mongo */}

Inicie o replica set do repositório e aguarde ficar pronto:

```bash
docker compose up -d --wait mongo
```

Salve `config-mongo.json`:

```json
{
  "auth": {"enable": false},
  "mongo": {
    "uri": "mongodb://localhost:27018/hyrule?directConnection=true",
    "query_timeout": "5s",
    "indexes": "tags slug:1 name=tags_slug_unique unique\n"
  }
}
```

A autenticação fica desabilitada para experimentar localmente. Mongo cria a
coleção na primeira escrita; a declaração do índice garante a unicidade do
slug no banco, inclusive em escritas concorrentes.

```bash
BACKEND=mongo CONFIG_FILE=config-mongo.json \
RESOURCES_FILE=resources-mongo.yaml PORT=8080 \
go run ./cmd/hyrule-serve
```

## Postgres {/* #postgres */}

Inicie o Postgres e aplique o DDL da tabela antes de iniciar a API.
Neste exemplo local, use o banco `postgres` existente:

```bash
docker compose up -d --wait postgres
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d postgres <<'SQL'
CREATE TABLE tags (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    doc jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1,
    deleted boolean NOT NULL DEFAULT false
);
CREATE UNIQUE INDEX tags_slug_unique ON tags ((doc->>'slug'));
SQL
```

Execute essa migração uma vez em um banco novo. `hyrule-serve` não cria
tabelas de recursos no Postgres nem migra schemas existentes. `unique: true`
ativa a validação; o índice único também protege escritas concorrentes.

Salve `config-postgres.json`:

```json
{
  "auth": {"enable": false},
  "postgres": {
    "uri": "postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable",
    "max_conns": 10,
    "min_conns": 1,
    "connect_timeout": "5s",
    "query_timeout": "5s",
    "statement_timeout": "5s",
    "lock_timeout": "1s"
  }
}
```

São configurações de exemplo para uso local: `max_conns`/`min_conns`
dimensionam o pool, `connect_timeout` limita o estabelecimento de conexões,
`query_timeout` limita operações dos recursos e `statement_timeout`/
`lock_timeout` são aplicados nas conexões Postgres. A autenticação está
desabilitada e a URI local desabilita TLS; configure ambos no seu ambiente.
Veja a [configuração dos backends](backends.md).

```bash
BACKEND=postgres CONFIG_FILE=config-postgres.json \
RESOURCES_FILE=resources-postgres.yaml PORT=8080 \
go run ./cmd/hyrule-serve
```

`MONGO_CONNECTION_STRING` e `POSTGRES_CONNECTION_STRING` têm precedência
sobre a respectiva URI configurada. Confira essas variáveis se o servidor
conectar a um banco inesperado.

## Experimente qualquer uma das APIs

```bash
curl -i -X POST localhost:8080/tags \
  -H 'Content-Type: application/json' -d '{"name":"Go","slug":"go"}'
curl -G localhost:8080/tags --data-urlencode 'where={"slug":"go"}'
```

Use o `_id` e o `_etag` retornados pelo POST nas operações de item:

```bash
curl -i localhost:8080/tags/ID
curl -i -X PATCH localhost:8080/tags/ID \
  -H 'Content-Type: application/json' -H 'If-Match: "ETAG"' \
  -d '{"name":"Golang"}'
```

IDs e ETags são opacos e variam entre backends. Postgres usa UUIDs e merge
patch RFC 7396 (`null` remove um campo); Mongo usa ObjectIDs e a semântica
de merge do Eve. Trocar o backend não migra dados. Veja a
[referência dos campos YAML](resources-yaml.md) para mais opções e os
[contratos de adapters](adapter.md) para as diferenças de semântica.
