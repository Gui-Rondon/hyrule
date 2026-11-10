[English](../resources-yaml.md)

# Resource via YAML

`internal/declarative` carrega um ou mais recursos de um arquivo YAML;
`cmd/hyrule-serve` os registra sem nenhuma linha de Go específica do
domínio. Cobre o caso comum — um recurso só de schema, sem hooks nem
endpoints custom (`examples/blog-apis/services/authors-api` e `tags-api`
são exatamente esse caso, hoje escritos em Go). Quando o recurso precisa
de lógica própria, ele continua sendo um binário Go com `app.Main`, como
`examples/blog-apis/services/posts-api`.

```bash
RESOURCES_FILE=resources.yaml PORT=8080 go run ./cmd/hyrule-serve
```

## Referência

```yaml
resources:
  - name: tags            # obrigatório; vira Resource.Name e Resource.URL
    collection: tags       # coleção no Mongo (env.Store(collection))
    item_title: tag        # opcional; default é o nome sem "s" no final

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

## O que não tem equivalente em YAML

Dois campos de `evecompat.Field`/`Resource` carregam código Go arbitrário,
não dado — e por isso não dá pra expressá-los aqui:

- **`Field.Check`**: validação personalizada de um campo (ex.: checar
  contra outra API).
- **`Resource.Validate`**: validação do documento inteiro depois do
  schema.

Fora do schema, `Resource.Datasource` (filtros/projeção `bson.M`) e todos
os `Hooks` (`OnInsert`, `OnUpdate`, outbox, busca, auditoria...) também
ficam de fora do loader — um recurso que precisa de qualquer um desses
continua em Go. Um recurso construído a partir do YAML é um
`*evecompat.Resource` normal, então nada impede anexar hooks nele em Go
antes de registrar (`internal/declarative/declarative_test.go`'s
`TestBuiltResourceComposesWithConditionalOutbox` faz exatamente isso).
