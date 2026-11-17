[English](README.md)

# standalone-tags-api

A prova da Fase 2 do caminho Framework do Hyrule (ver
[`website/docs/reference/roadmap.md`](../../website/i18n/pt-BR/docusaurus-plugin-content-docs/current/reference/roadmap.md)):
um resource com schema declarado em YAML mais um hook pequeno em Go
(`audit`), construído por um módulo que depende do Hyrule do jeito que o
repositório de um time externo dependeria — `go get`, não um import de
nada dentro de `internal/` (não tem mais nada lá pra isso: ver o
[ADR-0001](../../website/i18n/pt-BR/docusaurus-plugin-content-docs/current/reference/architecture-decisions.md#adr-0001--superfície-pública-do-framework)).

Diferente do [`examples/blog-apis`](../blog-apis), este diretório tem seu
próprio `go.mod` e `go.sum` — um módulo Go separado, não um pacote dentro
do módulo Hyrule. Essa fronteira de módulo é o ponto central: é o que
torna isso um teste de verdade de "um projeto externo consegue mesmo
depender disso", não só "isso compila dentro do mesmo `go.mod`".

## Por que tem uma diretiva `replace`

O Hyrule ainda não tem tag (a Fase 1 acabou de colocar os caminhos de
import públicos — ver o roadmap). O `go.mod` aqui diz:

```
require github.com/Gui-Rondon/hyrule v0.0.0-00010101000000-000000000000
replace github.com/Gui-Rondon/hyrule => ../..
```

Um consumidor externo de verdade, quando uma versão for taggeada,
escreveria em vez disso:

```
require github.com/Gui-Rondon/hyrule v0.101.0
```

e tiraria a linha `replace` completamente — `go get
github.com/Gui-Rondon/hyrule` faria exatamente isso.

## Rodando localmente

```bash
# a partir da raiz do repo
make env-up    # Mongo (27018)

cd examples/standalone-tags-api
echo '{"auth": {"enable": "false"}}' > config.json
MONGO_CONNECTION_STRING='mongodb://localhost:27018/standalone_demo?directConnection=true' \
  CONFIG_FILE=config.json PORT=8080 go run .
```

```bash
curl -X POST localhost:8080/tags -H 'Content-Type: application/json' -d '{"name":"Go","slug":"go"}'
curl localhost:8080/tags/go
```

## Testando

```bash
cd examples/standalone-tags-api
MONGO_TEST_URI='mongodb://localhost:27018/?directConnection=true' go test ./...
```

O `testkit` dá ao teste seu próprio banco Mongo descartável — nada disso
muda pra um módulo fora do `go.mod` do próprio Hyrule.


## Piloto local opcional de consulta de tags

`internal/taglookup` implementa somente `tags.lookup`, sem registrar rota ou mudar
`main`. O executor exige serviço registrado, escopo fixo, identidade/política e
auditoria obrigatória. Aceita apenas JSON limitado com `slug`, consulta uma tag
habilitada e retorna `name`, `slug`, `enabled`. Recursos com `OnPreGet` são recusados;
hooks de resposta continuam ativos e passam pela verificação de saída.

`NewJWTIdentity` verifica tokens RS256 com chaves confiáveis por `kid`, issuer,
audience, expiração e subject obrigatórios. A política exige scope exato
`tags:read`, versão e recurso corretos. `WithToken` transporta credencial não
confiável, sem conceder identidade. `LoadJWTIdentity(JWTFileConfig)` carrega a chave
pública PEM provisionada pela aplicação; não há fallback de autenticação legado.

`OpenSQLiteAudit(ctx, path)` oferece auditoria persistente local em arquivo separado
dos dados, com WAL/FULL e append confirmado antes da resposta. Fechar o sink no
shutdown. Arquivos novos usam 0600; retenção, backup, permissões existentes e
proteção do volume pertencem ao deployment. O log não é imutável nem exactly-once.

```sh
go test -race ./internal/taglookup
```

Os testes usam SQLite e assinaturas RSA reais, sem serviços externos. O target
raiz `make standalone-example-test` já os executa. A consulta tem orçamento de dois
segundos mais até um segundo para auditoria independente, sem herdar tokens/claims.
Dependências, readers e hooks precisam cooperar com cancelamento; o executor não
isola código Go arbitrário. Não há servidor de ferramentas, transporte externo ou
credenciais de produção incluídos.

Ver o [contrato do piloto](../../website/i18n/pt-BR/docusaurus-plugin-content-docs/current/reference/read-pilot.md)
para composição, limites e critérios de aceitação.


`NewHTTPHandler(service, identity, audit.Write)` oferece descoberta GET e consulta
POST em `/tools/tags.lookup`, com header Bearer único e auditoria obrigatória.
Cookie, query e token herdado no contexto não autenticam requests.
`NewLocalHTTPServer("127.0.0.1:8081", handler)` cria, sem iniciar, servidor restrito
a loopback com limites de leitura/escrita/header. A aplicação controla startup e
shutdown. Não há rota automática, deploy remoto ou TLS provisionado; o adapter MCP local
opcional está descrito abaixo.
Os testes incluem socket loopback real e timeout de corpo parcial; o ambiente
precisa permitir escuta local.


GET autorizado e `Executor.Discover` retornam contratos JSON Schema 2020-12 de
entrada/saída, limites reais, regras exclusivas do servidor e hash SHA-256
determinístico. Tipos vêm do schema do recurso; as restrições de slug e resultado
são da operação. Descoberta autoriza/audita sem consultar dados, com metadados novos
por chamada. Hash/schema não substituem autorização. Os contratos são comparados
com o parser real e os resultados SQLite.


O adapter opcional `internal/tagmcp` usa SDK oficial Go v1.8.0 e protocolo
2025-11-25. `NewServer`/`RunStdio` reutilizam descoberta e executor, com credencial
local explícita, autorização e auditoria por operação. Os testes usam cliente SDK
real em memória e subprocesso stdio. `main` não mudou; não há MCP HTTP/OAuth ou
deploy remoto. Ver o [piloto MCP](../../website/i18n/pt-BR/docusaurus-plugin-content-docs/current/reference/mcp-pilot.md).


O executável opt-in pode ser compilado com
`go build -o .local/tags-mcp ./cmd/tags-mcp` (criar `.local` antes).
`--help` lista as nove variáveis obrigatórias em stderr. O comando recebe paths
explícitos de schema, dados, auditoria, chave e token; abre somente um banco SQLite
existente em modo de leitura. Não gera dados/tokens nem altera o servidor REST.
O [guia MCP](../../website/i18n/pt-BR/docusaurus-plugin-content-docs/current/reference/mcp-pilot.md)
inclui configuração `mcpServers`, troubleshooting e limites de compatibilidade.
`go test -race ./cmd/tags-mcp` testa o binário real com cliente SDK e fixtures locais.

Recuperação opcional de documentação com pgvector: veja [configuração e limites](../../website/i18n/pt-BR/docusaurus-plugin-content-docs/current/reference/knowledge-pilot.md).

Para mudanças da fonte, bloqueio imediato por ACL/exclusão e indexação em segundo plano, veja [fonte versionada/outbox](../../website/i18n/pt-BR/docusaurus-plugin-content-docs/current/reference/knowledge-sync.md) (`enqueue` e `worker`).

O [produtor de artigos e CLI de curadoria](../../website/i18n/pt-BR/docusaurus-plugin-content-docs/current/reference/knowledge-producer.md) conecta CRUD concreto do Service a fonte/outbox por hooks transacionais.
