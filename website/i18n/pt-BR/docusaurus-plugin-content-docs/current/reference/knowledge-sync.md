---
sidebar_position: 27
---

# Fonte versionada de conhecimento e outbox

O piloto opcional agora indexa documentos assincronamente pelo relay
`outboxpostgres` existente. Acrescenta uma fonte autoritativa de documentos no
Postgres; não observa automaticamente todos os recursos Hyrule ou arquivos.
O MCP continua expondo apenas `knowledge.search` e `knowledge.fetch`.

## Consistência e permissões

`hyrule_knowledge_documents` guarda texto, ACL, revisão opaca, versão numérica
crescente e tombstone de exclusão. A mudança da fonte e um evento mínimo
`{scope,id,version}` são confirmados na **mesma transação local**. Texto e listas
de ACL não entram nos eventos. Cada corpus possui uma tabela outbox derivada de
seu escopo, impedindo que seu relay consuma o trabalho de outro corpus.

Busca e leitura por ID consultam a fonte atual e exigem ACL, documento não
excluído e versão numérica igual à do trecho. **Consultas iniciadas após o commit
de revogação/exclusão não retornam trechos antigos**, mesmo com provedor fora do
ar ou relay parado. Leituras SQL já em andamento seguem o snapshot de sua instrução.
O novo usuário autorizado também espera a indexação da versão atual: a segurança
é preservada com uma possível janela sem resultados. JWT e `knowledge:read`
continuam sendo verificados antes do embedding da pergunta e da recuperação.

O worker relê a fonte atual, gera embeddings fora das transações de fonte/índice
e compara a versão sob lock antes de substituir os trechos atomicamente. Uma
mudança durante o embedding descarta o resultado obsoleto. Eventos antigos
indexam a fonte mais recente, sem reproduzir conteúdos antigos; tombstones
impedem ressurreição. Versões/contratos já indexados dispensam novo embedding em
redelivery, inclusive após crash entre indexação e confirmação do outbox.
Workers concorrentes podem calcular embeddings duplicados: a entrega é pelo
menos uma vez, não exatamente uma. ACL vazia e exclusão limpam trechos sem embedding.

O relay existente fornece retry/backoff, DLQ Postgres, consultas de saúde e
`Replay`. A transação de claim mantém o **lock da linha outbox** durante a chamada
ao publisher; as transações da fonte/índice continuam curtas. O comando consulta
a fila a cada segundo e dá cinco minutos para indexar um documento. Provisione
pelo menos duas conexões no pool e um contrato de embedding por corpus/workers.
Criação de schema e operação/monitoramento de DLQ permanecem com o operador.

## Provisionar e usar

Em `examples/standalone-tags-api`, compile `cmd/knowledge-mcp` conforme a
[configuração do piloto](knowledge-pilot.md). Defina banco e escopo **antes** do init:

```sh
export KNOWLEDGE_SCOPE='hyrule-docs'
/tmp/knowledge-mcp init
```

`init` cria/atualiza tabelas de fonte/trechos e o outbox do corpus. Não presume
confiança nos trechos anteriores: versão zero não pode ser lida. Reimporte com
`ingest` ou crie fontes com `enqueue` e execute `worker`. Reaplique os grants SELECT
do leitor às tabelas `hyrule_knowledge_documents` e `hyrule_knowledge_chunks`.
Separe credenciais de provisionamento, escrita confiável de fonte, indexação e
leitura MCP. Não há novo endpoint de escrita ou ferramenta pública de ingestão.

No manifesto assíncrono, acrescente `expected_version`:

```json
[
  {
    "id": "architecture-decisions",
    "source": "website/docs/reference/architecture-decisions.md",
    "revision": "INDEXED_GIT_COMMIT",
    "subjects": ["alice"],
    "expected_version": 0
  }
]
```

Zero indica criação. Para atualizar, use a versão **numérica** atual da fonte,
não a revisão Git. Uma mudança conflitante desatualizada é rejeitada; reenviar
um snapshot idêntico não altera a versão nem acrescenta evento. Consulte `version`
em `hyrule_knowledge_documents` por escopo/ID ou use a API confiável `LoadSource`.
Com as configurações de raiz confinada e manifesto:

```sh
/tmp/knowledge-mcp enqueue
/tmp/knowledge-mcp worker
```

`enqueue` exige banco, escopo, raiz de documentos e manifesto; não chama embeddings.
`worker` exige banco, escopo e as configurações de modelo, dimensão e prompts do
piloto. Não usa JWT/arquivo de auditoria porque é um processo do operador com
credenciais de banco. `serve` mantém autorização JWT e auditoria independente
obrigatórias. Os comandos devem compartilhar o contrato de embedding. Encerre o
worker com interrupt/SIGTERM.

Para revogar todos, envie o documento existente com `subjects: []` e sua versão
esperada. Para excluir, envie um tombstone sem precisar de arquivo de origem:

```json
[
  {"id":"architecture-decisions","revision":"withdrawn","deleted":true,"expected_version":1}
]
```

Recriação exige a versão atual do tombstone. Omitir uma entrada ou editar um arquivo
**não** atualiza a fonte: um produtor deve chamar `enqueue`/`QueueSource`. O manifesto
confirma cada documento separadamente; falha numa entrada posterior não desfaz as
anteriores. O comando síncrono `ingest` continua para snapshots confiáveis e
serializados; não o misture com produtores versionados concorrentes, pois ele
substitui intencionalmente o snapshot autoritativo.

## Integração e testes

`Postgres.QueueSource(ctx, scope, document, deleted, expectedVersion)` exige uma
transação ativa `platform/postgres.Transactor` no mesmo pool. Aplicações podem
chamá-lo em sua transação/hook após materializar campos permitidos e ACL. A API
rejeita autocommit e transações de outro pool. Não há atomicidade entre bancos,
pipeline Mongo, mapeamento automático de recursos ou inferência de ACL de domínio.
Esta tabela é a autoridade do consumidor; a aplicação deve mantê-la alinhada
transacionalmente à sua verdadeira autoridade de permissões.

Testes com pgvector real cobrem rollback conjunto de fonte/evento, falha ao gravar
evento, conflito entre escritores, bloqueio imediato com trechos físicos antigos,
limpeza de ACL vazia/exclusão, eventos fora de ordem, duplicação, recriação,
alteração durante embedding, falha do provedor, DLQ e replay. Um subprocesso stdio
real grava troca de ACL sem embeddings, comprova bloqueio antes do worker,
executa o worker, consulta como novo sujeito e verifica tombstone antes de retomá-lo.
Trechos antigos sem versão são bloqueados.

O caminho local Postgres de fonte/outbox está implementado. Ligação aos produtores
de produção, operação de DLQ, retenção/tombstones e avaliação maior de qualidade e
carga continuam como trabalho de implantação. Um futuro índice Qdrant precisa
revalidar esta fonte antes de retornar resultados; seu servidor MCP nativo sozinho
não fornece essa política.

Para reconstruir com outro modelo/contrato, pare os workers antigos, configure
o novo contrato no worker/servidor e envie cada fonte com nova revisão opaca e
sua versão esperada atual (ou use `ingest` síncrono serializado). Um snapshot
idêntico não cria evento: repetir `enqueue` sem mudanças não dispara reindexação
de modelo. A versão da fonte muda e trechos antigos ficam bloqueados até a nova
indexação.

Um [produtor concreto de artigos](knowledge-producer.md) conecta escritas Service e hooks de pré-escrita do Hyrule a este pipeline, com permissão de curador e ACL explícita de leitores.
