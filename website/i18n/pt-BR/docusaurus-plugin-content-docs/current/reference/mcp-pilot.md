---
sidebar_position: 24
---

# Piloto MCP local

**Status:** adapter local opcional implementado em 2026-11-16, reutilizando o
executor e a descoberta do [piloto tags](read-pilot.md). Credenciais de produção,
adoção por cliente desktop, MCP HTTP/OAuth e deployment multiusuário seguem abertos.

## Protocolo e cliente selecionados

O módulo standalone fixa o SDK oficial `github.com/modelcontextprotocol/go-sdk`
**v1.8.0**, compatível com a versão Go do exemplo. O piloto seleciona o protocolo
**2025-11-25**, com `tools/list` e `tools/call`. As dependências SDK ficam no módulo
standalone; framework e `taglookup` não importam MCP.

| Cliente e transporte | Comportamento verificado |
|---|---|
| Cliente oficial Go SDK v1.8.0, transporte em memória, proposta 2025-11-25 | Negociação, listagem autorizada, chamada e cancelamento. |
| Mesmo cliente, subprocesso stdio e executável compilado `cmd/tags-mcp`, proposta 2025-11-25 | Negociação, listagem, consulta com token assinado e auditoria SQLite persistente. |
| Mesmo cliente, proposta 2026-07-28, em memória | SDK negocia 2025-11-25 e a descoberta funciona. É fallback, não validação do protocolo novo. |

Esta é a matriz explícita do piloto; não comprova compatibilidade com ChatGPT,
Claude Desktop ou qualquer cliente MCP. A aplicação deve respeitar a versão
negociada. O SDK cuida de negociação, framing, decodificação e cancelamento.
Ver [documentação do SDK v1.8.0](https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/docs/server.md)
e [schema de ferramentas 2025-11-25](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/2025-11-25/schema/2025-11-25/schema.ts).

## Composição

`examples/standalone-tags-api/internal/tagmcp` é um pacote interno separado.
`NewServer(Config)` exige serviço registrado, identidade JWT, escopo fixo, sink de
auditoria obrigatório e fornecedor local de credencial. `RunStdio(ctx, cfg)` cria
o servidor SDK e usa stdin/stdout com limite de 64 KiB por frame recebido.
Argumentos da ferramenta continuam limitados a 1 KiB e saída a 16 KiB.

```go
cfg := tagmcp.Config{
    Service: tagsService, Identity: identity, Scope: resourceScope,
    Audit: audit.Write,
    Token: func(ctx context.Context) (string, error) {
        return credentialStore.Token(ctx)
    },
}
return tagmcp.RunStdio(ctx, cfg)
```

A aplicação provisiona identidade/credenciais e fecha recurso/auditoria no shutdown.
`Token` fornece um JWT compacto não confiável do dono local deste servidor;
precisa respeitar contexto e concorrência. A obtenção da credencial compartilha
o budget de dois segundos do executor, respeitando prazo menor do chamador.
Cada operação revalida assinatura,
issuer, audience, expiração, subject e scope exato. Usar um servidor por dono local.
Estar no stdin ou concluir initialize não autentica usuário. O handshake anuncia
implementação/capacidade genéricas; definições de ferramenta exigem autorização.

Stdio reserva stdout para frames SDK; logs da aplicação devem ir para stderr.
O `main` não mudou e nenhum processo de ferramenta inicia automaticamente.
Quem usar `NewServer` com outro transporte passa a definir limites de frame/leitura
e autenticação. Não há servidor de autorização HTTP MCP, descoberta OAuth ou
delegação de tokens neste piloto.

## Mapeamento de operações

`tools/list` chama `Executor.Discover` em cada request. Principais autorizados
recebem uma ferramenta com os schemas existentes, hint de leitura e metadados
namespaced de versão, hash, limites e regras do servidor. Não há inferência de
schema ou registry estático paralelo. Descoberta autoriza e confirma auditoria
sem consultar dados; o resultado não vira permissão de sessão em cache. O catálogo
de uma ferramenta não pagina; cursor não vazio é recusado após autorização/auditoria.

`tools/call` envia argumentos JSON brutos para `Executor.Execute`. Chaves duplicadas
e tamanho excessivo passam pela mesma validação de HTTP/chamada local. Sucesso
retorna conteúdo estruturado e texto JSON equivalente. Ferramentas desconhecidas
também passam por identidade/política/auditoria antes de erro de protocolo
sanitizado, sem storage. Estado de requisições multi-round-trip não é suportado.
Métodos de resources/prompts não são expostos.

O output schema MCP exige `type: object` na raiz. A descoberta compartilhada agora
explicita isso, preservando as variantes encontrado/ausente. O hash de conteúdo
muda sem alterar a semântica da consulta. Anotações de leitura são hints; política
e executor continuam sendo os controles de acesso.

## Fronteiras de credencial e erros

O fornecedor de credencial é configuração da aplicação, não `_meta`, argumentos
ou estado de sessão de outro dono local. Metadados de identidade forjados pelo
cliente não concedem permissão. Fornecedor vazio/falho gera falha de autenticação
e auditoria da tentativa negada. Mudança de permissão ou expiração vale na próxima
operação, inclusive em sessão já inicializada.

| Condição | Resultado |
|---|---|
| Identidade ausente/inválida | Erro JSON-RPC -32001, `unauthenticated`. |
| Permissão ausente/recurso incorreto | Erro JSON-RPC -32003, `forbidden`. |
| Ferramenta desconhecida/cursor não suportado | Erro de parâmetros após autorização/auditoria. |
| Argumentos inválidos, falha de consulta/saída, deadline ou auditoria em chamada conhecida | Resultado `isError=true`, texto seguro e nenhum dado estruturado de consulta. |
| Falha de descoberta/auditoria | Erro de protocolo sem schema/hash. |
| Método não suportado | Erro de método inexistente. |

Falha de auditoria prevalece mesmo combinada com entrada inválida. Auditoria recebe
contexto novo sem credenciais e budget próprio de um segundo; precisa confirmar o
commit antes da liberação de metadados/resultados. Não retornar erro bruto, token
ou campo privado. Frames de protocolo malformados são tratados pelo SDK; não são
chamadas bem-sucedidas nem promessa de auditoria de domínio.

## Verificação

```sh
cd examples/standalone-tags-api
go test -race ./internal/tagmcp
```

Testes usam cliente/servidor SDK reais em memória e subprocesso stdio real, tokens
RSA assinados, recurso YAML existente, SQLite real e auditoria persistente. Cobrem
schemas/hash iguais à descoberta HTTP/local, resultados encontrado/ausente/
desabilitado, reautorização, servidores de donos separados, metadados forjados,
argumentos duplicados/grandes, ferramenta desconhecida, falha de auditoria,
negociação e cancelamento no storage. A suite completa standalone ainda exige
permissão para socket loopback nos testes HTTP.

O SDK selecionado é analisado pelo workflow de vulnerabilidades existente.
Usar toolchain Go corrigida no scan local: o Go 1.27.2 antigo reporta advisories
da biblioteca padrão independentemente da dependência MCP. O piloto não altera
a configuração global de Go.

## Comando executável

O módulo standalone agora fornece `cmd/tags-mcp`. Ele inicia somente MCP stdio;
o servidor REST original e seu `main` continuam inalterados. Na pasta do exemplo:

```sh
mkdir -p .local
go build -o .local/tags-mcp ./cmd/tags-mcp
.local/tags-mcp --help
```

`--help` escreve em stderr. Outros argumentos são recusados sem ecoar seus valores.
Toda configuração é explícita por ambiente/arquivos, sem credenciais na linha de
comando. Usar caminhos absolutos no cliente, cujo diretório de trabalho pode variar.

| Variável obrigatória | Conteúdo |
|---|---|
| `TAGS_MCP_RESOURCES_FILE` | YAML existente com um recurso `name: tags`, `collection: tags` e os campos esperados. Backend omitido é vinculado explicitamente a SQLite neste comando; backend explícito diferente de sqlite é recusado. |
| `TAGS_MCP_DATA_FILE` | Arquivo SQLite existente, com tabela/índice compatíveis já provisionados por uma aplicação Hyrule. É aberto com `mode=ro`; o comando não cria dados, seeds ou migrações. |
| `TAGS_MCP_AUDIT_FILE` | Arquivo de auditoria separado, com diretório pai existente e gravável. Pode ser criado no startup. |
| `TAGS_MCP_PUBLIC_KEY_FILE` | Arquivo regular com chave pública RSA PEM confiável. |
| `TAGS_MCP_KEY_ID` | `kid` correspondente à chave pública provisionada. |
| `TAGS_MCP_ISSUER` | Issuer esperado, igual ao claim `iss`. |
| `TAGS_MCP_AUDIENCE` | Audience esperada, presente no claim `aud`. |
| `TAGS_MCP_SCOPE` | Escopo fixo do recurso, por exemplo `local:tags`; não é o claim de permissão. |
| `TAGS_MCP_TOKEN_FILE` | Arquivo regular com JWT compacto do dono local, sem prefixo Bearer. Exige `scope` contendo exatamente `tags:read`. |

O schema canônico do exemplo está em `resources.yaml`. O modo SQLite deste comando
é uma escolha explícita do launcher; não muda o default Mongo da API standalone.
Preparar o banco com as APIs/adapter SQLite existentes antes de iniciar o comando;
ver [SQLite](sqlite.md) e [backends YAML](yaml-backends.md). Startup verifica a
compatibilidade em conexão somente leitura e falha se precisar criar tabela/índice.
Não há garantia de paridade ou migração de produção do SQLite experimental.

A configuração, os arquivos regulares e a autorização inicial do token são
verificados antes de iniciar o protocolo. Auditoria não pode apontar para dados,
schema, chave ou token; alias por symlink/hardlink de arquivo existente também é
recusado. Chave pública é carregada no startup; token é relido por operação,
limitado a 8 KiB mais newline opcional. Para substituir o token durante a sessão,
preferir troca atômica de arquivo e restringir acesso ao seu conteúdo. Token
inválido/expirado no meio da sessão nega a próxima operação sem conceder dados.

O processo responde a EOF, interrupção e SIGTERM, fechando sessão, auditoria e
conexão de dados. Inicialização tem budget de cinco segundos; requests mantêm
budgets do executor e auditoria. Operações de arquivo/hook precisam cooperar com
o ambiente local, sem promessa de sandbox de filesystem ou interrupção forçada.

## Configuração de cliente

O formato abaixo se aplica a clientes que aceitam `mcpServers` com `command`,
`args` e `env`, conforme o [guia oficial de servidores locais](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/docs/2026-07-28/develop/connect-local-servers.mdx).
Substituir todos os caminhos e valores de identidade pelos provisionados na
aplicação; o JSON não contém token nem chave privada.

```json
{
  "mcpServers": {
    "hyrule-tags": {
      "command": "/absolute/path/to/tags-mcp",
      "args": [],
      "env": {
        "TAGS_MCP_RESOURCES_FILE": "/absolute/path/to/resources.yaml",
        "TAGS_MCP_DATA_FILE": "/absolute/path/to/tags.db",
        "TAGS_MCP_AUDIT_FILE": "/absolute/path/to/audit.db",
        "TAGS_MCP_PUBLIC_KEY_FILE": "/absolute/path/to/public.pem",
        "TAGS_MCP_KEY_ID": "replace-with-provisioned-key-id",
        "TAGS_MCP_ISSUER": "https://issuer.example.test",
        "TAGS_MCP_AUDIENCE": "replace-with-configured-audience",
        "TAGS_MCP_SCOPE": "local:tags",
        "TAGS_MCP_TOKEN_FILE": "/absolute/path/to/owner.jwt"
      }
    }
  }
}
```

A matriz comprovada continua sendo o cliente SDK oficial, agora lançando o
executável compilado a partir de outro diretório. O formato de configuração é
orientação para integração; uma sessão de Claude Desktop/ChatGPT não foi executada
neste ambiente. O cliente deve negociar o protocolo 2025-11-25 suportado pelo piloto.

## Troubleshooting do comando

| Sintoma | Verificação |
|---|---|
| Processo sai com configuração ausente | Ler stderr e preencher as nove variáveis no processo do cliente, não somente em outro terminal. |
| Chave/identidade inválida | Conferir PEM público RSA, kid, issuer, audience, sub, exp e permissão exata `tags:read`; não passar `Bearer` no arquivo. |
| Banco incompatível/read-only | Confirmar arquivo existente e tabela/índice criados pelo adapter; o comando não inicializa nem migra o banco. |
| Auditoria não provisiona | Conferir diretório gravável e arquivo separado dos dados/credenciais, inclusive aliases. |
| Ferramenta funciona e depois retorna unauthenticated | Conferir expiração e substituição do arquivo de token; não há grant de sessão em cache. |
| Processo parece parado no terminal | Stdio aguarda frames do cliente MCP; usar cliente compatível, não curl. |
| Cliente reporta erro ao decodificar stdout | Iniciar o binário diretamente, sem wrapper que imprima banners/logs no stdout. |

`go test -race ./cmd/tags-mcp` compila o binário real e provisiona apenas fixtures
temporários de chave/token/banco. O teste verifica descoberta, consulta, troca de
token em sessão, origem somente leitura, auditoria persistida, stdout reservado,
erros de startup, EOF e interrupção. Não há credenciais de produção incluídas.

## Escopo restante

A Fase 9 tem prova local de interoperabilidade, não rollout de produção. Cliente
real da aplicação/desktop, provisionamento, operação da auditoria, isolamento
multi-tenant e transporte MCP remoto ainda dependem de evidência da aplicação.
Mutações e RAG continuam incrementos separados e adiados. O adapter não cria
framework de agentes, exportação automática de CRUD ou deploy de produção.

O [piloto de recuperação de documentação](knowledge-pilot.md) acrescenta ferramentas de conhecimento com pgvector em executável local separado.
