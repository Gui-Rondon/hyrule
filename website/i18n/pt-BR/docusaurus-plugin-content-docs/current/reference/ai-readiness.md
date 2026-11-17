---
sidebar_position: 21
---

# Análise de DX e prontidão para IA

**Data da revisão:** 2026-11-16. **Base:** `970b7f141a4eb7eb0865865191acd3d4ee53a9b7` de
`github.com/Gui-Rondon/hyrule`.

Esta revisão estende ADR-0001–0006 e as Fases 0–5 históricas, sem recomeçar esse
trabalho. Os [ADRs 0007–0012](architecture-decisions.md) estão **Propostos**. A
[Fase 6](roadmap.md) tem estudo concluído e correções de contrato abertas; a
Fase 7 tem executor local parcial; a Fase 8 tem descoberta local parcial; as Fases 9–11 estão **não iniciadas**. Aceitação e implementação são fatos separados.
Não há novas capacidades de runtime ou medições de desempenho. O estudo posterior
usa SQLite em memória; não executa as integrações Mongo/Postgres dos serviços.

## Correções da auditoria

- Pacotes públicos, portabilidade por `rest.Adapter`, outbox Mongo/Postgres,
  scaffolding do CLI, CI de build afetado e versionamento já existem. Reutilizar
  essas capacidades. Postgres implementa contrato de documentos sobre JSONB e
  metadados, não um ORM relacional arbitrário.
- `declarative.Registry` já tem implementações Mongo, Postgres e SQLite em
  `cmd/hyrule-serve/backends.go`. Não é uma abstração especulativa sem uso.
  O processo ainda tem um backend conectado por vez; não há promessa de
  transações entre bancos.
- SQLite já suporta YAML com `backend: sqlite`, sujeito aos
  [limites experimentais](sqlite.md). Outbox, dry-run e paridade avançada seguem
  indisponíveis. O padrão local do CLI é SQLite; o layout de plataforma continua
  disponível explicitamente.
- O caminho Go constrói `Resource`/`Schema` explicitamente. Não foi encontrada
  uma API geral para inferir schema de structs de domínio arbitrárias.
- `app/dryrun.go` configura dry-run por deployment. O `WriteGuard` Mongo detecta
  escritas fora da transação depois que persistiram; não as impede. Postgres tem
  composição própria de rollback. SQLite recusa dry-run. Nenhum rollback de banco
  desfaz efeitos externos de hooks.
- Autenticação HTTP e preparação da requisição pertencem a `rest.API`/handlers;
  invocar `Service` diretamente não reaplica esse perímetro.
- `platform/search/elasticsearch.go` oferece indexação/atualização/exclusão e
  healthcheck, não retrieval semântico. `behaviors/searchsync` é pós-commit
  best-effort, sem garantia de sincronização durável do índice.
- Hoje permanecem internos `internal/domain`, `internal/integration` e
  `internal/pgexplain`. Descrições antigas das fases registram a árvore anterior.

O roadmap histórico também continha afirmações desatualizadas sobre versionamento e
outbox. Notas datadas preservam as decisões originais e identificam o estado atual.
Reutilizar gates existentes, incluindo cobertura de runtime de 95%, workflows de
segurança e proveniência de releases. Ler um workflow não comprova proteção de branch
remota nem sucesso do último release. O roadmap v1 raiz continua independente; IA não
vira um novo gate de v1 por implicação.

## Comparação de desenho de DX

Esta comparação é de desenho e experiência de desenvolvimento. Não houve benchmark de
throughput, latência, alocações ou produtividade medida. Hyrule inclui contrato REST,
validação, hooks e concorrência; comparar seu request completo com uma chamada SQL
isolada produziria números sem equivalência.

| Referência | Modelo de trabalho documentado | O que adaptar ao Hyrule | Limite da adaptação |
|---|---|---|---|
| Bun | Modelos Go e abordagem SQL-first | Diagnósticos de query e caminho explícito para operações SQL específicas da aplicação | Não mascarar joins relacionais como filtros de documento universalmente portáveis. |
| Ent | Schema e geração de API estaticamente tipada | Geração determinística e verificação de divergência, se um consumidor demonstrar duplicação repetida | Não importar um grafo de relacionamentos, migrações e eager loading para o núcleo REST. |
| GORM | Structs, tags e convenções configuráveis | Exemplos mínimos, defaults claros e escapes documentados | Não introduzir inferência implícita de tabelas, associações ou migração automática de produção. |
| sqlc | Código tipado derivado de schema e consultas SQL | Preservar visibilidade de SQL em workloads relacionais fora do contrato de documento | Não substituir `Adapter` nem prometer que SQL arbitrário pode virar uma operação REST genérica. |

Fontes primárias: [Bun: getting
started](https://bun.uptrace.dev/guide/getting-started.html), [Bun:
models](https://bun.uptrace.dev/guide/models.html), [Ent: code
generation](https://entgo.io/docs/code-gen/), [GORM:
models](https://gorm.io/docs/models.html), [GORM:
conventions](https://gorm.io/docs/conventions.html), [sqlc:
generate](https://docs.sqlc.dev/en/v1.26.0/howto/generate.html). A referência sqlc está
versionada em 1.26.0; a comparação usa o princípio de geração, sem alegar que seja a
versão mais recente.

**Pontos fortes próprios.** Um recurso agrega schema, validação, hooks e operações. O
contrato REST inclui ETags e precondições. YAML e Go usam o mesmo registro em
`rest.API`. Os comportamentos existentes compõem hooks em vez de criar outro framework.
SQLite reduz a infraestrutura da primeira experiência, mantendo limites experimentais
explícitos.

**Lacunas justificadas.** Definições declarativas e de runtime repetem parte da
estrutura; contratos de descrição não representam closures de validação; recursos expõem
`Doc` dinâmico; diferenças de backend precisam ser descobertas antes de uma chamada
falhar. Isso justifica melhorar contrato e diagnóstico. Não comprova, por si só, retorno
de um gerador completo de entidades.

 A próxima melhoria justificada é consistência de contrato e diagnósticos; um gerador
completo de entidades ainda precisa de evidência medida. O
[ADR-0007](architecture-decisions.md) propõe esse estudo.

## Composição opcional

```text
Aplicação / composition root
  ├── rest.API ───────────────────────> rest.Service ──> rest.Adapter
  └── adaptador MCP opcional
        └── catálogo de ferramentas e descoberta
              └── executor autorizado ──────────────> rest.Service

Descritores externos ──> contratos derivados de Resource/Schema
Hooks existentes ─────> auditoria de domínio e outbox transacional
Worker opcional ──────> embeddings / índice / retrieval
```

As setas representam dependências e chamadas propostas, não pacotes implementados. O
catálogo, o executor e os adaptadores são opt-in. `platform/*` continua sem importar
`rest`; `rest` e `app` não importam MCP ou SDK de modelo. Clientes sem IA não
inicializam esses componentes nem configuram credenciais de provedores. Se manter SDK
fora do grafo de dependências do módulo principal se tornar requisito, avaliar submódulo
separado em vez de prometer esse isolamento apenas com novos pacotes.

O termo “AI Tool Registry” designa registro de operações da aplicação, não registro de
plugins de banco. Um nome possível é `aitools`; é ilustrativo. Hooks são apropriados
para efeitos ligados ao ciclo de escrita. Descoberta, autenticação, limites e despacho
não cabem em um `rest.Hooks` e precisam de um wrapper de execução. Portanto não se
recomenda colocar toda a integração em `behaviors/ai`.

 O primeiro consumidor deve fornecer um recurso real, uma consulta limitada e sua
política de acesso. Comprovar execução autorizada antes da exposição externa por MCP.
Mutações entram depois da validação de concorrência, idempotência, aprovação e efeitos
externos. Retrieval é um ramo opcional independente de MCP.

## Escolhas abertas e compatibilidade

Consumidor/recurso/política, direção de eventual codegen, API neutra de exportação de
contrato, nomes/distribuição dos pacotes, transporte/versão/SDK MCP, armazenamento de
aprovações/idempotência e provedor/índice/corpus de embeddings continuam abertos. RAG
exige corpus permitido, consultas reais, baseline lexical, orçamento e metas de
qualidade antes de começar.

A direção é aditiva: descritores externos, pacotes opcionais e composição explícita. Não
se propõe mudar assinaturas de `rest.Adapter`, `rest.Store`, `Service` ou `app.Spec`. Um
eventual exportador neutro de contrato é nova superfície pública sujeita a revisão.
Preservar compatibilidade BSON e diferenças semânticas dos backends. Mudanças futuras de
comportamento público seguem a política de compatibilidade e changelog existente.

## Evidências

A revisão leu as páginas de ADR e roadmap, `ROADMAP.md`, `app/app.go`, `app/dryrun.go`,
`testkit/testkit.go`, `rest/{resource,schema,service,http,adapter,
store,mongoadapter,openapi,swagger}.go`, `declarative/{declarative,backend}.go`,
`cmd/hyrule-serve/backends.go`, `restpostgres/`, `restsqlite/`,
`behaviors/{events,searchsync}/`, `outbox/`, `outboxpostgres/`,
`platform/search/elasticsearch.go`, a referência SQLite e os workflows existentes.
Evidências históricas de implementação permanecem na [revisão
integrada](integration-review.md). Não se afirma novo sucesso de CI, resultado de
desempenho ou matriz MCP validada.

## Resultados do estudo da Fase 6

O [estudo de contratos](phase-6-contract-study.md) executou 21 casos com o adapter
SQLite real. Seis diferenças entre schema estrutural e HTTP foram reproduzidas;
codegen continua adiado. Essa evidência não aceita os ADRs propostos nem encerra
todos os critérios da Fase 6.

A correção posterior de C6-01 preserva obrigatoriedade aninhada no contrato
OpenAPI de PATCH; o estudo registra a medição original e a repetição após a
correção. As restrições de servidor C6-02–04 continuam explícitas.

O [piloto de consulta tags.lookup](read-pilot.md) define o recurso e a política
proposta para o próximo incremento, com limites e critérios de aceitação.
