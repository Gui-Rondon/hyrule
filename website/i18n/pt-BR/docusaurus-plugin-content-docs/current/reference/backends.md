---
sidebar_position: 0
---

# Integrando Mongo e Postgres

Os dois backends usam `app.Main`/`app.Build`, `rest.Resource`, validação HTTP,
ETags, hooks, autenticação, health checks, métricas e dry run com rollback.
Escolha o backend em Go; Mongo continua sendo o valor padrão dos serviços
existentes. Suporte equivalente não significa semântica de armazenamento idêntica.

| Ponto de integração | Mongo | Postgres |
|---|---|---|
| Bootstrap | `app.BackendMongo` (valor zero) | `app.BackendPostgres` |
| Persistência | `Resource.Store = env.Store(collection)` | `Resource.Adapter = restpostgres.NewAdapter(...)` |
| Conexão no Setup | `env.DB` | `env.Postgres`, `env.PostgresTx` |
| Identidade | BSON ObjectID | UUID |
| Índices | `Spec.Indexes` / `mongo.indexes` | `Spec.PostgresIndexes` |
| Eventos transacionais | `outbox.New(env.DB)` | `outboxpostgres.Outbox{Pool: env.Postgres}` |
| Dry run | Replica set, rollback e WriteGuard | Rollback da transação no mesmo pool |
| Testes | `testkit.Service` / Mongo real | `app.Build` + Postgres real isolado + `testkit.NewClient` |
| YAML / recurso gerado pela CLI | Suportado | Registro em Go; YAML/scaffolding continuam específicos do Mongo |

## Inicie as dependências locais

Em um checkout desta integração:

```sh
make env-up
```

O compose padrão inicia Mongo (27018), Postgres (5433), Pub/Sub (8085) e Redis
(6380). Ele é separado do laboratório HA manual, usado neste workspace em
**5540**. Não aponte comandos HA para 5434 aqui; a porta pertence a outro projeto.
Em um módulo externo, instale uma versão ou commit que contenha a integração;
não suponha que ela já esteja em `main`. Nesse módulo, salve **um** dos programas
completos abaixo como `main.go` e execute `go mod tidy`.

## Mongo: registre e execute o serviço

```go
package main

import (
    "context"
    "net/http"

    "github.com/Gui-Rondon/hyrule/app"
    "github.com/Gui-Rondon/hyrule/rest"
)

func main() {
    app.Main(app.Spec{
        Name: "items-api", Backend: app.BackendMongo, DefaultDB: "items_demo",
        Indexes: []byte("items name:1 name=items_name_idx\n"),
        Setup: func(ctx context.Context, env *app.Env) error {
            env.API.Register(&rest.Resource{
                Name: "items", Store: env.Store("items"),
                Schema: rest.Schema{"name": {Type: rest.TypeString, Required: true}},
                ResourceMethods: []string{http.MethodGet, http.MethodPost},
                ItemMethods: []string{http.MethodGet, http.MethodPatch, http.MethodPut, http.MethodDelete},
            })
            return nil
        },
    })
}
```

Mongo cria a collection na primeira escrita. Para escritas transacionais,
outbox e dry run, conecte a um replica set; o compose local fornece um.
Mongo standalone usa um transactor de passagem e não garante atomicidade entre
várias escritas/outbox. O registro legado em `Resource.Store` continua válido:
um adapter Mongo interno é construído automaticamente.

```sh
printf '%s\n' '{"auth":{"enable":false}}' > config.json
CONFIG_FILE=config.json PORT=8080 \
MONGO_CONNECTION_STRING='mongodb://localhost:27018/items_demo?directConnection=true' \
  go run .
```

Declare índices no startup, independentemente da validação amigável de schema:

```go
spec.Indexes = []byte("items name:1 name=items_name_idx\n")
// Acrescente `unique` quando o contrato de negócio exigir. Schema.Unique
// é uma pré-checagem, não proteção contra duplicatas concorrentes.
```

`mongo.indexes_mode` aceita `create` (padrão), `check` e `off`; dry run troca
create por check. O bootstrap combina `.indices.conf` embutido e overrides
`mongo.indexes`. Veja [Store e índices](store.md). Para a alternativa YAML,
siga [resources.yaml](resources-yaml.md).

## Postgres: migre, registre e execute o serviço

Crie o banco uma vez e aplique o DDL como uma migration explícita:

```sh
docker compose exec -T postgres psql -U postgres -d postgres -c 'CREATE DATABASE items_demo'
docker compose exec -T postgres psql -U postgres -d items_demo <<'SQL'
CREATE TABLE items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    doc jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1,
    deleted boolean NOT NULL DEFAULT false
);
SQL
```

```go
package main

import (
    "context"
    "net/http"

    "github.com/Gui-Rondon/hyrule/app"
    "github.com/Gui-Rondon/hyrule/platform/postgres"
    "github.com/Gui-Rondon/hyrule/rest"
    "github.com/Gui-Rondon/hyrule/restpostgres"
)

func main() {
    schema := rest.Schema{"name": {Type: rest.TypeString, Required: true}}
    app.Main(app.Spec{
        Name: "items-api", Backend: app.BackendPostgres, DefaultDB: "items_demo",
        PostgresIndexes: []postgres.IndexSpec{{
            Table: "items", Fields: []postgres.IndexField{{Field: "name"}},
        }},
        Setup: func(ctx context.Context, env *app.Env) error {
            adapter, err := restpostgres.NewAdapter(
                postgres.NewStore(env.Postgres, "items"), env.PostgresTx,
                restpostgres.Options{Schema: schema, Retry: rest.DefaultRetryOptions()},
            )
            if err != nil { return err }
            env.API.Register(&rest.Resource{
                Name: "items", Adapter: adapter, Schema: schema,
                ResourceMethods: []string{http.MethodGet, http.MethodPost},
                ItemMethods: []string{http.MethodGet, http.MethodPatch, http.MethodPut, http.MethodDelete},
            })
            return nil
        },
    })
}
```

Reutilize `env.PostgresTx`: ele carrega o pool e a política de dry run da
requisição. `platform/postgres.Store` é um helper de colunas planas, **não**
uma implementação de `rest.Store`; recursos JSONB precisam de
`restpostgres.NewAdapter`. O bootstrap instala funções compartilhadas antes
do Setup e garante índices declarados depois dele. Não cria a tabela do
recurso nem executa migrations. O exemplo comments usa runner explícito em
etapas e checagem de schema no startup; veja o [runbook de migrations](https://github.com/Gui-Rondon/hyrule/blob/integration/postgres-portability/examples/blog-apis/internal/commentsschema/README.md).

```sh
printf '%s\n' '{"auth":{"enable":false}}' > config.json
CONFIG_FILE=config.json PORT=8080 \
POSTGRES_CONNECTION_STRING='postgres://postgres:postgres@localhost:5433/items_demo?sslmode=disable' \
  go run .
```

`postgres.indexes_mode` também aceita create/check/off; dry run troca create por
check. Os índices são valores Go `IndexSpec`, não texto `.indices.conf` Mongo.
`EnsureIndexes` verifica nomes, não equivalência das definições: use nome novo
explícito ao adotar outra expressão; o startup não migra um índice existente.

## Exercite qualquer um dos serviços

As mesmas requisições funcionam com ambos; trate IDs e ETags como opacos e use
os valores reais retornados nas próximas requisições:

```sh
curl -i -X POST localhost:8080/items -H 'Content-Type: application/json' -d '{"name":"first"}'
curl -G localhost:8080/items --data-urlencode 'where={"name":"first"}' --data-urlencode 'sort=name'
curl -i localhost:8080/items/ID
curl -i -X PATCH localhost:8080/items/ID -H 'Content-Type: application/json' \
  -H 'If-Match: "ETAG"' -d '{"name":"second"}'
```

ETags antigos retornam 412. Não converta ObjectID em UUID nem reutilize ETag de
outro banco. PATCH Postgres segue RFC 7396 (`null` remove a chave); Mongo
preserva deep merge/null do Eve. Ambos rejeitam dados obrigatórios inválidos,
mas rotas iguais não implicam documentos ou erros idênticos byte a byte.
Veja [contratos do Adapter](adapter.md) e [consultas](querying.md).

## Configuração, ciclo de vida e limites de integração

| Aspecto | Mongo | Postgres |
|---|---|---|
| Precedência de conexão | `MONGO_CONNECTION_STRING`, `mongo.uri`, campos individuais `mongo.*` | `POSTGRES_CONNECTION_STRING`, `postgres.uri`, campos individuais `postgres.*` |
| Orçamento de leitura da aplicação | `mongo.query_timeout` (padrão 10s) | `postgres.query_timeout` (padrão 10s) |
| Conexão/pool | Opções de driver na URI Mongo | `postgres.max_conns`, `min_conns`, `max_conn_lifetime`, `max_conn_idle_time`, `health_check_period`, `connect_timeout` |
| Limites de statement/lock | Configurações do driver/servidor Mongo | `postgres.statement_timeout`, `postgres.lock_timeout` (durations Go enviados em milissegundos) |
| Escritas de negócio | Reutilize contexto e sessão Mongo | Use `Store.Querier(ctx)` com mesmo pool/contexto |
| Tarefas de fundo | `env.Go` | `env.Go` |

Sempre inclua o banco pretendido na URI. Uma URI Postgres sem banco segue a
resolução do pgx em vez de aplicar `Spec.DefaultDB`; a configuração por campos
host/port usa DefaultDB. Mongo também usa DefaultDB quando a URI omite o banco.
Não suponha que opções dos drivers sejam intercambiáveis. Valores desconhecidos
de Spec.Backend são rejeitados antes de carregar configuração/abrir conexão.

`app.Run` gerencia as conexões que abre; o chamador de `app.Build` gerencia a
vida das dependências fornecidas. Build não inicia tarefas sem `Deps.Start`.
`env.Go` é suprimido em dry run. O shutdown cancela o contexto; workers devem
respeitar cancelamento. Efeitos externos pertencem ao outbox/consumidor após
commit ou a rotas recusadas em dry run, não a callbacks de transação.
Autenticação continua sendo validação JWT, sem gerenciamento de sessão de usuário.

## Eventos, dry run e testes

- [Outbox transacional](outbox.md): registro equivalente nos dois backends,
  entrega pelo menos uma vez, diferenças dos relays e replay de eventos mortos.
- [Dry run](dry-run.md): transações, cuidados no setup e efeitos externos.
- [Testes](testkit.md): isolamento de banco real e app.Build em ambos.
- [Revisão da integração e evidências](integration-review.md): comparação com main,
  mudanças de compatibilidade, medições e limites.

## Adote os opt-ins Postgres explicitamente

| Opt-in | Onde configurar | Padrão preservado | Adoção e reversão |
|---|---|---|---|
| Índice de sort tipado | `IndexField.Mode = postgres.IndexFieldTypedSort`, nome novo explícito | Expressão JSONB legada | Audite casts e custo de escrita; somente String/Integer/Number/Boolean, sem Unique/Sparse/datetime typed. Para reverter, remova a declaração e migre/remova explicitamente o novo índice se não for usado. |
| Sort datetime seguro | `restpostgres.Options.SafeDateTimeSort = true` | Cast direto e erros de dados inválidos | Audite formatos; rejeitados ordenam como SQL NULL, sem mudar documentos. Índice legado datetime já pode corresponder. Remova a opção para restaurar cast direto. |
| Política de planos custom | URI vencedora: `plan_cache_mode=force_custom_plan` | Política existente de servidor/role (normalmente auto) | Abrange o pool inteiro, inclusive escritas/outbox; custa planejamento por execução. Meça workload real. Remova o parâmetro e recrie o pool para reverter. |

Presença sparse explícita (`{"slug":{"$eq":"value","$exists":true}}`) pode
viabilizar o índice parcial legado em planos custom. Nenhuma guarda de presença
é acrescentada implicitamente: a tentativa anterior regrediu planos UUID.
Campos parametrizados no generic e offsets profundos continuam observacionais;
declarar índice não garante uso nem latência de produção. Nested datetime e
Unique/Sparse typed são extensões separadas. YAML continua Mongo-only; trocar
Spec.Backend não converte backend nem migra dados.
