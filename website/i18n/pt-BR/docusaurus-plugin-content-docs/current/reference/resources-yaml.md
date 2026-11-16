---
sidebar_position: 4
---

# Recursos em YAML

`declarative` carrega um ou mais recursos de um arquivo YAML;
`cmd/hyrule-serve` os registra sem nenhuma linha de Go específica do
domínio. Cobre o caso comum — um recurso só de schema, sem hooks nem
endpoints custom (`examples/blog-apis/services/authors-api` e `tags-api`
são exatamente esse caso, hoje escritos em Go). Quando o recurso precisa
de lógica própria, ele continua sendo um binário Go com `app.Main`, como
`examples/blog-apis/services/posts-api`.

```bash
RESOURCES_FILE=resources.yaml PORT=8080 go run ./cmd/hyrule-serve
```

## Um exemplo completo

Esse `resources.yaml` é o equivalente em YAML de
`examples/blog-apis/services/tags-api` (hoje escrito à mão em Go) —
toda requisição/resposta abaixo é saída real de rodar isso, não
inventada para esta documentação:

```yaml
resources:
  - name: tags
    collection: tags
    item_title: tag
    schema:
      name: {type: string, required: true}
      slug: {type: string, required: true, unique: true}
      enabled: {type: boolean, default: true}
    additional_lookup:
      field: slug
      pattern: '^[\w-]+$'
    resource_methods: [get, post]
    item_methods: [get, patch, put]
    public_methods: [get]
    public_item_methods: [get]
```

A autenticação vem ligada por padrão (config `auth.enable` do
`app.Env`), o que daria 401/403 nos `POST`s abaixo, já que eles não
estão em `public_methods` — o run abaixo desliga isso, o mesmo truque
de `CONFIG_FILE` que todo outro exemplo deste repo usa:

```bash
echo '{"auth": {"enable": false}}' > config.json
RESOURCES_FILE=resources.yaml CONFIG_FILE=config.json PORT=8080 go run ./cmd/hyrule-serve
```

Criando um — a resposta é o envelope do `WriteResult` (só os campos de
meta, o `BANDWIDTH_SAVER` do Eve), não o documento em si:

```bash
curl -X POST localhost:8080/tags -H 'Content-Type: application/json' \
  -d '{"name":"Go","slug":"go"}'
```

```json
{
  "_created": "Sat, 14 Nov 2026 21:38:49 GMT",
  "_etag": "2db9a568b830ed8c6e0f00e6e5e432e52f3bed03",
  "_id": "6af8d4e9e54b1d88e572d5e8",
  "_links": {"self": {"href": "tags/6af8d4e9e54b1d88e572d5e8", "title": "tag"}},
  "_status": "OK",
  "_updated": "Sat, 14 Nov 2026 21:38:49 GMT"
}
```

`additional_lookup` deixa a mesma rota de item resolver por `slug`,
não só por `_id` — e (`public_item_methods: [get]`) sem token:

```bash
curl localhost:8080/tags/go
```

```json
{
  "_created": "Sat, 14 Nov 2026 21:38:49 GMT",
  "_etag": "2db9a568b830ed8c6e0f00e6e5e432e52f3bed03",
  "_id": "6af8d4e9e54b1d88e572d5e8",
  "_links": {
    "collection": {"href": "tags", "title": "tags"},
    "parent": {"href": "/", "title": "home"},
    "self": {"href": "tags/6af8d4e9e54b1d88e572d5e8", "title": "tag"}
  },
  "_updated": "Sat, 14 Nov 2026 21:38:49 GMT",
  "enabled": true,
  "name": "Go",
  "slug": "go"
}
```

`unique: true` em `slug` transforma uma repetição num problema de
validação, não num erro cru de chave duplicada:

```bash
curl -X POST localhost:8080/tags -H 'Content-Type: application/json' \
  -d '{"name":"Golang","slug":"go"}'
```

```json
{
  "_error": {"code": 422, "message": "Insertion failure: 1 document(s) contain(s) error(s)"},
  "_issues": {"slug": "value 'go' is not unique"},
  "_status": "ERR"
}
```

Listar combina `where=` (ver [`querying.md`](querying.md)) com
`pagination` — `_links` sempre aparece, mas `_meta` (`page`/
`max_results`/`total`) só aparece quando a paginação não está
`disabled`. Mais uma tag antes, essa desabilitada, para ter algo para o
`where=` de fato restringir:

```bash
curl -X POST localhost:8080/tags -H 'Content-Type: application/json' \
  -d '{"name":"Rust","slug":"rust","enabled":false}'
curl -G localhost:8080/tags --data-urlencode 'where={"enabled":false}'
```

```json
{
  "_items": [
    {"_id": "6af8d4ede54b1d88e572d5ea", "name": "Rust", "slug": "rust", "enabled": false, "...": "..."}
  ],
  "_links": {"parent": {"href": "/", "title": "home"}, "self": {"href": "tags?where={\"enabled\":false}", "title": "tags"}},
  "_meta": {"max_results": 25, "page": 1, "total": 1}
}
```

## Referência

```yaml
resources:
  - name: tags            # obrigatório; vira Resource.Name e Resource.URL
    collection: tags       # coleção no Mongo (env.Store(collection))
    item_title: tag        # opcional; o padrão é o nome sem "s" no final

    schema:
      <campo>:
        type: string | boolean | integer | number | dict | list | objectid | datetime
        required: true
        nullable: true
        unique: true
        default: <qualquer valor YAML>
        allowed: [a, b, c]
        min_length: 1
        max_length: 100
        min: 0            # number
        max: 100          # number
        not_empty: true
        allow_empty: true
        regex: '^[\w-]+$' # compilado; erro de regex falha o build
        allow_unknown: true  # dict: aceita chaves fora de schema
        schema: {...}        # dict: valida as chaves (recursivo)
        items: {...}         # list: valida cada elemento (recursivo)

    allow_unknown: true   # aceita campos fora de schema no documento
    additional_lookup:
      field: slug          # GET /<resource>/<valor> busca por este campo
      pattern: '^[\w-]+$'  # regex que distingue um lookup de um _id

    resource_methods: [get, post]          # GET/POST na coleção
    item_methods: [get, patch, put]        # GET/PATCH/PUT no item
    public_methods: [get]                  # sem autenticação
    public_item_methods: [get]

    pagination:
      default: 25
      limit: 50
      optimize_for_speed: false  # true: sem contagem total (_meta.total)
      disabled: false            # true: devolve tudo, sem paginação

    soft_delete: true      # DELETE marca _deleted em vez de remover
    cache_control: "max-age=20"
    cache_expires: 20
```

Nomes de método aceitam qualquer caixa (`get` ou `GET`).

`resource_methods`/`item_methods` decidem quais métodos HTTP existem
de fato (um não listado é `405`); `public_methods`/
`public_item_methods` só restringem mais ainda, de "precisa de token"
para "não precisa" — um método em `public_methods` mas fora de
`resource_methods` continua `405`, nunca um buraco público para algo
não listado. No exemplo acima, `GET` é público nas duas rotas,
`POST`/`PATCH`/`PUT` sempre precisam de token.

## O que não tem equivalente em YAML

Dois campos de `rest.Field`/`Resource` carregam código Go arbitrário,
não dado — e por isso não dá para expressá-los aqui:

- **`Field.Check`**: validação personalizada de um campo (ex.: checar
  contra outra API).
- **`Resource.Validate`**: validação do documento inteiro depois do
  schema.

Fora do schema, `Resource.Datasource` (filtros/projeção `bson.M`) e todos
os `Hooks` (`OnInsert`, `OnUpdate`, outbox, busca, auditoria...) também
ficam de fora do loader — um recurso que precisa de qualquer um desses
continua em Go. Um recurso construído a partir do YAML é um
`*rest.Resource` normal, então nada impede anexar hooks nele em Go
antes de registrar (`TestBuiltResourceComposesWithConditionalOutbox`, em
`declarative/declarative_test.go`, faz exatamente isso).

## Visibilidade no Swagger

O Swagger vem habilitado por padrão. Configure `disableSwagger: true`
(alias: `disable_swagger`) no recurso para ocultar somente sua documentação.
Omitido ou `false` inclui o recurso. Aliases conflitantes impedem a construção.
Veja a [documentação Swagger automática](swagger.md).
