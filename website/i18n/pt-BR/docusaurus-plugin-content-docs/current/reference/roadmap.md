---
sidebar_position: 11
---

# Roadmap: framework e plataforma

Esta página é um plano, não uma lista de funcionalidades — a maioria
das fases abaixo é trabalho proposto, não algo já construído; cada uma
diz explicitamente quando isso muda. Ela existe pra que as [Decisões
arquiteturais](architecture-decisions.md) tenham uma sequência pra se
encaixar, e pra que ninguém confunda uma fase futura com comportamento
atual.

## Por que dois caminhos

A direção do projeto é oferecer ao Hyrule dois caminhos de adoção
complementares, sobre o mesmo núcleo: um caminho **Framework** (`go
get`, sem exigir monorepo ou CLI) e um caminho **Platform** (um
monorepo, CLI e scaffolding, pra equipes que querem uma configuração
padronizada de múltiplos serviços). A partir da Fase 1, o caminho
Framework é consumível externamente (ver ADR-0001), e a partir da Fase
3, o caminho Platform tem seus dois primeiros comandos (ver ADR-0004)
— as duas linhas de gap abaixo ficam pra registro histórico, não
porque ainda estejam abertas.

## Gap analysis

| Área | Visão | Estado real hoje | Gap |
|---|---|---|---|
| Framework (`go get`) | Consumível por qualquer app Go externa | `app`, `rest`, `outbox`, `testkit`, `declarative`, `behaviors/*`, `platform/*` são públicos; só `internal/domain` continua interno | **Fechado** — Fase 1 (verificado: um módulo externo de rascunho importa `rest`, `app`, `declarative`, `platform/mongodb`) |
| `rest.Store` entre backends | Portável entre Mongo e Postgres | Tipos do Mongo vazam pro contrato | **Alto** — precisa de uma decisão de design antes de qualquer código se mover (ADR-0002) |
| Outbox entre backends | Implícito pela narrativa dual-mode | Fixo no Mongo | **Alto**, corretamente bloqueado até a questão do Store ser resolvida (ADR-0003) |
| CLI / Platform | `hyrule new` / `service create` / `dev` / `build` / `deploy` | `cmd/hyrule` tem `new` e `service create` (ADR-0004); `dev`/`build --affected`/`deploy` nem começaram | **Parcialmente fechado** — scaffolding da Fase 3 pronto; envolver `ci/affected`/Makefile foi adiado de propósito |
| Build afetado / deploy independente | Orquestrado pela plataforma | `ci/affected` funciona sozinho, não ligado ao CI nem ao CLI; um manifesto Kubernetes ilustrativo | **Médio** — a parte difícil existe, a orquestração não |
| Documentação de duas jornadas | Learn (Framework) + Learn (Platform) + Reference compartilhada | Só existe a jornada Framework, e ela já é honesta sobre o que falta | **Médio** — não escrever a jornada Platform antes do CLI ser real |
| ADRs / versionamento | Um processo formal de decisão e compatibilidade | Não existia antes desta página | **Gap alto, custo baixo** — pura documentação |

## Fases

- **Fase 0 (esta rodada).** Diagnóstico, ADRs e este roadmap,
  publicados. Risco: nenhum (só documentação). Esforço: baixo.
  **Concluída.**
- **Fase 1 — Consolidar o framework.** Executar o ADR-0001 (promover os
  pacotes que um consumidor precisa, tirando-os de `internal/`) e
  manter a linha do ADR-0002 (sem generics em `rest.Store` ainda).
  **Concluída.** `app`, `behaviors/*`, `declarative`, `outbox`,
  `platform/*`, `rest` e `testkit` se moveram pra raiz do módulo
  (`internal/domain` é o único pacote que continua interno); `go build
  ./...`, `go vet ./...` e `make cover` (90,9%, gate de 85%) passam
  todos, e um módulo externo de rascunho construído contra os novos
  caminhos compila e roda. O `rest.Store` em si continua inalterado,
  conforme o ADR-0002.
- **Fase 2 — Validar o modo standalone.** Construir um exemplo externo
  de verdade: um módulo Go separado, com seu próprio `go.mod`,
  importando o Hyrule publicado — não mais um pacote dentro de
  `examples/blog-apis`. **Concluída.**
  [`examples/standalone-tags-api`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/standalone-tags-api)
  é um módulo próprio (`replace` pra este checkout até uma versão ser
  taggeada — ver o README dele), com um resource declarado em YAML, um
  hook em Go (`behaviors/audit`), um teste usando `testkit`, e um ciclo
  real de `go run .` + `curl`, tudo verificado.
- **Fase 3 — Platform / CLI.** Implementar o menor CLI útil conforme o
  ADR-0004. **Concluída, só scaffolding.** O `cmd/hyrule` (`go install
  github.com/Gui-Rondon/hyrule/cmd/hyrule`) tem `hyrule new <project>`
  e `hyrule service create <name>`, com templates embarcados pro
  `go.mod`/`Makefile`/`docker-compose.yml` de um projeto e pro
  `app.Main` + um resource + um teste com `testkit` de um serviço.
  Verificado de ponta a ponta: `hyrule new` → `hyrule service create`
  → `go mod tidy` → `make build` → `make test` → `make run` + `curl`
  tudo funciona, sem copiar `examples/blog-apis` na mão. O `ci/affected`
  e o padrão do Makefile *não* foram envolvidos como subcomandos ainda
  — o ADR-0004 adiou isso de propósito até existir um segundo comando
  que precise deles de verdade.
- **Fase 4 — Experiência operacional.** Ligar o `ci/affected` a um
  workflow de CI de verdade; tornar o deploy configurável em vez de
  genérico. Depende da Fase 3.
- **Fase 5 — Documentação.** Escrever a jornada Platform só quando o
  CLI for real, pra que o site publicado nunca descreva um comando que
  ainda não existe.

## O que esta rodada não faz

Nenhuma mudança na interface do `rest.Store`, nenhuma portabilidade de
outbox, nenhum subcomando de CLI pra `dev`/`build --affected`/`deploy`,
e o `ci/affected` continua uma ferramenta solta, sem wrapper. Essas são
exatamente as decisões que as próprias regras do projeto dizem pra
parar e apresentar antes de executar — o ADR-0002, o ADR-0003 e o
ADR-0004 são essa apresentação pra cada uma delas.
