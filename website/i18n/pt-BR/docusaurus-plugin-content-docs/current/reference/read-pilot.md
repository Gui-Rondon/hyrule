---
sidebar_position: 23
---

# Piloto de consulta — lookup de tags

**Status:** piloto local implementado no pacote interno do exemplo standalone,
2026-11-16. Identidade JWT RS256 e política de scope estão integradas como dependências
opcionais; auditoria pode usar o sink SQLite persistente incluído. Não há registry genérico, configuração
de provedor de produção nem deploy remoto. O transporte HTTP local é opt-in.
Isso não conclui a Fase 7 nem comprova prontidão de produção.

## Consumidor e recurso

Usar um consumidor técnico local que busca uma tag pelo slug exato antes de
sugerir tags para um rascunho. É uma demonstração reproduzível com dados
sintéticos, não um time de produção identificado nem política editorial aprovada.
O schema vem de `examples/standalone-tags-api/resources.yaml`; o recurso Go em
`examples/blog-apis/services/tags-api/tags.go` confirma que `name`, `slug` e
`enabled` existem e que slug é único.

O standalone normalmente usa Mongo; o ensaio conecta seu schema compilado ao
SQLite em memória. GET público no exemplo blog não autoriza ferramentas
implicitamente. Preservar as políticas REST dos dois exemplos.

## Contrato da operação

Nome da ferramenta: `tags.lookup`, versão de contrato `1`. Somente leitura e
registro explícito. A entrada contém exatamente uma string obrigatória `slug`,
com 1–128 caracteres ASCII correspondentes a `^[A-Za-z0-9_-]+$`. Rejeitar
propriedades extras, valor vazio/tipo incorreto, JSON inválido e chaves duplicadas.
A busca é exata e sensível a maiúsculas; não normalizar nem truncar silenciosamente.
Limitar o corpo a 1 KiB antes de decodificar.

```json
{"slug":"go"}
```

O servidor monta o filtro: slug igual à entrada E `enabled=true`. O cliente não
escolhe recurso, tenant, datasource, backend, `where`, SQL, operadores, ordenação,
projeção, página ou limite. Usar `Adapter.ParseWhere`/`ParseProjection` para a
representação do serviço vinculado, depois `Service.List` com `Page=1`,
`MaxResults=1`, `ShowDeleted=false`. Assim permanecem filtros obrigatórios do
datasource e hooks de leitura. Impor deadline máximo de dois segundos, respeitando
um prazo menor do chamador. Não repetir automaticamente.

Solicitar apenas `name`, `slug`, `enabled` na projeção; copiar esses campos para
um novo objeto depois dos hooks. Não serializar `Doc` inteiro. Validar slug,
enabled e tipos retornados antes de responder; recusar resultado inconsistente
ou alterado por hook. Limitar resposta a 16 KiB sem truncar JSON. Esses limites
são escolhas do piloto, não defaults do framework.

```json
{"found":true,"tag":{"name":"Go","slug":"go","enabled":true}}
```

Registros ausentes e desabilitados retornam igualmente `{"found":false}`. Não
retornar IDs, ETags, timestamps, auditoria, notas internas, totais, outros registros
ou erros brutos do adapter. Falha de autorização/infraestrutura não é ausência.
Uma resposta ausente contém só `found`; uma encontrada contém só `found` e `tag`.

## Política de acesso proposta

| Verificação | Regra |
|---|---|
| Habilitação | Desligado por padrão; aplicação registra explicitamente recurso/ferramenta. |
| Identidade | Fronteira confiável de autenticação fornece subject verificado não vazio; argumentos não fornecem principal/permissões. |
| Permissão | Política da aplicação concede exatamente `tags:read` para `tags.lookup` versão 1 neste recurso vinculado; caso contrário, negar. É proposta do piloto, não convenção de claims já existente no Hyrule. |
| Escopo | Um dataset sintético da aplicação por instância; vincular serviço/banco no startup, nunca pela entrada. |
| Descoberta | Mostrar apenas a principal autorizado; execução revalida autorização, inclusive em chamada direta por nome. |
| Campos | Retornar só os três campos e impor filtro enabled no servidor. |
| Escritas | Nenhum insert, patch, replace, delete, aprovação ou dry-run exposto. |
| Auditoria | Registrar ator, ferramenta/versão, decisão de política, correlação, duração e resultado; redigir credenciais/conteúdo. Falha de auditoria obrigatória impede sucesso. |
| Falhas | Distinguir entrada inválida, não autenticado, proibido, prazo/cancelamento, indisponibilidade/erro interno e resultado ausente; não expor DSN/SQL/token. |

O schema tags não tem campo tenant. Este piloto não demonstra isolamento entre
tenants em banco compartilhado. Rejeitar seleção de tenant/dataset por argumentos.
Multitenancy em produção exige decisão separada da aplicação: armazenamento
vinculado por tenant ou schema/filtro impondo propriedade, mais testes de
isolamento antes da exposição a múltiplos tenants.

`platform/auth.Authenticator.Authenticate` retorna claims vazios com auth
desabilitada; isso não satisfaz a identidade exigida. `auth.WithClaims` armazena
contexto, mas não comprova verificação. O caminho de chave estática legado não
estabelece sozinho todos os checks de issuer/audience/claims obrigatórios deste
piloto. Selecionar emissor/audiência verificados e exigir expiração/subject na
fronteira da aplicação. Não enfraquecer auth REST globalmente nem presumir que
`Service` a executa. Este desenho não configura provedor de identidade nem endpoint
remoto.

## Limite de implementação

O incremento local usa um adapter de aplicação restrito a esta operação, com
fronteira confiável de identidade/política injetada e sink de auditoria explícito.
Não começar por registry genérico antes de provar a necessidade. Não são
necessários exportação automática de CRUD, linguagem nova de schema, execução
arbitrária, provedor de modelos ou SDK MCP. Testes locais podem injetar principais
por verificador exclusivo de teste; isso não autentica um serviço publicado.

Antes de uso externo, escolher e validar autenticação/transporte reais. Ausência
de verificador/política, auth desabilitada e subject vazio negam acesso. Deadlines
limitam consulta e tratamento da aplicação; verificar cancelamento no storage.
Contratos de saída/erro pertencem ao piloto, sem substituir envelopes REST Eve.

## Casos de aceitação do próximo incremento

- Descoberta e chamada direta sem autorização falham antes de acessar storage.
- Ausência de identidade, auth desabilitada, subject vazio, recurso fora do escopo
  ou falta de permissão negam acesso. Claims não confiáveis em argumentos/contexto
  não concedem permissões.
- Slug exato habilitado retorna somente os campos permitidos. Desabilitado e
  inexistente retornam a mesma forma de ausência.
- Chaves extras/duplicadas, payload grande, slug/tipo inválido e tentativa de
  sobrescrever filtro/projeção/tenant falham antes do storage.
- Limite continua um com paginação de recurso desabilitada. Filtros obrigatórios
  e hooks permanecem efetivos; hooks não burlam enabled/slug/regras de saída.
- Deadline, cancelamento, falha de banco, tamanho/tipo de saída e falha de auditoria
  têm erros explícitos e nunca viram `found=false`.
- Nenhum método de mutação é chamado; registros semeados permanecem inalterados.

## Executor local implementado — 2026-11-16

O pacote `examples/standalone-tags-api/internal/taglookup` fornece:

- `New(Dependencies)`, exigindo serviço, escopo fixo, `Verify`, `Authorize` e `Audit`.
- `Describe(ctx)`, com a mesma autorização da chamada direta, sem consulta ao banco.
- `Execute(ctx, io.Reader)`, com JSON estrito, rejeição de extras/duplicatas,
  filtro/projeção fixos e saída reconstruída depois dos hooks.
- Erros sentinela para identidade, permissão, entrada, storage, saída,
  cancelamento/deadline e auditoria, sem propagar erros brutos das dependências.

O pacote é interno ao módulo standalone; não altera `rest`/`app` e não é ligado ao
`main`. Consumidores externos não recebem uma nova API pública do framework.
O exemplo continua iniciando como antes, sem expor ferramentas automaticamente.

**Limites explícitos.** Recursos com `OnPreGet` são recusados na construção,
porque esses hooks podem alterar filtro/paginação e invalidar o limite de uma
linha. Hooks `OnFetchedResource` continuam executando; slug, enabled, tipos e
tamanho são revalidados depois. Configuração do recurso deve ficar imutável e
hooks/dependências precisam ser confiáveis; o executor não isola código Go
arbitrário nem desfaz efeitos externos de hooks.

A execução tem orçamento de dois segundos, respeitando prazo menor do chamador.
Auditoria recebe contexto novo, sem token/claims herdados, independente de
cancelamento e com orçamento adicional de um segundo, inclusive para registrar falhas/cancelamentos. Falha de auditoria
remove a saída e retorna `ErrAudit`; usar `errors.Is` para classificar erros que
podem combinar auditoria e falha original. O evento descreve o resultado da
operação antes da entrega da resposta; duração exclui a persistência da auditoria.
`io.Reader`, hooks, callbacks e storage devem cooperar com cancelamento: não há
promessa de interrupção forçada de código bloqueado. Transporte futuro precisa
configurar timeout de leitura próprio.

Os testes usam o YAML existente e SQLite real. Cobrem negação antes do storage,
escopo incorreto, descoberta, entrada inválida, limite de uma linha mesmo com
paginação desabilitada, filtro obrigatório, campos extras de hooks, saída inválida,
falha de storage/auditoria, cancelamento/deadline, ausência de mutações e chamadas
concorrentes com auditoria isolada. Os primeiros testes injetavam verificador sintético; os testes posteriores de
identidade usam tokens RSA assinados, conforme a integração abaixo.

```sh
cd examples/standalone-tags-api
go test -race ./...
```

Continuam pendentes a configuração do provedor de produção, deploy remoto,
política de produção e isolamento multi-tenant. A sugestão de registry/MCP só deve
avançar depois de provar o consumidor concreto.

## Integração JWT e política — 2026-11-16

`NewJWTIdentity(JWTConfig)` implementa a fronteira de identidade/política usando a
dependência já fixada `golang-jwt/jwt/v5`. Configuração exige issuer, audience,
escopo do recurso e chaves públicas RSA confiáveis com pelo menos 2048 bits,
indexadas por `kid`. O construtor copia mapa e módulos RSA, evitando que mutações
posteriores da configuração alterem a confiança. Não existe modo desabilitado.

`WithToken(ctx, compactJWT)` apenas transporta uma credencial **não confiável**,
sem prefixo Bearer; não concede identidade. `Verify` aceita somente RS256, assinatura
válida com `kid` conhecido, issuer/audience exatos, expiração obrigatória válida e
`sub` não vazio de até 256 bytes. `nbf` e `iat`, quando presentes, também são
validados; não há tolerância adicional de relógio. Limite do token: 8 KiB.

`Authorize` revalida o token e compara subject, `tags.lookup`, versão `1`, permissão
`tags:read` e escopo do recurso configurado. O claim `scope` deve ser uma string
com scopes separados por espaços e conter exatamente `tags:read`. Wildcards,
substrings, `roles`, `scp`, username e claims de contexto não concedem permissão.
Esta convenção é explícita do piloto; um provedor que use outro formato exige
mapeamento revisado, sem fallback automático.

Vincular `identity.Verify` e `identity.Authorize` às dependências do executor e
usar o mesmo escopo de recurso em ambos os construtores. A aplicação fornece a
chave pública, issuer/audience reais e credencial recebida; estes valores não vêm
dos argumentos da ferramenta. `auth.WithClaims` não é consultado. O autenticador
REST legado e a configuração do exemplo não foram alterados.

```go
identity, err := taglookup.NewJWTIdentity(taglookup.JWTConfig{
    Issuer: issuer, Audience: audience, Scope: resourceScope,
    Keys: map[string]*rsa.PublicKey{keyID: publicKey},
})
// Handle err before constructing the executor.
executor, err := taglookup.New(taglookup.Dependencies{
    Service: tagsService, Scope: resourceScope,
    Verify: identity.Verify, Authorize: identity.Authorize, Audit: auditSink,
})
// Handle err; compactJWT comes from the trusted application's credential input.
ctx = taglookup.WithToken(ctx, compactJWT)
output, err := executor.Execute(ctx, strings.NewReader(`{"slug":"go"}`))
```

O trecho ilustra composição dentro do módulo standalone; não registra rota nem
oferece verificador fictício permissivo. O pacote continua interno. Não há acesso
à rede, descoberta/JWKS, interpretação de `jku`/`x5u` nem fallback para outra chave.
Para rotação, construir nova instância com conjunto de chaves provisionado pela
aplicação. Revogação/introspecção online e concessões fora do scope assinado não
foram implementadas; a política é avaliada a cada chamada com a configuração atual.

Testes assinam tokens reais com chaves RSA temporárias e usam SQLite real. Cobrem
assinatura/chave/algoritmo inválidos, claims ausentes/incorretos, expiração, nbf/iat
futuros, claims forjados no contexto, permissão ausente, troca de subject, versão,
ferramenta/recurso e mutação da configuração de chaves. Nenhum token rejeitado
consulta storage. Isso valida a integração criptográfica local, não conectividade
com um provedor de produção já configurado.

## Provisionamento e auditoria persistente — 2026-11-16

`LoadJWTIdentity(JWTFileConfig)` lê uma chave pública RSA PEM de um arquivo
configurado pela aplicação (até 64 KiB). Exige issuer, audience, escopo e `kid`
explícitos; chave privada, arquivo inválido ou configuração ausente falham no
startup. O arquivo é lido uma vez; substituí-lo não altera uma instância existente.
Para rotação com sobreposição, usar `NewJWTIdentity` com as duas chaves provisionadas.
Não há credencial real, emissor ou caminho de produção escolhido neste exemplo.

`OpenSQLiteAudit(ctx, path)` abre um arquivo SQLite persistente separado dos dados
do recurso, configura WAL e `synchronous=FULL` e cria `tags_lookup_audit`. `Write`
faz um append com sequência, timestamp UTC e metadados sanitizados em JSON.
O commit da gravação precisa terminar antes de o executor liberar a saída.
O chamador é dono do sink e deve fechá-lo no shutdown. Arquivos novos usam modo
0600; arquivos existentes preservam as permissões definidas pelo deployment.

A composição abaixo não registra rota nem muda o `main` do exemplo:

```go
func configuredLookup(ctx context.Context, service *rest.Service,
    cfg taglookup.JWTFileConfig, auditPath string,
) (*taglookup.Executor, *taglookup.SQLiteAudit, error) {
    identity, err := taglookup.LoadJWTIdentity(cfg)
    if err != nil {
        return nil, nil, err
    }
    audit, err := taglookup.OpenSQLiteAudit(ctx, auditPath)
    if err != nil {
        return nil, nil, err
    }
    executor, err := taglookup.New(taglookup.Dependencies{
        Service: service, Scope: cfg.Scope,
        Verify: identity.Verify, Authorize: identity.Authorize,
        Audit: audit.Write,
    })
    if err != nil {
        audit.Close()
        return nil, nil, err
    }
    return executor, audit, nil
}
```

Testes com SQLite real confirmam persistência após reabertura, append concorrente,
registro de chamadas canceladas e retenção da resposta quando o arquivo fecha ou
a auditoria falha. Um ensaio integrado usa chave PEM provisionada e token RSA
assinado, consulta a tag e relê os eventos persistidos sem token ou argumentos.

O arquivo é um log local com commit SQLite, não um ledger imutável ou entrega
exactly-once. Uma falha depois do commit pode levar o chamador a repetir a leitura
e produzir outro evento. Retenção, backup, proteção de acesso, permissões de
arquivos existentes e proteção do volume são responsabilidades do deployment.
Não usar o arquivo do recurso como arquivo de auditoria. A disponibilidade de WAL
e sincronização depende do filesystem; falhas de configuração são recusadas.
Deploy remoto, credenciais/provedor de produção e política multi-tenant
continuam pendentes. O pacote não inicia servidor nem executa deploy.

## Transporte HTTP local opcional — 2026-11-16

`NewHTTPHandler(service, identity, audit.Write)` cria um handler usando
`JWTIdentity` configurado, escopo fixo e auditoria obrigatória. Não aceita
verificador permissivo nem credencial de cookie/query/contexto herdado. Exige
exatamente um header `Authorization: Bearer <compactJWT>` por chamada; `main`
continua sem registrar rota ou iniciar servidor de ferramentas automaticamente.

| Requisição | Resultado autorizado |
|---|---|
| `GET /tools/tags.lookup` | Descritor com `name`, `version`, `read_only`; não consulta storage. |
| `POST /tools/tags.lookup` | Entrada JSON `{"slug":"go"}` e resultado do executor. |

POST exige `application/json`. Query parameters, corpo em GET, chaves extras ou
duplicadas são recusados; o limite de 1 KiB também vale para corpo de tamanho
desconhecido/chunked. Falhas de metadados/entrada passam por identidade, política e
auditoria antes de responder, sem storage. Rotas desconhecidas retornam 404;
métodos fora de GET/POST retornam 405 com `Allow`, sem executar ferramenta.
Não há CORS, autenticação por cookie, redirects ou fallback REST.

| Falha | HTTP |
|---|---|
| Credencial ausente/inválida | 401 com challenge Bearer |
| Permissão ausente | 403 |
| Entrada inválida | 400 |
| Corpo maior que 1 KiB | 413 |
| Content-Type incompatível | 415 |
| Leitura de corpo expirada / chamada cancelada | 408 |
| Deadline do executor | 504 |
| Falha de auditoria ou indisponibilidade | 503 |
| Saída inconsistente | 500 |

Erros contêm somente código estável; nunca token, DSN ou erro bruto. Todas as
respostas usam `Cache-Control: no-store`, `Vary: Authorization`, JSON e `nosniff`.
Falha de auditoria tem precedência e retém tanto resultado quanto descritor.
Tag ausente/desabilitada continua HTTP 200 com `{"found":false}`.

`NewLocalHTTPServer("127.0.0.1:8081", handler)` cria um `http.Server`, sem
iniciá-lo. Aceita apenas endereço IP numérico de loopback explícito; wildcard,
hostname e bind remoto são recusados. Aplica budgets de leitura/header de dois
segundos, escrita de cinco segundos, idle de 30 segundos e headers de 16 KiB.
A aplicação é dona do listener, shutdown e do fechamento do sink de auditoria.
Ao montar o handler em outro servidor, aplicar limites equivalentes de leitura e
escrita; somente contexto Go não interrompe um reader bloqueado.

```go
handler, err := taglookup.NewHTTPHandler(tagsService, identity, audit.Write)
if err != nil {
    return err
}
server, err := taglookup.NewLocalHTTPServer("127.0.0.1:8081", handler)
if err != nil {
    return err
}
// The application may call server.ListenAndServe and owns shutdown.
```

Testes cobrem GET/POST autorizados e negados, isolamento de credenciais,
ausência de fallback por cookie/query, corpo chunked grande, falhas sanitizadas e
auditoria antes de liberar saída. Um teste abre conexão loopback real com token
RSA e auditoria SQLite, incluindo cliente com corpo parcial que expira no servidor.
Não há deploy remoto, TLS provisionado, MCP ou credenciais de produção incluídos.

## Ensaio local da consulta e limites

Um programa temporário compilou o YAML existente, semeou dois registros sintéticos
em SQLite e chamou `Service.List` real via `restsqlite`. Um hook de escrita adicionou
nota interna para demonstrar por que reconstruir a saída. Não usou listener,
credenciais, dados de produção ou outra infraestrutura.

```text
go => {"found":true,"tag":{"enabled":true,"name":"Go","slug":"go"}}
hidden => {"found":false}
missing => {"found":false}
```

Isso comprova localmente o formato de filtro/projeção limitado. **Não** comprova
parser de entrada, identidade, permissão, auditoria, limites de saída nem falhas
propostas: eram requisitos de aceitação naquele ensaio. O executor local posterior está
descrito acima; o provedor de produção e o deploy remoto permanecem pendentes.

Para repetir, criar o diretório e salvar o bloco Go abaixo em
`/tmp/hyrule-read-pilot/main.go`; executar na raiz do repositório:

```sh
go run /tmp/hyrule-read-pilot/main.go
```

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/Gui-Rondon/hyrule/declarative"
	"github.com/Gui-Rondon/hyrule/platform/sqlite"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restsqlite"
	"os"
	"time"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:")
	must(err)
	defer db.Close()
	raw, err := os.ReadFile("examples/standalone-tags-api/resources.yaml")
	must(err)
	file, err := declarative.Parse(bytes.NewReader(raw))
	must(err)
	res, err := file.Resources[0].Build(nil)
	must(err)
	adapter, err := restsqlite.NewAdapter(ctx, db, "tags", res)
	must(err)
	res.Adapter = adapter
	// Seed only synthetic data. The hook deliberately adds a server-only field
	// to demonstrate why the result must be allowlisted after Service hooks.
	res.Hooks.OnInsert = append(res.Hooks.OnInsert, func(_ context.Context, docs []rest.Doc) error {
		for _, d := range docs {
			d["internal_note"] = "synthetic secret"
		}
		return nil
	})
	api := rest.New(rest.Options{Transactor: sqlite.Transactor{DB: db}})
	svc := api.Register(res)
	_, err = svc.Insert(ctx, []rest.Doc{{"name": "Go", "slug": "go", "enabled": true}, {"name": "Hidden", "slug": "hidden", "enabled": false}}, rest.WriteOptions{})
	must(err)
	projection, err := adapter.ParseProjection(`{"name":1,"slug":1,"enabled":1}`)
	must(err)
	for _, slug := range []string{"go", "hidden", "missing"} {
		query, _ := json.Marshal(map[string]any{"slug": slug, "enabled": true})
		filter, err := adapter.ParseWhere(string(query), res)
		must(err)
		readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		result, err := svc.List(readCtx, &rest.ListQuery{Filter: filter, Projection: projection, Page: 1, MaxResults: 1})
		cancel()
		must(err)
		if len(result.Docs) > 1 {
			panic("unbounded result")
		}
		out := map[string]any{"found": len(result.Docs) == 1}
		if len(result.Docs) == 1 {
			d := result.Docs[0]
			out["tag"] = map[string]any{"name": d["name"], "slug": d["slug"], "enabled": d["enabled"]}
		}
		encoded, err := json.Marshal(out)
		must(err)
		fmt.Printf("%s => %s\n", slug, encoded)
	}
}
```
