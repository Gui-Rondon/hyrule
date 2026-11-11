---
sidebar_position: 10
---

# Decisões arquiteturais

Esta página registra decisões, não funcionalidades — é o resultado de
uma auditoria de Fase 0 feita no código real (não um plano escrito a
partir de suposições). A linha **Status** de cada ADR acompanha se a
decisão foi de fato implementada e validada, separadamente de se a
decisão em si foi aceita — aceitar uma direção e construí-la são dois
fatos diferentes, e esta página não confunde os dois. Ver o
[`roadmap.md`](roadmap.md) pra como as fases que implementam essas
decisões se sequenciam entre si.

## ADR-0001 — Superfície pública do framework

**Status:** Implementado

**Implementado em:** PR #6 (Fase 1, commit `2385f9e`)

**Contexto.** Todo pacote vive dentro de `internal/`:
`app`, `rest`, `behaviors/*`, `outbox`, `platform/*`, `testkit`,
`declarative`, `domain/slug`. Não existe `pkg/`. `examples/blog-apis`
importa `internal/app` direto, mas só porque está dentro do mesmo
módulo — a regra de visibilidade `internal/` do Go significa que nenhum
módulo externo pode importar nada disso hoje, independente do que o
badge do pkg.go.dev no README sugira.

**Problema.** Um consumidor não consegue fazer `go get
github.com/Gui-Rondon/hyrule` e construir nada com isso.

**Alternativas consideradas.**
- Mover os pacotes do núcleo pra um novo diretório `pkg/` — rejeitada;
  a própria regra do projeto contra criar `pkg/` "apenas por
  convenção" se aplica, e o Go não exige isso.
- Promover exatamente os pacotes que um consumidor externo precisa —
  `rest`, `app`, `outbox`, `testkit`, `declarative`, `behaviors/*`, e
  os adapters de `platform/*` que um consumidor instancia direto
  (`mongodb`, `postgres`, `redis`, `pubsub`, `auth`, `config`,
  `health`, `metrics`, `server`) — de `internal/` pra raiz do módulo,
  com os mesmos nomes de pacote.
- Manter tudo interno e construir um pacote público separado de "SDK"
  que reexporta um subconjunto curado — rejeitada; dobra a manutenção
  da mesma superfície e cria exatamente o tipo de implementação
  paralela que o projeto está tentando evitar.

**Decisão.** A segunda opção. `domain/slug` fica interno até que
surja uma necessidade externa real pra ele. Este ADR registra o
destino; mover o código é Fase 1, não esta rodada.

**Consequências.** `cmd/hyrule-serve` e `examples/blog-apis` vão
precisar dos caminhos de import atualizados quando a Fase 1 executar.
A superfície de compatibilidade do módulo cresce de zero pra tudo que
é exportado por esses pacotes.

**Riscos.** Diff mecânico grande, tocando todo arquivo dos pacotes
movidos; risco de exportar algo de cada pacote que devia ter ficado
como detalhe de implementação, sem uma passada deliberada por pacote
pra decidir o que é de fato público.

**Critério de validação.** Um módulo Go de rascunho separado (seu
próprio `go.mod`) consegue `go get github.com/Gui-Rondon/hyrule` e
construir um resource mínimo usando `rest`, `app`, `declarative` e
`platform/mongodb`, sem importar nada de dentro de `internal/`.

## ADR-0002 — Portabilidade do contrato `rest.Store`

**Status:** Aceito — implementação adiada

**Implementado em:** não se aplica; a própria decisão é adiar o
rework com generics (ver Decisão abaixo). O `rest.Store` continua
inalterado no PR #6, conforme decidido.

**Contexto.** Os métodos de `rest.Store` são tipados em
`bson.M`/`bson.D`, e a tradução de erro de `rest.Service` checa tipos
do driver Mongo direto (`mongo.ErrNoDocuments`,
`mongo.IsDuplicateKeyError`, `mongo.IsTimeout` — ver
[`store.md`](store.md)). [`postgres-rfc.md`](postgres-rfc.md) já
decidiu, de forma independente, que `postgres.Store` não tenta
implementar `rest.Store`.

**Problema.** Pra promoção pública do ADR-0001 significar algo pro
caminho do Postgres, alguma coisa precisa impedir que o framework
assuma silenciosamente erros no formato do Mongo quando um consumidor
puder de fato escolher um backend.

**Alternativas consideradas.**
- Generificar `rest.Store`/`Service`/`Resource` com generics do Go
  (`Store[F, U any]`) — o esforço maior que `postgres-rfc.md` nomeia,
  tocando a maior parte de `internal/rest/service.go`.
- Contornar `rest` completamente pros resources com Postgres — o
  caminho de menor custo que `postgres-rfc.md` já recomenda, o mesmo
  padrão que `internal/outbox` já usa falando com `*mongo.Collection`
  direto.
- Manter `rest.Store` no formato do Mongo como contrato "de
  referência" e construir um segundo caminho de HTTP/resource
  independente pro Postgres — rejeitada de cara: isso é dois
  frameworks, e é exatamente o tipo de diferença entre Mongo e
  Postgres escondida que as próprias regras do projeto proíbem.

**Decisão.** Adiar o rework com generics. Até que um resource real com
Postgres seja necessário em produção, o caminho de contorno continua
sendo a forma suportada de usar `postgres.Store`, exatamente como
`postgres-rfc.md` recomenda. A Fase 1 não vai tentar generificar
`rest.Store`. Este ADR existe pra isso ser uma decisão explícita e
revisitável, não uma paralisação implícita.

**Consequências.** A futura documentação "Storage and database
adapters" da jornada Framework só pode descrever honestamente o Mongo
como o backend integrado com `rest`; o Postgres continua documentado
como um pacote independente, de nível mais baixo, até o rework com
generics acontecer de fato.

**Riscos.** Bloqueia qualquer necessidade de curto prazo de um resource
integrado ao `rest` que não seja Mongo. Gatilho pra revisitar: o
primeiro resource real que precise tanto da maquinaria HTTP do `rest`
quanto de um store que não seja Mongo.

**Critério de validação.** Nenhuma documentação descreve um resource
Postgres passando por `rest.Store`.

## ADR-0003 — Outbox e Postgres

**Status:** Aceito — implementação adiada

**Implementado em:** não se aplica; a própria decisão é esperar por um
resource real com Postgres (ver Decisão abaixo). O `outbox` (movido de
`internal/outbox` pelo ADR-0001/Fase 1) continua inalterado no PR #6,
conforme decidido.

**Contexto.** `internal/outbox` é fixo em `*mongo.Collection`/
`*mongo.Database`, escreve um resource e seu evento na mesma transação
do Mongo, e documenta a entrega como at-least-once, nunca
exactly-once. [`postgres-rfc.md`](postgres-rfc.md), seção 9,
esboça uma alternativa nativa de Postgres (`BEGIN`/`COMMIT`,
`LISTEN`/`NOTIFY`, `FOR UPDATE SKIP LOCKED`) mas diz que depende do
ADR-0002 acontecer primeiro.

**Problema.** Se vale começar a construir portabilidade de outbox
agora, especulativamente, ou esperar por um consumidor real.

**Alternativas consideradas.**
- Construir uma interface de outbox genérica em relação ao store
  agora — rejeitada: não há um consumidor real pra validar o design
  contra, exatamente a "abstração genérica sem consumidores reais"
  que o projeto proíbe.
- Esperar até o ADR-0002 ser revisitado e existir um resource real com
  Postgres, então construir a contraparte de outbox do Postgres
  guiada pelas necessidades reais daquele resource.

**Decisão.** A segunda opção. Outbox continua Mongo-only; nenhuma
mudança de código nesta rodada ou na Fase 1.

**Consequências.** A narrativa de "dois caminhos complementares, mesmo
núcleo" ainda não pode reivindicar portabilidade de outbox —
a documentação deve dizer isso claramente, não insinuar.

**Riscos.** Nenhum por continuar como está. O único risco é construir
a versão genérica antes do ADR-0002 desbloquear isso.

**Critério de validação.** Nenhuma documentação ou ADR afirma que
existe um outbox Postgres funcionando até que um resource de fato use
um.

## ADR-0004 — Escopo inicial do CLI / Platform

**Status:** Implementado

**Implementado em:** PR #6 (Fase 3, commit `fff7a61`)

**Contexto.** Não existe CLI multi-comando. `cmd/hyrule-serve` é um
único binário genérico de servidor. `ci/affected` já faz detecção real
de build afetado como ferramenta independente (aceita `-services` pra
layouts fora de `examples/blog-apis`) mas não está ligado a nenhum
workflow de CI ou CLI. O `Makefile` já tem targets `build`/`image`/
`run`/`env-up` parametrizados por `SERVICE`.

**Problema.** A visão de Platform do prompt (`hyrule new`,
`service create`, `dev`, `test`, `build --affected`, `deploy`) é
grande; construir tudo isso antes de qualquer parte ter um usuário
real arrisca "implementar comandos apenas pra preencher uma lista."

**Alternativas consideradas.**
- Construir a superfície completa de comandos de uma vez.
- Começar só com scaffolding de projeto/serviço (`hyrule new`,
  `hyrule service create`), reaproveitando o Makefile e o
  `ci/affected` pra tudo relacionado a build/test/afetado em vez de
  reimplementá-los como subcomandos Go imediatamente.
- Envolver `ci/affected` e os targets do Makefile como subcomandos
  primeiro, deixando o scaffolding pra depois.

**Decisão.** Começar só com scaffolding. O primeiro valor real de um
CLI é eliminar a cópia manual de `examples/blog-apis/services/*` ao
começar um novo serviço — `ci/affected`, o Makefile e o modelo de
manifesto único já funcionam e não precisam de um wrapper de CLI pra
serem úteis hoje. Envolver as ferramentas existentes como subcomandos
fica pra depois, quando o CLI já existir como binário.

**Consequências.** A documentação da jornada Platform deve descrever
só `hyrule new` e `hyrule service create` no início; os subcomandos
`dev`/`test`/`build`/`deploy` ficam como trabalho futuro, e devem ser
marcados como tal onde quer que sejam mencionados.

**Riscos.** Só scaffolding pode parecer pouco em relação à visão
completa do prompt — intencional; é o menor incremento que ainda é
valor real e utilizável.

**Critério de validação.** Uma pessoa consegue rodar `hyrule new
my-platform && hyrule service create articles` e obter um serviço que
builda e roda pelos targets existentes do Makefile, sem copiar
`examples/blog-apis` manualmente.

## ADR-0005 — Versionamento e compatibilidade pré-1.0

**Status:** Implementado

**Implementado em:** PR #6 (Fase 0, commit `b19f31c`); toda fase desde
então adicionou sua própria entrada no `CHANGELOG.md`, na prática, não
só no papel.

**Contexto.** Sem `CHANGELOG`, uma tag de git (`v0.100.0`), sem
política de compatibilidade documentada. `go.mod` sem sufixo de versão
major, consistente com 0.x.

**Problema.** A Fase 1 está pra introduzir as primeiras mudanças
quebradoras de verdade do repo (mover pacotes de `internal/` pra
caminhos de import públicos) sem lugar nenhum pra registrar o que
mudou e quando.

**Decisão.** Adotar o formato [Keep a Changelog](https://keepachangelog.com/)
em `CHANGELOG.md`, e tags SemVer 0.x a partir de agora (uma mudança
quebradora sobe o minor, já que o major continua `0`). Começar o
changelog agora, antes da Fase 1 executar, pra que a primeira entrada
de verdade já tenha um lugar pra ir.

**Consequências.** PRs futuros que mudam comportamento público devem
adicionar uma entrada no changelog; nenhuma checagem de CI força isso
nesta rodada.

**Riscos.** Baixo — puramente aditivo.

**Critério de validação.** `CHANGELOG.md` existe, segue a estrutura do
Keep a Changelog, e tem uma seção `Unreleased` pronta pras entradas da
Fase 1.

## ADR-0006 — Escopo operacional da Fase 4: CI de build afetado, não um sistema de deploy

**Status:** Implementado

**Implementado em:** PR #6 (Fase 4, commit `b07df31`)

**Contexto.** `ci/affected` funciona (o próprio teste dele exercita o
grafo de dependência real do `examples/blog-apis` — ver
[`store.md`](store.md) e o doc do pacote de `ci/affected/main.go`) mas
nenhum workflow de CI chama ele. O único artefato de deploy é um
manifesto Kubernetes ilustrativo
(`examples/blog-apis/deploy/posts-api/manifest.yml`), não ligado a
nada, e específico do formato de um provedor.

**Problema.** Os objetivos da Fase 4 do prompt — "evoluir
build-affected," "integrar build independente," "oferecer templates de
CI/CD," "permitir deploy independente com adapters configuráveis" —
são fáceis de super-construir num sistema de deploy multi-cloud
genérico, exatamente o que a regra 6 do prompt ("não implemente um
sistema universal de deploy") e a regra de CI/CD ("não acople o
Hyrule a um único provedor") proíbem explicitamente. Também não existe
um consumidor real multi-provedor ainda pra validar essa abstração —
o mesmo problema de "sem consumidor real" que o ADR-0003 nomeou pra
portabilidade do outbox.

**Alternativas consideradas.**
- Construir uma camada de abstração de provedor (interfaces pra GitHub
  Actions/GitLab CI, pra Kubernetes/Cloud Run/Fly/etc.) — rejeitada
  pelo mesmo motivo: nenhum segundo provedor real pra desenhar contra
  ainda.
- Documentar o padrão só em texto, sem um workflow de verdade rodando
  — rejeitada: "ligar a um workflow de CI real" (a própria frase do
  roadmap pra Fase 4) implica algo que de fato roda, não só uma
  descrição de como poderia.
- Adicionar um workflow real do GitHub Actions, com escopo em
  `examples/blog-apis`, que chama o `ci/affected` pra selecionar os
  serviços alterados, builda só esses, e — a costura configurável —
  chama um `deploy/<service>/deploy.sh` opcional se o serviço tiver
  um, deixado como um stub que imprime o que faria em vez de tocar num
  ambiente real. O `deploy.sh` é o "adapter" completo: o comando de
  deploy real de qualquer provedor vai lá, o Hyrule nunca vê isso.

**Decisão.** A terceira opção. Build independente é real (um `go
build` por serviço afetado no CI, não um rebuild do repo inteiro);
deploy independente é um ponto de extensão documentado
(`deploy/<service>/deploy.sh`), não uma capacidade entregue — não tem
o que configurar ainda porque não existe um ambiente de destino real
pra esse exemplo público fazer deploy. Versionamento por serviço (o
objetivo 4 da Fase 4 do prompt) fica sem começar: esses serviços de
exemplo não são publicados de forma independente hoje, e inventar um
esquema sem consumidor real repete o erro pelo qual as alternativas
acima foram rejeitadas.

**Consequências.** Esse workflow é uma responsabilidade do próprio
`examples/blog-apis` — ilustrativo, como o manifesto Kubernetes dele —
não uma capacidade que o framework ou o CLI do Hyrule prometem como
parte do seu contrato público. Um time usando `hyrule new` copia e
adapta o padrão; o Hyrule não entrega um subcomando "deploy" que chama
isso.

**Riscos.** Alguém pode confundir o workflow com uma integração de CI
suportada pelo Hyrule, em vez de um template pra copiar — mitigado por
comentários no arquivo do workflow e por este ADR, o mesmo
enquadramento honesto que o [`postgres-rfc.md`](postgres-rfc.md) já
usa.

**Critério de validação.** Um push ou PR que só toca os arquivos de um
serviço faz o workflow reportar e buildar só esse serviço, em menos
tempo que um rebuild completo; um push tocando um caminho compartilhado
(go.mod, `platform/*`, `Dockerfile`) faz ele reportar e buildar todos.
O stub `deploy.sh` de cada serviço roda sem erro e não faz nenhuma
mudança externa real.
