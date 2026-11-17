---
sidebar_position: 26
---

# Avaliação local com EmbeddingGemma

Em 2026-11-16, o [piloto de conhecimento](knowledge-pilot.md) foi avaliado com
os pesos já disponíveis de `onnx-community/embeddinggemma-300m-ONNX`, quantizados
em q8, snapshot `5090578d9565bb06545b4552f76e6bc2c93e4a66`. Esse modelo pequeno
atendeu ao corpus inicial; esta medição não justifica baixar um maior.

O projeto relacionado `site-chat` hoje usa Gemini. Seus experimentos anteriores
com modelos locais motivaram aproveitar esses pesos. Nenhuma configuração dos
outros projetos ou memória privada foi alterada/indexada. O corpus contém oito
documentos públicos do Hyrule e 113 trechos. Hashes de arquivos/modelo e o commit
base `eaacf6d` estão registrados em
`examples/standalone-tags-api/knowledge/evidence/2026-11-16-embeddinggemma.json`.

## Resultado e limites

| Métrica | EmbeddingGemma + pgvector | Baseline textual PostgreSQL |
|---|---:|---:|
| Perguntas com trecho relevante no top 5 (Hit@5) | 10/10 | 10/10 |
| Média do inverso da primeira posição relevante (MRR@5) | 0,883 | 0,753 |
| p95 da busca MCP com modelo carregado | 121 ms | Não medido pelo mesmo caminho MCP |

A indexação levou 41,9 segundos. A CPU foi AMD Ryzen 9 7950X3D, com duas threads
intra-op e uma inter-op, ONNX Runtime 1.31.1, NumPy 2.5.4 e tokenizers 0.22.2,
produzindo vetores de 768 dimensões. A latência inclui SDK MCP, embedding da
pergunta, consulta SQL e auditoria SQLite confirmada; startup/carga do modelo
ficam fora. Não extrapole esses tempos para Raspberry Pi ou produção sob carga.

Dez perguntas cobrem cinco assuntos em português e inglês. Um trecho relevante
precisa corresponder à fonte esperada **e** conter algum termo esperado. Essa
classificação simples não é rotulação exaustiva nem Recall@5. O baseline usa
full-text inglês do PostgreSQL, com palavras ligadas por OR, nos mesmos trechos,
ACL, escopo e contrato de modelo. Consultas em português buscam documentos em
inglês. O baseline repete autorização JWT e filtro SQL de ACL no avaliador;
não é outra ferramenta MCP.

Ambos os métodos encontram trechos relevantes. O ganho observado é na ordenação,
não no Hit@5. A amostra pequena não comprova superioridade geral, correção de
respostas, qualidade das citações ou aceitação de produção. O avaliador registra
métricas sem aplicar um limiar definido após o resultado. Amplie as perguntas
rotuladas independentemente e os casos sem resposta antes de definir critérios.

## Reproduzir com pesos existentes

Mantenha os pesos fora do Git e observe os [termos e instruções de prompts do
modelo](https://huggingface.co/onnx-community/embeddinggemma-300m-ONNX).
Para reproduzir o runtime, instale em Python isolado `onnxruntime==1.31.1`,
`numpy==2.5.4` e `tokenizers==0.22.2`. A partir do módulo standalone:

```sh
python knowledge/eval_onnx.py --snapshot /caminho/absoluto/do/snapshot
```

O helper informa URL loopback, ID de modelo e hashes dos artefatos. Atende somente
`/api/embed`, rejeita truncamento e entradas acima de 2.048 tokens, sem downloads.
É uma ponte para avaliação, não um servidor de inferência de produção.

Com banco pgvector descartável e a URL/modelo informados:

```sh
KNOWLEDGE_TEST_DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/disposable?sslmode=disable' \
KNOWLEDGE_EVAL_EMBED_URL='http://127.0.0.1:PORTA_INFORMADA/api/embed' \
KNOWLEDGE_EVAL_MODEL='embeddinggemma-300m-onnx-q8@5090578d9565bb06545b4552f76e6bc2c93e4a66' \
KNOWLEDGE_EVAL_REPORT='/tmp/hyrule-evaluation.json' \
  go test ./internal/knowledge -run '^TestRealModelEvaluation$' -count=1 -v
```

O teste opcional lê oito documentos explicitamente selecionados, cria corpus
isolado e identidade local assinada, consulta o modelo real pelo SDK MCP com
auditoria confirmada e remove suas linhas ao terminar. O CI padrão usa embeddings
determinísticos, sem pesos, chaves ou dados privados. Mudanças nos documentos
alteram o corpus: compare os hashes ao comparar medições. Encerre o helper e
o banco descartável depois do experimento.

## Prompts e reingestão

Use os mesmos valores na ingestão e no servidor com EmbeddingGemma:

```sh
export KNOWLEDGE_EMBED_DOCUMENT_PREFIX='title: none | text: '
export KNOWLEDGE_EMBED_QUERY_PREFIX='task: search result | query: '
```

Os opcionais são vazios por padrão; outros modelos podem exigir valores diferentes.
O contrato armazenado inclui hash do ID do modelo, dimensões, dois prefixos e
versão do chunker. Configurações incompatíveis não consultam/leem vetores antigos.
**Reingira os dados do piloto anterior**, que armazenava só o nome bruto do
modelo, e após qualquer mudança no contrato. Trocar pesos sem mudar o ID ainda
não é detectado: fixe a revisão do artefato.

Os testes comprovam explicitamente que uma chamada sem `knowledge:read` não chega
ao provedor de embeddings. O filtro de ACL dos documentos ocorre após o embedding
de uma pergunta autorizada. Mudanças de permissão ainda exigem reingestão; ACL
na origem/outbox são outra etapa. Esta medição não encerra essas garantias.
