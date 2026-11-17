---
sidebar_position: 11
---

# Roteiro de evolução: framework e plataforma

Esta página registra a base já concluída e a evolução proposta. Os resumos
separam evidência de implementação, adoção em produção e aceitação formal das
[decisões arquiteturais](architecture-decisions.md).

## Trabalho atual de adoção e estabilidade

As Fases 0–5 abaixo são históricas; a Fase 6 tem evidência de estudo e segue
aberta; as Fases 7–9 têm provas locais de identidade, descoberta e MCP; a Fase 10 de mutações MCP controladas segue proposta; a Fase 11 tem retrieval local, avaliação de modelo, fonte/outbox e produtor concreto. Adoção em produção permanece aberta. O [roadmap atual para v1](https://github.com/Gui-Rondon/hyrule/blob/main/ROADMAP.md)
adiciona políticas de compatibilidade/suporte, CI Go 1.26/1.27 em PRs, scaffold
SQLite local, composição de roteadores e releases manuais com proveniência.
Veja [estabilidade](stability.md) e [SQLite](sqlite.md) para o comportamento atual.
A cobertura histórica abaixo descreve fases antigas; o gate atual é 95% do runtime.
Não há data definida para a v1.

## Por que dois caminhos

A direção do projeto é oferecer ao Hyrule dois caminhos de adoção
complementares, sobre o mesmo núcleo: um caminho **Framework** (`go
get`, sem exigir monorepo ou CLI) e um caminho **Plataforma** (um
monorepo, CLI e geração da estrutura inicial, para equipes que querem uma configuração
padronizada de múltiplos serviços). A partir da Fase 1, o caminho
Framework é consumível externamente (ver ADR-0001), e a partir da Fase
3, o caminho Plataforma tem seus dois primeiros comandos (ver ADR-0004)
— as duas lacunas abaixo ficam como registro histórico, não
porque ainda estejam abertas.

## Análise de lacunas

| Área | Visão | Estado real hoje | Lacuna |
|---|---|---|---|
| Framework (`go get`) | Consumível por qualquer aplicação Go externa | `app`, `rest`, `outbox`, `testkit`, `declarative`, `behaviors/*`, `platform/*` são públicos; `internal/domain`, `internal/integration` e `internal/pgexplain` continuam internos | **Fechado** — Fase 1 (verificado: um módulo externo de rascunho importa `rest`, `app`, `declarative`, `platform/mongodb`) |
| `rest.Store` entre backends | Portável entre Mongo e Postgres | `rest.Adapter` (`rest/adapter.go`) na frente de `Store`, implementado por `restpostgres` sobre `platform/postgres`; `examples/blog-apis/services/comments-api` é um recurso real servido de ponta a ponta por esse caminho | **Fechado** — revisão do ADR-0002; ver [`adapter.md`](adapter.md) |
| Outbox nos dois backends | Eventos transacionais | `outbox` Mongo e `outboxpostgres` Postgres, hooks compartilhados, comments-api publica via Pub/Sub | **Concluído** — veja [outbox](outbox.md) e [evidências](integration-review.md) |
| CLI / Plataforma | `hyrule new` / `service create` / `dev` / `build` / `deploy` | `cmd/hyrule` tem `new` e `service create` (ADR-0004); `dev`/`build --affected`/`deploy` nem começaram | **Parcialmente fechado** — geração da estrutura inicial da Fase 3 pronta; envolver `ci/affected`/Makefile foi adiado de propósito |
| Build afetado / deploy independente | Orquestrado pela plataforma | `ci/affected` roda de verdade em `.github/workflows/blog-apis-affected.yml`; deploy é um ponto de integração documentado `deploy/<service>/deploy.sh`, não uma capacidade entregue (ADR-0006) | **Fechado para o exemplo, de propósito** — Fase 4; um adapter de deploy real e versionamento por serviço continuam sem começar até existir um consumidor real |
| Documentação de duas jornadas | Guia (Framework) + Guia (Plataforma) + Referência compartilhada | As duas jornadas existem (`/docs/learn`, `/docs/platform`), cada uma descrevendo só o que foi construído | **Fechado** — Fase 5 |
| ADRs / versionamento | Um processo formal de decisão e compatibilidade | ADRs, política de compatibilidade e `CHANGELOG.md` existem | **Fechado** — Fase 0 / ADR-0005; novos ADRs abaixo continuam propostos |

## Fases

- **Fase 0 (esta rodada).** Diagnóstico, ADRs e este roadmap,
  publicados. Risco: nenhum (só documentação). Esforço: baixo.
  **Concluída.**
- **Fase 1 — Consolidar o framework.** Executar o ADR-0001 (promover os
  pacotes que um consumidor precisa, tirando-os de `internal/`) e
  manter a linha do ADR-0002 (sem generics em `rest.Store` ainda).
  **Concluída.** `app`, `behaviors/*`, `declarative`, `outbox`,
  `platform/*`, `rest` e `testkit` se moveram para raiz do módulo
  (`internal/domain` é o único pacote que continua interno); `go build
  ./...`, `go vet ./...` e `make cover` (90,9%, mínimo exigido de 85% na época) passam
  todos, e um módulo externo de rascunho construído contra os novos
  caminhos compila e roda. O `rest.Store` em si continua inalterado,
  conforme o ADR-0002.
- **Fase 2 — Validar o modo standalone.** Construir um exemplo externo
  de verdade: um módulo Go separado, com seu próprio `go.mod`,
  importando o Hyrule publicado — não mais um pacote dentro de
  `examples/blog-apis`. **Concluída.**
  [`examples/standalone-tags-api`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/standalone-tags-api)
  é um módulo próprio (`replace` para este checkout até uma versão receber
  uma tag — ver o README dele), com um recurso declarado em YAML, um
  hook em Go (`behaviors/audit`), um teste usando `testkit`, e um ciclo
  real de `go run .` + `curl`, tudo verificado.
- **Fase 3 — Plataforma / CLI.** Implementar o menor CLI útil conforme o
  ADR-0004. **Concluída, só geração da estrutura inicial.** O `cmd/hyrule` (`go install
  github.com/Gui-Rondon/hyrule/cmd/hyrule`) tem `hyrule new <project>`
  e `hyrule service create <name>`, com templates embarcados para o
  `go.mod`/`Makefile`/`docker-compose.yml` de um projeto e para o
  `app.Main` + um recurso + um teste com `testkit` de um serviço.
  Verificado de ponta a ponta: `hyrule new` → `hyrule service create`
  → `go mod tidy` → `make build` → `make test` → `make run` + `curl`
  tudo funciona, sem copiar `examples/blog-apis` na mão. O `ci/affected`
  e o padrão do Makefile *não* foram envolvidos como subcomandos ainda
  — o ADR-0004 adiou isso de propósito até existir um segundo comando
  que precise deles de verdade.
- **Fase 4 — Experiência operacional.** Conforme o ADR-0006.
  **Concluída, com escopo no exemplo.** O
  `.github/workflows/blog-apis-affected.yml` roda o `ci/affected` de
  verdade em push/PR, compila só os serviços que ele reporta, e chama o
  `deploy/<service>/deploy.sh` de cada um — um stub hoje, o ponto de integração
  onde o comando de deploy real de qualquer provedor entra. Nenhuma
  abstração de provedor, nenhum sistema de deploy genérico, e nenhum
  esquema de versionamento por serviço: nenhum deles tem um consumidor
  real ainda para desenhar contra (ver o ADR-0006). Isso vive em
  `examples/blog-apis`, não como uma capacidade entregue pelo Hyrule —
  um time usando `hyrule new` copia e adapta.
- **Fase 5 — Documentação.** Escrever a jornada Plataforma só quando o
  CLI for real, para que o site publicado nunca descreva um comando que
  ainda não existe. **Concluída.** Uma nova categoria "Plataforma" sob o
  [Guia](/docs/learn/intro) — `/docs/platform/intro`,
  `creating-a-project`, `creating-services`, `local-development` e
  `ci-and-deploy` — cobre exatamente `hyrule new`/`hyrule service
  create` e o padrão `ci/affected`/`deploy.sh` da Fase 4, nomeando
  todo comando ainda não construído (`dev`, `build --affected`,
  `deploy`) como não construído em vez de omitir silenciosamente. As jornadas de
  aprendizado e plataforma estão disponíveis em inglês e português brasileiro.

## Limites da rodada histórica (Fases 0–5)

**Nota de revisão — 2026-11-16.** O parágrafo abaixo preserva o escopo original,
não as capacidades atuais. A portabilidade de outbox foi implementada depois,
conforme a revisão do ADR-0003. A observação da Fase 1 sobre o único pacote
interno também é histórica; hoje há `internal/integration` e `internal/pgexplain`.
O CLI ganhou SQLite local e YAML suporta os três backends registrados; isso
não adiciona outbox nem dry-run ao SQLite. Ver [análise atual](ai-readiness.md).

Nenhuma mudança na interface do `rest.Store`, nenhuma portabilidade de
outbox, nenhum subcomando de CLI para `dev`/`build --affected`/`deploy`
(o `ci/affected` roda no CI agora, mas não é envolvido pelo
`cmd/hyrule`), nenhuma abstração de provedor de deploy, e nenhum
esquema de versionamento por serviço. Essas são exatamente as decisões
que as próprias regras do projeto dizem para parar e apresentar antes
de executar — o ADR-0002, o ADR-0003, o ADR-0004 e o ADR-0006 são essa
apresentação para cada uma delas.

## Evolução proposta — Fase 6+ (2026-11-16)

A Fase 6 tem [estudo concluído](phase-6-contract-study.md) e correções de
contrato; a aceitação formal permanece aberta. As Fases 7–8 possuem JWT/política,
leituras limitadas, descoberta autorizada e helpers HTTP loopback opcionais. A
Fase 9 tem provas SDK MCP em memória/stdio; cliente de produção/transporte remoto continuam
abertos. A Fase 10 de mutações MCP controladas não foi iniciada. A Fase 11 possui
retrieval documental, avaliação local com modelo real, fonte Postgres versionada/
outbox e [produtor concreto de artigos](knowledge-producer.md). Implantação e
aceitação mais ampla de qualidade permanecem abertas. Esses incrementos não
significam aceitação integral dos [ADRs 0007–0012](architecture-decisions.md) ou
conclusão de todas as fases. Os gates v1 e a numeração histórica de portabilidade
Postgres são independentes. Complexidade é relativa, sem prazo/equipe prometidos.

| Fase | Problema / benefício | Complexidade | Risco principal | Dependências | Done-when |
|---|---|---|---|---|---|
| **6 — Contrato e DX** | Evitar schema paralelo; melhorar diagnóstico e decidir codegen com evidência | Média | Expandir para ORM/compilador | ADR-0007; consumidor de recurso declarativo e Go | Comparação de contratos validada; limites não serializáveis documentados; origem por recurso definida; decisão de gerar ou adiar baseada no estudo. |
| **7 — Executor seguro e consulta opt-in** | Reutilizar `Service` sem perder autenticação, política e limites | Alta | Acesso entre tenants ou bypass do handler | ADR-0008 e ADR-0010; consumidor e política definidos | Uma consulta real registrada explicitamente; identidade/política fail-closed; limites, isolamento, projeção e paridade com REST verificados. Mutações por ferramentas continuam desabilitadas. |
| **8 — Context Discovery** | Oferecer contratos úteis e autorizados | Média | Vazamento de metadados/cache | Fases 6–7; ADR-0009 | Catálogo determinístico por escopo, contratos de entrada/saída e restrições corretos; cache isolado; sem dados sensíveis; OpenAPI sem regressão. |
| **9 — Piloto MCP de leitura** | Validar interoperabilidade com cliente real | Média/alta | Compatibilidade de protocolo e sessão | Fases 7–8; ADR-0011; SDK/versão/transporte escolhidos | Cliente real lista e chama a consulta; autenticação e política comprovadas; erros/cancelamento/isolamento validados; configuração inteiramente opt-in. |
| **10 — Mutações controladas** | Permitir ação útil com concorrência e rastreabilidade | Alta | Duplicação e efeitos externos indevidos | Fases 7–9; consumidor de escrita; ADR-0010 completo | Uma mutação real preserva ETag, autorização, validação/hooks e outbox quando aplicável; idempotência e aprovação vinculada validadas; timeout ambíguo tratado; dry-run anunciado apenas onde demonstrado. |
| **11 — Retrieval e RAG condicionado** | Melhorar recuperação de conhecimento se baseline justificar | Alta | Qualidade, custo e vazamento pelo índice | ADR-0012; corpus/consultas/ACL/budget; outbox Mongo ou Postgres | Baseline e limiares acordados; pipeline idempotente e avaliação reproduzível passam; revogação/exclusão e reindexação comprovadas. Piloto de documentação estática/pgvector/MCP implementado; avaliação local com modelo real e fonte Postgres versionada/outbox implementados; aceitação ampla e produtores de produção permanecem abertos. Ver [piloto](knowledge-pilot.md). |

Sequência principal: 6 → 7 → 8 → 9 → 10. Fase 11 é um ramo independente de MCP e de
mutações via IA; pode começar após contrato/política e consumidor definidos. Não há
motivo para exigir um servidor MCP para pesquisar documentos.

### Definição do piloto de leitura — 2026-11-16

O [piloto tags.lookup](read-pilot.md) seleciona recurso, entrada, saída, limites e
política proposta de acesso para um dataset sintético local. O pacote interno do exemplo standalone implementa executor e descoberta
autorizados por callbacks injetados, com auditoria obrigatória e testes SQLite.
A Fase 7 segue parcial: provedor/transporte de produção, consumidor de produção
e política de produção continuam pendentes.

### Estratégia de verificação nas futuras implementações

Reutilizar `testkit`, suites de adapters e gates de CI existentes. Testes novos devem
provar invariantes de fronteira: autorização antes de executar, limites antes de
consultar, paridade de validação/ETag/hook, isolamento entre identidades, ausência de
efeitos no modo anunciado e tratamento de falhas. Não adicionar testes que apenas
espelhem DTOs ou retornem o mesmo descritor montado pelo código sob teste.

A prova local de leitura pode usar SQLite para baixar custo, dentro de suas capacidades;
a prova de outbox/rollback exige os backends que realmente suportam esses recursos. A
cobertura de 95% é gate já existente, não evidência de segurança suficiente por si só.
Verificar também o consumo externo dos novos pacotes e os exemplos sem dependência de
credenciais de IA.

### Decisões que continuam explicitamente abertas

Implantação de consumidores/políticas em produção; origem e direção de eventual
codegen; formato da API neutra de exportação de contrato; nome/distribuição dos pacotes;
transporte MCP de produção e adoção por cliente; armazenamento de aprovações/idempotência; provedor de
embeddings em produção e metas ampliadas de qualidade/carga RAG. Essas escolhas dependem de evidência
identificada nas fases correspondentes e não impedem revisar as propostas.

O piloto já integra verificação JWT RS256 e política de scope, com testes de
assinaturas reais. O sink de auditoria SQLite persistente também está disponível. Configuração
do provedor de produção e transporte externo continuam pendentes; a Fase 7 segue parcial.

O transporte HTTP local opt-in do piloto já tem descoberta e execução protegidas
por JWT/auditoria, testes loopback reais e limites de leitura/escrita. Deploy
remoto, provedor de produção e MCP permanecem pendentes; a Fase 7 segue parcial.

A descoberta local do piloto agora projeta schemas de entrada/saída e limites do
contrato, com hash determinístico e autorização/auditoria a cada chamada. A Fase 8
segue parcial: catálogo genérico e cache por perfil permanecem abertos; ver adapter MCP local abaixo.

O [adapter MCP local](mcp-pilot.md) implementa tools/list e tools/call reutilizando
o executor, com SDK v1.8.0/protocolo 2025-11-25 e cliente SDK real em memória/stdio.
A Fase 9 segue parcial quanto a adoção por consumidor de produção e MCP remoto.

O comando opt-in `cmd/tags-mcp` agora permite iniciar o piloto por cliente stdio,
com configuração explícita, origem SQLite somente leitura e auditoria separada.
O executável foi validado por cliente SDK real; adoção desktop/produção segue aberta.

### Documentação ainda necessária para adoção em produção

Os guias atuais cobrem configuração local, grants, ingestão, fonte/outbox, CRUD de
curadoria e avaliação. A documentação específica de produção deve acompanhar
integrações comprovadas:

- Configuração do cliente MCP escolhido e matriz de transporte/autenticação testada.
- Topologia de implantação entre setup-tools, risotto-site e hub, incluindo recursos,
  NetworkPolicies, rotas, segredos e rollback.
- Desenho Authelia: identidade confiável, tipo/audiência/scopes de token, rotação de
  chaves e autorização documental. Headers forwardAuth não são aceitos implicitamente
  pelo verificador JWT atual; essa integração não foi implementada.
- Backup/restore, retenção de auditoria/tombstones, alertas de worker/DLQ e procedimentos
  de replay específicos do ambiente, acompanhados de testes de recuperação.
- Critérios acordados de qualidade/carga/freshness além do experimento local registrado.

São entregas abertas de documentação/integração; não significam ausência da
fonte/outbox ou do produtor de artigos já implementados. A aceitação formal dos
ADRs continua como decisão de revisão independente da evidência de implementação.
