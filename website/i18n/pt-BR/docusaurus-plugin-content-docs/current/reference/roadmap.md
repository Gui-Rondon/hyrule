---
sidebar_position: 11
---

# Roadmap: framework e plataforma

Esta página é um plano, não uma lista de funcionalidades — toda fase
abaixo é trabalho proposto, não algo já construído. Ela existe pra que
as [Decisões arquiteturais](architecture-decisions.md) tenham uma
sequência pra se encaixar, e pra que ninguém confunda uma fase futura
com comportamento atual. A Fase 0 é a única fase que esta rodada de
fato completa.

## Por que dois caminhos

A direção do projeto é oferecer ao Hyrule dois caminhos de adoção
complementares, sobre o mesmo núcleo: um caminho **Framework** (`go
get`, sem exigir monorepo ou CLI) e um caminho **Platform** (um
monorepo, CLI e scaffolding, pra equipes que querem uma configuração
padronizada de múltiplos serviços). Hoje só existe uma versão informal
do caminho Framework, e ela ainda não é consumível externamente (ver
ADR-0001). O caminho Platform não existe além de alguns blocos
reutilizáveis (`ci/affected`, targets do Makefile, um monorepo de
exemplo).

## Gap analysis

| Área | Visão | Estado real hoje | Gap |
|---|---|---|---|
| Framework (`go get`) | Consumível por qualquer app Go externa | Tudo vive dentro de `internal/` | **Crítico** — nada é importável hoje |
| `rest.Store` entre backends | Portável entre Mongo e Postgres | Tipos do Mongo vazam pro contrato | **Alto** — precisa de uma decisão de design antes de qualquer código se mover (ADR-0002) |
| Outbox entre backends | Implícito pela narrativa dual-mode | Fixo no Mongo | **Alto**, corretamente bloqueado até a questão do Store ser resolvida (ADR-0003) |
| CLI / Platform | `hyrule new` / `service create` / `dev` / `build` / `deploy` | Nenhum subcomando existe | **Crítico**, mas já existem blocos reutilizáveis (`ci/affected`, Makefile, monorepo de exemplo) |
| Build afetado / deploy independente | Orquestrado pela plataforma | `ci/affected` funciona sozinho, não ligado ao CI; um manifesto Kubernetes ilustrativo | **Médio** — a parte difícil existe, a orquestração não |
| Documentação de duas jornadas | Learn (Framework) + Learn (Platform) + Reference compartilhada | Só existe a jornada Framework, e ela já é honesta sobre o que falta | **Médio** — não escrever a jornada Platform antes do CLI ser real |
| ADRs / versionamento | Um processo formal de decisão e compatibilidade | Não existia antes desta página | **Gap alto, custo baixo** — pura documentação |

## Fases

- **Fase 0 (esta rodada).** Diagnóstico, ADRs e este roadmap,
  publicados. Risco: nenhum (só documentação). Esforço: baixo.
  **Concluída.**
- **Fase 1 — Consolidar o framework.** Executar o ADR-0001 (promover os
  pacotes que um consumidor precisa, tirando-os de `internal/`) e
  manter a linha do ADR-0002 (sem generics em `rest.Store` ainda).
  Risco: alto — é uma mudança quebradora, que afeta a identidade do
  produto, e precisa de aprovação explícita antes de começar, conforme
  a própria regra do projeto sobre parar antes de decisões assim.
- **Fase 2 — Validar o modo standalone.** Construir um exemplo externo
  de verdade: um módulo Go separado, com seu próprio `go.mod`,
  importando o Hyrule publicado — não mais um pacote dentro de
  `examples/blog-apis`. Depende da Fase 1.
- **Fase 3 — Platform / CLI.** Implementar o menor CLI útil conforme o
  ADR-0004 (`hyrule new`, `hyrule service create`), reaproveitando
  `ci/affected` e o Makefile em vez de reimplementá-los. Risco: médio.
  Esforço: alto.
- **Fase 4 — Experiência operacional.** Ligar o `ci/affected` a um
  workflow de CI de verdade; tornar o deploy configurável em vez de
  genérico. Depende da Fase 3.
- **Fase 5 — Documentação.** Escrever a jornada Platform só quando o
  CLI for real, pra que o site publicado nunca descreva um comando que
  ainda não existe.

## O que esta rodada não faz

Nenhum código dentro de `internal/` se move. Nenhuma interface muda.
Nenhum CLI é escrito. Essas são exatamente as decisões que as próprias
regras do projeto dizem pra parar e apresentar antes de executar —
este roadmap é essa apresentação.
