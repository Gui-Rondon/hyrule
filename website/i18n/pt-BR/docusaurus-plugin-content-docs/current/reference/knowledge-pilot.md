---
sidebar_position: 25
---

# Recuperação de documentação com pgvector e MCP

**Status:** piloto local opcional. O executável
`examples/standalone-tags-api/cmd/knowledge-mcp` oferece `knowledge.search` e
`knowledge.fetch`, reaproveitando SDK oficial MCP, identidade RS256 e auditoria
SQLite separada. O framework não recebe dependência obrigatória. A geração da
resposta fica no modelo do cliente MCP.

## Corpus e permissões

O corpus inicial contém Markdown/ADRs selecionados explicitamente por manifesto
JSON: ID estável, caminho relativo, revisão e sujeitos JWT autorizados. Não há
acesso público implícito, expansão de papéis ou wildcard. `os.OpenRoot` confina a
leitura à raiz configurada, inclusive contra escapes por symlink. Cada arquivo
aceita até 1 MiB e é dividido em trechos determinísticos de 1.200 caracteres Unicode.

Cada listagem/chamada verifica novamente assinatura, emissor, audiência, expiração
e sujeito do token. Exige `knowledge:read` no escopo de corpus configurado;
`tags:read` não concede esse acesso. O PostgreSQL filtra escopo e sujeito exato
**antes** de ordenar/limitar a busca ou buscar por ID. Documento inexistente e
não autorizado produzem o mesmo resultado. Argumentos não podem escolher usuário
ou escopo. A busca retorna até cinco trechos com caminho, revisão e distância cosseno.

A saída tem limite de 16 KiB: resultados maiores são recusados. Entradas são
limitadas e rejeitam campos extras/duplicados. O orçamento de execução é de dez
segundos e a auditoria tem um segundo independente. Falha de auditoria impede a
liberação do resultado. Conteúdo recuperado é dado não confiável e não altera política.

O [caminho de fonte versionada/outbox](knowledge-sync.md) agora permite mudanças
assíncronas de conteúdo, ACL e exclusões. Busca/leitura verificam versão e ACL da
fonte, bloqueando trechos antigos após o commit mesmo com worker parado. `ingest`
continua para snapshots serializados. Editar arquivos ou omitir entradas não
atualiza a fonte: use `enqueue`/`QueueSource`. Mapeamento de produtores de aplicações
continua como integração explícita.

## Executar

Provisione um banco dedicado com [pgvector](https://github.com/pgvector/pgvector).
O administrador instala `CREATE EXTENSION vector;`. Use credencial de provisionamento
em `init`, de escrita em `ingest` e somente SELECT em `serve`. O servidor de leitura
não cria schema nem escreve documentos.

A partir de `examples/standalone-tags-api`:

```sh
go build -o /tmp/knowledge-mcp ./cmd/knowledge-mcp
export KNOWLEDGE_DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/knowledge?sslmode=disable'
export KNOWLEDGE_SCOPE='hyrule-docs'
/tmp/knowledge-mcp init

export KNOWLEDGE_SCOPE='hyrule-docs'
export KNOWLEDGE_EMBED_URL='http://127.0.0.1:11434/api/embed'
export KNOWLEDGE_EMBED_MODEL='your-pinned-embedding-model'
export KNOWLEDGE_EMBED_DIMENSIONS='768'
export KNOWLEDGE_DOCUMENT_ROOT='/caminho/absoluto/hyrule'
export KNOWLEDGE_MANIFEST='/caminho/absoluto/manifest.json'
/tmp/knowledge-mcp ingest
```

Modelo e dimensão são exemplos de configuração, não defaults. Provisione primeiro
o modelo escolhido no Ollama. Usamos sua [API `/api/embed`](https://docs.ollama.com/api/embed)
com truncamento desabilitado, timeout e resposta limitada, validando dimensão e
vetores finitos/não nulos. O executável não baixa modelos. Ingestão e busca exigem
o mesmo artefato de modelo fixado e dimensão. Trocar modelo exige reingerir todo
o corpus; nomes diferentes não se misturam na consulta. Alterar pesos mantendo
o mesmo nome não é detectado. Textos são enviados ao endpoint configurado.

Exemplo de manifesto; substitua a revisão pelo commit indexado e o sujeito pelo
`sub` real do JWT assinado:

```json
[
  {
    "id": "architecture-decisions",
    "source": "website/docs/reference/architecture-decisions.md",
    "revision": "INDEXED_GIT_COMMIT",
    "subjects": ["alice"]
  }
]
```

Para servir, mantenha configurações de banco, corpus e embeddings e acrescente:

```sh
export KNOWLEDGE_PUBLIC_KEY_FILE='/caminho/absoluto/public.pem'
export KNOWLEDGE_KEY_ID='provisioned-key-id'
export KNOWLEDGE_ISSUER='https://your-issuer.example'
export KNOWLEDGE_AUDIENCE='hyrule-knowledge'
export KNOWLEDGE_TOKEN_FILE='/caminho/absoluto/owner.jwt'
export KNOWLEDGE_AUDIT_FILE='/caminho/absoluto/knowledge-audit.sqlite'
/tmp/knowledge-mcp serve
```

Configure o cliente MCP para executar esse binário com argumento `serve` e essas
variáveis. Stdout é exclusivo do protocolo. O token é relido por operação; sua
substituição revoga a próxima chamada. Use um processo por proprietário local,
como no [piloto de tags](mcp-pilot.md). Não há endpoint MCP de rede, OAuth ou
compatibilidade comprovada com clientes desktop. Credenciais não entram nos
argumentos das ferramentas ou na auditoria; erros SQL/provedor são sanitizados.

Entradas das ferramentas: `{"query":"Como funciona a autorização MCP?"}` e
`{"id":"ID_DO_TRECHO_RETORNADO"}`. O modelo do cliente usa os trechos e referências
para elaborar respostas e citações.

## Validação e evolução

A suíte usa PostgreSQL/pgvector reais para ingestão idempotente, chunking Unicode,
rollback, remoção de trechos antigos, troca de ACL, isolamento de usuário/corpus
e separação de modelos. Um subprocesso stdio real valida list/search/fetch após
a ingestão pelo comando. O HTTP de embeddings usa fixture determinística no
formato Ollama: isso valida integração e isolamento, **não qualidade semântica**.
Uma [avaliação local separada com EmbeddingGemma](knowledge-evaluation.md) agora
registra modelo real: Hit@5 10/10, MRR@5 0,883 versus textual 0,753, em dez perguntas.

Execute com banco descartável e permissão para instalar a extensão:

```sh
cd examples/standalone-tags-api
KNOWLEDGE_TEST_DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/disposable?sslmode=disable' \
  go test -race ./internal/knowledge ./cmd/knowledge-mcp -count=1
```

Antes da adoção, meça Recall@5 contra busca lexical com modelo real. Perguntas
iniciais: quais backends têm outbox transacional; por que dryrun não é sandbox;
como PATCH/OpenAPI trata campos aninhados obrigatórios; como permissões JWT
controlam ferramentas; e como Postgres armazena documentos de recursos. Registre
seções esperadas, latência e custos de embedding/indexação. Não há superioridade geral ou aceitação de produção comprovadas, nem ingestão
automática no startup.

A interface `Store` separa substituição atômica de documento, busca e leitura
autorizadas de MCP/embeddings. Uma futura implementação Qdrant deve preservar
essas garantias. Adotar seu servidor MCP oficial requer integração própria de
identidade, ACL e auditoria obrigatória. O piloto usa busca cosseno exata; índices
aproximados e busca híbrida dependem de avaliação. Veja [fonte versionada/outbox](knowledge-sync.md) para o novo pipeline local e atualização de schema; produtores de produção exigem integração explícita.

Manifesto de corpus e perguntas de avaliação estão em `examples/standalone-tags-api/knowledge/`; substitua os placeholders de sujeito/revisão antes de ingerir.

Os opcionais `KNOWLEDGE_EMBED_DOCUMENT_PREFIX` e `KNOWLEDGE_EMBED_QUERY_PREFIX` atendem modelos assimétricos. O contrato inclui esses valores; veja [prompts e reingestão](knowledge-evaluation.md#prompts-e-reingestão).
