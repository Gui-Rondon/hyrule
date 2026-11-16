# Documentação Swagger automática

O Swagger vem **habilitado por padrão** para todo recurso registrado com
`rest.API.Register`, tanto em Go quanto em YAML. Não exige anotações, geração
de código, conexão ao banco nem instalação de Node no projeto consumidor.

| URL | Conteúdo |
|---|---|
| `/api/docs` | Swagger UI com arquivos locais |
| `/swagger` | Redirecionamento para `/api/docs` |
| `/api/openapi.json` | Especificação OpenAPI 3.1.0 |

A página e o JSON são públicos. Use **Authorize** para informar um token Bearer
nas operações protegidas; métodos públicos continuam públicos. A interface não
persiste tokens nem envia a especificação para um validador externo.

## Oculte um recurso

Em Go, use `DisableSwagger`:

```go
env.API.Register(&rest.Resource{
    Name:           "internal-events",
    Store:          env.Store("internal-events"),
    DisableSwagger: true,
})
```

Em YAML, use `disableSwagger` ou o alias `disable_swagger`:

```yaml
resources:
  - name: tags
    collection: tags
    # Omitido ou false: incluído no Swagger por padrão.
    disableSwagger: false
    schema:
      name: {type: string, required: true}
  - name: internal-events
    collection: internal-events
    disableSwagger: true
```

Valores conflitantes entre os aliases impedem a construção do recurso.
`true` oculta suas operações e schemas da especificação. CRUD, hooks,
autenticação e a listagem na raiz da API continuam funcionando. A opção
controla visibilidade na documentação, não permissões de acesso.
Se todos estiverem ocultos, a página permanece disponível com uma especificação vazia.
Esta versão não inclui uma opção global para desabilitar a documentação.

## Contrato gerado

A especificação usa a URL e os valores padrão efetivos de cado recurso: métodos
CRUD habilitados, schemas aninhados e listas, schemas separados para escrita
completa/PATCH/resposta, POST individual e em lote, consultas, paginação,
lookup adicional somente para leitura, ETag/If-Match, erros e autenticação
por método. Funciona com Mongo e Postgres. IDs de rota são strings, sem
presumir ObjectID em todos os adapters. Datas seguem `DATE_FORMAT` do Eve,
sem anunciar respostas RFC 3339.

Unicidade, expressões regulares Go e callbacks são regras do servidor:
`x-hyrule-unique`, `x-hyrule-regex` e `x-hyrule-custom-validation` preservam
esses detalhes. Hooks e validadores podem impor regras adicionais ao schema.
Endpoints de `API.Handle`, verificações de saúde e métricas ficam fora desta versão.
Os registros e definições dos recursos são configuração de inicialização;
não devem ser alterados simultaneamente com requisições HTTP.

## Implantação

Swagger UI v5.35.0 e sua licença ficam embutidos no binário. Não há dependência
de CDN ou internet para servir a documentação. URLs relativas preservam o
prefixo externo de um proxy que o remove antes de encaminhar a requisição.

Os caminhos `/api/docs` (incluindo arquivos estáticos), `/api/openapi.json` e `/swagger`
são reservados. Colisões em recursos ou rotas personalizadas geram panic claro
no registro, seguindo o comportamento de `http.ServeMux`. Um recurso com URL
`api` também conflita, pois sua rota de item cobre os caminhos de documentação;
escolha outra URL. Os caminhos ficam reservados mesmo com todos os recursos
ocultos por `DisableSwagger: true`.
