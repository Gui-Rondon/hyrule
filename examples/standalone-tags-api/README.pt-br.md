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
