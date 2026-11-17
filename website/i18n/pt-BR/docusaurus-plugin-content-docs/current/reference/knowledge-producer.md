---
sidebar_position: 28
---

# Produtor concreto de artigos

O módulo standalone inclui `internal/knowledgedocs` e a CLI opcional de curadoria
`cmd/knowledge-docs`. Um `rest.Resource` concreto usa o adapter Postgres e as
operações normais `Service.Insert/Patch/Replace/Delete`. Seus hooks de pré-escrita
materializam a fonte de conhecimento e chamam `QueueSource`. Artigo, fonte/ACL e
evento outbox compartilham a mesma transação. O [worker e o leitor MCP](knowledge-sync.md)
existentes consomem as mudanças. A aplicação REST padrão de tags permanece igual.

## Mapeamento e acesso explícitos

O artigo possui `title`, `body`, `readers` e `private_notes` opcional. Somente título
e corpo viram texto de recuperação. Leitores são sujeitos JWT exatos; lista vazia
nega todos. Notas privadas ficam apenas no artigo: não entram na fonte projetada,
eventos, embeddings ou trechos MCP. Referências/IDs da fonte são `articles/UUID`;
um hash determinístico dos campos projetados fornece a revisão. Alterar notas
privadas não gera trabalho de indexação.

O curador precisa de JWT válido com `knowledge:write` exato, emissor, audiência,
key ID e corpus configurados. A autorização precede o parsing; a CLI a verifica
antes de abrir o banco ou o arquivo de documento. Os hooks repetem a autorização
na transação. Essa permissão permite ao curador confiável administrar artigos e
ACLs de todo o corpus configurado. Ela não implica `knowledge:read`: leitores
continuam precisando dessa permissão e da ACL do documento.

PATCH/PUT/DELETE exigem ETag específico e correspondente. A validação permanece
ativa; clientes não escolhem ID, corpus ou bypass de validação/concorrência.
Entradas são limitadas e rejeitam campos extras/duplicados. PATCH usa a semântica
real de merge do adapter; PUT preserva o ID original. Exclusão gera tombstone.
Falhas posteriores nos hooks/adapter revertem também a projeção e o evento.

Cada corpus usa uma tabela física de artigos derivada do escopo. O adapter
Postgres atual não aplica `Resource.Datasource.Filter`, portanto o produtor não
usa essa propriedade como garantia de isolamento. O escopo é configuração
confiável do processo, nunca campo da entrada. Fonte/outbox mantêm o isolamento
por corpus descrito no guia de sincronização.

## Executar

Instale a extensão vector como administrador. No módulo standalone, defina banco
e escopo e provisione explicitamente o produtor:

```sh
go build -o /tmp/knowledge-docs ./cmd/knowledge-docs
export KNOWLEDGE_DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/knowledge?sslmode=disable'
export KNOWLEDGE_SCOPE='hyrule-docs'
/tmp/knowledge-docs init
```

`init` usa privilégios de provisionamento do banco para criar tabelas de fonte,
trechos, outbox e artigos do corpus. Comandos de escrita não criam schema. Aplique
grants separadamente; o leitor MCP não precisa acessar a tabela de artigos.
Provisione pelo emissor confiável um JWT de curador com `knowledge:write` e configure:

```sh
export KNOWLEDGE_PUBLIC_KEY_FILE='/caminho/absoluto/public.pem'
export KNOWLEDGE_KEY_ID='provisioned-key-id'
export KNOWLEDGE_ISSUER='https://your-issuer.example'
export KNOWLEDGE_AUDIENCE='hyrule-knowledge'
export KNOWLEDGE_TOKEN_FILE='/caminho/absoluto/curator.jwt'
export KNOWLEDGE_DOCUMENT_FILE='/caminho/absoluto/article.json'
/tmp/knowledge-docs create
```

Exemplo de `article.json`:

```json
{"title":"Transactions","body":"Source and outbox commit together.","readers":["alice"],"private_notes":"Nota editorial interna"}
```

A saída contém somente operação, UUID e ETag, sem conteúdo do documento. Guarde
os valores para as próximas escritas. Por exemplo, salve `{"readers":["bob"]}`
num arquivo de patch e execute:

```sh
export KNOWLEDGE_DOCUMENT_ID='UUID_RETORNADO_NO_CREATE'
export KNOWLEDGE_IF_MATCH='ETAG_RETORNADO_NO_CREATE'
export KNOWLEDGE_DOCUMENT_FILE='/caminho/absoluto/patch.json'
/tmp/knowledge-docs patch
```

Use o último ETag retornado a cada operação. `replace` recebe um artigo completo;
`delete` precisa de ID/ETag e não lê arquivo de documento. Execute o
`knowledge-mcp worker` existente com o mesmo escopo/contrato de embedding e
`knowledge-mcp serve` com token de leitor. A revogação/exclusão vale após o commit
da fonte, independentemente do término da indexação.

Este é um produtor concreto no exemplo. Não cria endpoint HTTP de escrita nem
ferramenta de mutação MCP. A implantação requer configuração do emissor,
credenciais, supervisão operacional e política de auditoria do domínio. Mutações
controladas pelo MCP e seu protocolo de aprovação/idempotência continuam na Fase 10.

## Evidências

Os testes com Postgres/pgvector reais cobrem criação, mudanças de conteúdo/ACL,
exclusão de campos privados, ETag inválido, PUT/delete, rollback por hook de domínio
posterior e isolamento entre corpora. O fluxo termina numa busca SDK MCP
autenticada com auditoria persistente. Um teste da CLI comprova autorização antes
do banco/corpo e valida provisionamento, criação e exclusão. O job pgvector do CI
executa os dois pacotes. Nenhuma aplicação foi implantada em produção.
