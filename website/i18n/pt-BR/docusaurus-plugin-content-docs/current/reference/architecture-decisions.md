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
[`roadmap.md`](roadmap.md) para entender como as fases que implementam essas
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
- Mover os pacotes do núcleo para um novo diretório `pkg/` — rejeitada;
  a própria regra do projeto contra criar `pkg/` "apenas por
  convenção" se aplica, e o Go não exige isso.
- Promover exatamente os pacotes que um consumidor externo precisa —
  `rest`, `app`, `outbox`, `testkit`, `declarative`, `behaviors/*`, e
  os adapters de `platform/*` que um consumidor instancia direto
  (`mongodb`, `postgres`, `redis`, `pubsub`, `auth`, `config`,
  `health`, `metrics`, `server`) — de `internal/` para raiz do módulo,
  com os mesmos nomes de pacote.
- Manter tudo interno e construir um pacote público separado de "SDK"
  que reexporta um subconjunto curado — rejeitada; dobra a manutenção
  da mesma superfície e cria exatamente o tipo de implementação
  paralela que o projeto está tentando evitar.

**Decisão.** A segunda opção. `domain/slug` fica interno até que
surja uma necessidade externa real para ele. Este ADR registra o
destino; mover o código é Fase 1, não esta rodada.

**Consequências.** `cmd/hyrule-serve` e `examples/blog-apis` vão
precisar dos caminhos de import atualizados quando a Fase 1 executar.
A superfície de compatibilidade do módulo cresce de zero para tudo que
é exportado por esses pacotes.

**Riscos.** Diff mecânico grande, tocando todo arquivo dos pacotes
movidos; risco de exportar algo de cada pacote que devia ter ficado
como detalhe de implementação, sem uma passada deliberada por pacote
para decidir o que é de fato público.

**Critério de validação.** Um módulo Go de rascunho separado (seu
próprio `go.mod`) consegue `go get github.com/Gui-Rondon/hyrule` e
construir um recurso mínimo usando `rest`, `app`, `declarative` e
`platform/mongodb`, sem importar nada de dentro de `internal/`.

## ADR-0002 — Portabilidade do contrato `rest.Store`

**Status:** Aceito, depois revisado (ver Revisão abaixo) — o redesenho
com generics continua adiado, exatamente como decidido; um ponto de integração
diferente (`Adapter`) é o que de fato foi construído.

**Implementado em:** não se aplica na época em que este ADR foi
escrito (a decisão era adiar o redesenho com generics, ver Decisão
abaixo); o `rest.Store` continuou inalterado no PR #6, conforme
decidido. Ver Revisão para o que foi implementado em vez disso, a partir
do PR #10.

**Contexto.** Os métodos de `rest.Store` são tipados em
`bson.M`/`bson.D`, e a tradução de erro de `rest.Service` checa tipos
do driver Mongo direto (`mongo.ErrNoDocuments`,
`mongo.IsDuplicateKeyError`, `mongo.IsTimeout` — ver
[`store.md`](store.md)). [`postgres-rfc.md`](postgres-rfc.md) já
decidiu, de forma independente, que `postgres.Store` não tenta
implementar `rest.Store`.

**Problema.** Pra promoção pública do ADR-0001 significar algo para o
caminho do Postgres, alguma coisa precisa impedir que o framework
assuma silenciosamente erros no formato do Mongo quando um consumidor
puder de fato escolher um backend.

**Alternativas consideradas.**
- Generificar `rest.Store`/`Service`/`Resource` com generics do Go
  (`Store[F, U any]`) — o esforço maior que `postgres-rfc.md` nomeia,
  tocando a maior parte de `internal/rest/service.go`.
- Contornar `rest` completamente para os recursos com Postgres — o
  caminho de menor custo que `postgres-rfc.md` já recomenda, o mesmo
  padrão que `internal/outbox` já usa falando com `*mongo.Collection`
  direto.
- Manter `rest.Store` no formato do Mongo como contrato "de
  referência" e construir um segundo caminho de HTTP/resource
  independente para o Postgres — rejeitada de cara: isso é dois
  frameworks, e é exatamente o tipo de diferença entre Mongo e
  Postgres escondida que as próprias regras do projeto proíbem.

**Decisão.** Adiar o redesenho com generics. Até que um recurso real com
Postgres seja necessário em produção, o caminho de contorno continua
sendo a forma suportada de usar `postgres.Store`, exatamente como
`postgres-rfc.md` recomenda. A Fase 1 não vai tentar generificar
`rest.Store`. Este ADR existe para isso ser uma decisão explícita e
revisitável, não uma paralisação implícita.

**Consequências.** A futura documentação "Armazenamento e adaptadores
de banco de dados" da jornada Framework só pode descrever honestamente o Mongo
como o backend integrado com `rest`; o Postgres continua documentado
como um pacote independente, de nível mais baixo, até o redesenho com
generics acontecer de fato.

**Riscos.** Bloqueia qualquer necessidade de curto prazo de um recurso
integrado ao `rest` que não seja Mongo. Gatilho para revisitar: o
primeiro recurso real que precise tanto da maquinaria HTTP do `rest`
quanto de um store que não seja Mongo.

**Critério de validação.** Nenhuma documentação descreve um recurso
Postgres passando por `rest.Store`.

### Revisão — 2026-11, Fase 8 da portabilidade Postgres

**O que mudou.** Um recurso Postgres real acabou sendo necessário
antes do gap de generics ser revisitado por conta própria — o próprio
gatilho de revisão deste ADR ("o primeiro recurso real que precise
tanto da maquinaria HTTP do `rest` quanto de um store que não seja
Mongo"). Nenhuma das três alternativas originais foi seguida como
estava escrita: nem o redesenho com generics (`Store[F, U any]`), nem
contornar `rest` completamente (a recomendação original), nem um
segundo caminho HTTP independente (rejeitado desde o início). O que
de fato foi construído, aprovado explicitamente pelo usuário ("Eu
concordo que esse desenho — um núcleo compartilhado via Adapter — é o
caminho certo") antes do início da Fase 0: uma quarta opção, um novo
ponto de integração de interface — `rest.Adapter` (`rest/adapter.go`) — que
`rest.Service` passou a usar em vez de `rest.Store` diretamente.

**Por que não generics.** `Adapter` não parametriza `Store` por
tipo — é uma interface comum que cada backend implementa à sua
própria maneira (`mongoAdapter` em `rest/mongoadapter.go`, o
`postgresAdapter` de `restpostgres`), cobrindo exatamente a
superfície que `rest.Service` precisa: parsear/combinar
`where`/`sort`/`projection` (`QueryAdapter`), geração/parsing de id
(`IDCodec`), classificação de erro (`ErrorClassifier`), e
CRUD/merge-patch/unicidade. As três primeiras foram extraídas de
dentro de `rest/service.go`/`query.go`/`http.go` ainda na Fase 0 (zero
mudança de comportamento do Mongo, confirmada por uma regressão
completa) como interfaces separadas e testáveis de forma
independente; a Fase 4a as uniu num só `Adapter`, junto com os
métodos de escrita. **`rest.Store` continua exatamente o que este ADR
decidiu** — ainda tipado em `bson.M`/`bson.D`, ainda só implementado
por `platform/mongodb.Store` — porque `Adapter` fica na frente dele,
não no lugar dele: uma `Resource` com `Adapter` definido nunca toca
`Store` (`Resource.setDefaults`, `rest/resource.go`, só constrói um
`mongoAdapter` envolvendo `Store` quando `Adapter` é `nil` — o
caminho padrão de um recurso Mongo que nunca precisou mudar nada).

**O que isso habilitou.** `restpostgres.NewAdapter` (um pacote novo,
o único importando `rest` e `platform/postgres` juntos, seção 2 do
plano de portabilidade) implementa `Adapter` por completo sobre uma
tabela `jsonb` mais colunas relacionais de metadado — ver
[`adapter.md`](adapter.md) para o contrato completo e
[`examples/blog-apis/services/comments-api`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/blog-apis/services/comments-api)
para o primeiro recurso real servido de ponta a ponta sobre HTTP por
esse caminho (Fase 7). O scaffold `platform/postgres` que este ADR já
citava continua existindo, ainda sem importar `rest`, exatamente como
decidido — `restpostgres` é a única ponte entre os dois, nunca o
contrário.

**Consequência para documentação.** A frase original ("o caminho de
contorno continua sendo a forma suportada de usar `postgres.Store`")
não é mais verdade — desatualizada por este próprio ADR, não ignorada
silenciosamente. [`store.md`](store.md) e
[`postgres-rfc.md`](postgres-rfc.md) foram atualizados para apontar para
[`adapter.md`](adapter.md) como o caminho atual; `postgres-rfc.md` em
si passa a ser um registro histórico do desenho original (a maioria
dos pontos "Decided" lá se confirmou; os marcados "open" foram
resolvidos durante a implementação em si, não revisitados aqui).

## ADR-0003 — Outbox e Postgres

**Status:** Implementado — adiamento original cumprido (2026-11-15)

**Implementado em:** não se aplica; a própria decisão é esperar por um
recurso real com Postgres (ver Decisão abaixo). O `outbox` (movido de
`internal/outbox` pelo ADR-0001/Fase 1) continua inalterado no PR #6,
conforme decidido.

**Contexto.** `internal/outbox` é fixo em `*mongo.Collection`/
`*mongo.Database`, escreve um recurso e seu evento na mesma transação
do Mongo, e documenta a entrega como at-least-once, nunca
exactly-once. [`postgres-rfc.md`](postgres-rfc.md), seção 9,
esboça uma alternativa nativa de Postgres (`BEGIN`/`COMMIT`,
`LISTEN`/`NOTIFY`, `FOR UPDATE SKIP LOCKED`) mas diz que depende do
ADR-0002 acontecer primeiro.

**Problema.** Se vale começar a construir portabilidade de outbox
agora, especulativamente, ou esperar por um consumidor real.

**Alternativas consideradas.**
- Construir uma interface de outbox genérica em relação ao store
  agora — rejeitada: não há um consumidor real para validar o design
  contra, exatamente a "abstração genérica sem consumidores reais"
  que o projeto proíbe.
- Esperar até o ADR-0002 ser revisitado e existir um recurso real com
  Postgres, então construir a contraparte de outbox do Postgres
  guiada pelas necessidades reais daquele recurso.

**Decisão.** A segunda opção. Outbox continua exclusivo do Mongo; nenhuma
mudança de código nesta rodada ou na Fase 1.

**Consequências.** A narrativa de "dois caminhos complementares, mesmo
núcleo" ainda não pode reivindicar portabilidade de outbox —
a documentação deve dizer isso claramente, não insinuar.

**Riscos.** Nenhum por continuar como está. O único risco é construir
a versão genérica antes do ADR-0002 desbloquear isso.

**Critério de validação.** Nenhuma documentação ou ADR afirma que
existe um outbox Postgres funcionando até que um recurso de fato use
um.

**Revisão — 2026-11-15.** O adiamento histórico acima foi cumprido nas Streams
C1–C5 (PRs #29–#31). `outboxpostgres` grava eventos na transação do mesmo pool
do recurso; `comments-api` o utiliza e verifica entrega Pub/Sub real. Hooks
de eventos aceitam Adder; publishers/conditions permanecem em `outbox`.
Mongo mantém implementação separada. Postgres usa SKIP LOCKED, sem ordem por
Key entre workers, e implementa dead_at/replay; Mongo preserva lease de líder
e retry sem limite. Não há interface genérica de armazenamento nem transação
distribuída. Veja [outbox](outbox.md) e [evidências consolidadas](integration-review.md).

## ADR-0004 — Escopo inicial do CLI / Plataforma

**Status:** Implementado

**Implementado em:** PR #6 (Fase 3, commit `fff7a61`)

**Contexto.** Não existe CLI multi-comando. `cmd/hyrule-serve` é um
único binário genérico de servidor. `ci/affected` já faz detecção real
de build afetado como ferramenta independente (aceita `-services` para
layouts fora de `examples/blog-apis`) mas não está ligado a nenhum
workflow de CI ou CLI. O `Makefile` já tem alvos `build`/`image`/
`run`/`env-up` parametrizados por `SERVICE`.

**Problema.** A visão de Plataforma do prompt (`hyrule new`,
`service create`, `dev`, `test`, `build --affected`, `deploy`) é
grande; construir tudo isso antes de qualquer parte ter um usuário
real arrisca "implementar comandos apenas para preencher uma lista."

**Alternativas consideradas.**
- Construir a superfície completa de comandos de uma vez.
- Começar só com geração da estrutura inicial de projeto/serviço (`hyrule new`,
  `hyrule service create`), reaproveitando o Makefile e o
  `ci/affected` para tudo relacionado a build/test/afetado em vez de
  reimplementá-los como subcomandos Go imediatamente.
- Envolver `ci/affected` e os alvos do Makefile como subcomandos
  primeiro, deixando o geração da estrutura inicial para depois.

**Decisão.** Começar só com geração da estrutura inicial. O primeiro valor real de um
CLI é eliminar a cópia manual de `examples/blog-apis/services/*` ao
começar um novo serviço — `ci/affected`, o Makefile e o modelo de
manifesto único já funcionam e não precisam de um adaptador de CLI para
serem úteis hoje. Envolver as ferramentas existentes como subcomandos
fica para depois, quando o CLI já existir como binário.

**Consequências.** A documentação da jornada Plataforma deve descrever
só `hyrule new` e `hyrule service create` no início; os subcomandos
`dev`/`test`/`build`/`deploy` ficam como trabalho futuro, e devem ser
marcados como tal onde quer que sejam mencionados.

**Riscos.** Só geração da estrutura inicial pode parecer pouco em relação à visão
completa do prompt — intencional; é o menor incremento que ainda é
valor real e utilizável.

**Critério de validação.** Uma pessoa consegue rodar `hyrule new
my-platform && hyrule service create articles` e obter um serviço que
compila e roda pelos alvos existentes do Makefile, sem copiar
`examples/blog-apis` manualmente.

## ADR-0005 — Versionamento e compatibilidade pré-1.0

**Status:** Implementado

**Implementado em:** PR #6 (Fase 0, commit `b19f31c`); toda fase desde
então adicionou sua própria entrada no `CHANGELOG.md`, na prática, não
só no papel.

**Contexto.** Sem `CHANGELOG`, uma tag de git (`v0.100.0`), sem
política de compatibilidade documentada. `go.mod` sem sufixo de versão
major, consistente com 0.x.

**Problema.** A Fase 1 está para introduzir as primeiras mudanças
quebradoras de verdade do repo (mover pacotes de `internal/` para
caminhos de import públicos) sem lugar nenhum para registrar o que
mudou e quando.

**Decisão.** Adotar o formato [Keep a Changelog](https://keepachangelog.com/)
em `CHANGELOG.md`, e tags SemVer 0.x a partir de agora (uma mudança
quebradora sobe o minor, já que o major continua `0`). Começar o
changelog agora, antes da Fase 1 executar, para que a primeira entrada
de verdade já tenha um lugar para ir.

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
[`store.md`](store.md) e o documento do pacote de `ci/affected/main.go`) mas
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
um consumidor real multi-provedor ainda para validar essa abstração —
o mesmo problema de "sem consumidor real" que o ADR-0003 nomeou para
portabilidade do outbox.

**Alternativas consideradas.**
- Construir uma camada de abstração de provedor (interfaces para GitHub
  Actions/GitLab CI, para Kubernetes/Cloud Run/Fly/etc.) — rejeitada
  pelo mesmo motivo: nenhum segundo provedor real para desenhar contra
  ainda.
- Documentar o padrão só em texto, sem um workflow de verdade rodando
  — rejeitada: "ligar a um workflow de CI real" (a própria frase do
  roadmap para Fase 4) implica algo que de fato roda, não só uma
  descrição de como poderia.
- Adicionar um workflow real do GitHub Actions, com escopo em
  `examples/blog-apis`, que chama o `ci/affected` para selecionar os
  serviços alterados, compila só esses, e — o ponto de integração configurável —
  chama um `deploy/<service>/deploy.sh` opcional se o serviço tiver
  um, deixado como um stub que imprime o que faria em vez de tocar num
  ambiente real. O `deploy.sh` é o "adapter" completo: o comando de
  deploy real de qualquer provedor vai lá, o Hyrule nunca vê isso.

**Decisão.** A terceira opção. Build independente é real (um `go
build` por serviço afetado no CI, não um rebuild do repo inteiro);
deploy independente é um ponto de extensão documentado
(`deploy/<service>/deploy.sh`), não uma capacidade entregue — não tem
o que configurar ainda porque não existe um ambiente de destino real
para esse exemplo público fazer deploy. Versionamento por serviço (o
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
suportada pelo Hyrule, em vez de um template para copiar — mitigado por
comentários no arquivo do workflow e por este ADR, o mesmo
enquadramento honesto que o [`postgres-rfc.md`](postgres-rfc.md) já
usa.

**Critério de validação.** Um push ou PR que só toca os arquivos de um
serviço faz o workflow reportar e compilar só esse serviço, em menos
tempo que um rebuild completo; um push tocando um caminho compartilhado
(go.mod, `platform/*`, `Dockerfile`) faz ele reportar e compilar todos.
O stub `deploy.sh` de cada serviço roda sem erro e não faz nenhuma
mudança externa real.

## Evolução proposta — 2026-11-16

Os ADRs 0007–0012 mantêm **Proposto** como status formal de revisão. Implementação
local e aceitação formal são fatos separados: os pilotos do PR #70 implementam
partes dessas decisões sem concluir seu escopo de produção. Os nomes sugeridos
continuam ilustrativos; o código atual fica no módulo opcional
`examples/standalone-tags-api`.

| ADR | Evidência atual de implementação | Ainda aberto |
|---|---|---|
| 0007 | [Estudo de 21 casos](phase-6-contract-study.md), correções PATCH aninhadas e annotations de validação do servidor | Aceitação formal e evidência para gerador |
| 0008 | Registro explícito e limitado de `tags.lookup`, `knowledge.search` e `knowledge.fetch` | Registry genérico reutilizável e API pública |
| 0009 | [Descoberta autorizada de tags](read-pilot.md), schemas, limites e hash determinístico | Exportador/catálogo genérico e cache isolado por autorização |
| 0010 | JWT/política, limites de leitura, auditoria obrigatória de leituras, ACL/versão da fonte e CRUD de curadoria com ETags | Mutações MCP controladas, aprovação/idempotência, auditoria de domínio em produção e garantias de preview |
| 0011 | [Piloto SDK/stdio](mcp-pilot.md), protocolo escolhido e clientes SDK reais | Cliente de produção/desktop, HTTP MCP/OAuth e sessões remotas |
| 0012 | [Retrieval pgvector](knowledge-pilot.md), [avaliação com modelo real](knowledge-evaluation.md), [fonte/outbox](knowledge-sync.md) e [produtor de artigos](knowledge-producer.md) | Implantação, critérios ampliados de qualidade/carga, operação e futura integração Qdrant |

O [roadmap](roadmap.md) e a [análise de DX](ai-readiness.md) distinguem os achados
históricos da auditoria dos incrementos posteriores de implementação.

## ADR-0007 — Contrato canônico e geração condicionada à evidência

**Status:** Proposto para aceitação formal; estudo e correções de contrato
implementados. Geração segue adiada até evidência recorrente de consumidores.

**Contexto.** `declarative.Resource` compila dados em `rest.Resource`; `rest.Schema` já
alimenta validação e OpenAPI. `Field.Check`, `Resource.Validate`, hooks e filtros
programáticos não são serializáveis como schema simples.

**Problema.** Um segundo schema para IA ou um gerador bidirecional YAML↔Go criaria
divergência. Ao mesmo tempo, a API dinâmica pode obrigar consumidores a repetir DTOs e
validações.

**Alternativas consideradas.** Adotar Ent/GORM como novo núcleo; inferir tudo de tags
por reflexão; manter representações independentes; gerar todos os modelos desde já;
reutilizar o contrato atual e medir a duplicação antes de gerar.

**Decisão proposta.** Escolher a última alternativa. `Resource`/`Schema` continuam sendo
a representação canônica de runtime. Cada recurso tem uma origem autoral escolhida —
YAML ou Go — sem sincronização bidirecional. Metadados opcionais de IA ficam em
descritores externos associados ao recurso, sem duplicar regras de validação. O
exportador registra regras não representáveis como restrições adicionais do servidor,
sem fingir equivalência total com JSON Schema.

Primeiro fazer um estudo com um recurso declarativo e um programático, cobrindo lista,
objeto, nullable, campo obrigatório, default, unicidade, PATCH e uma validação
customizada. Medir campos repetidos, linhas de manutenção e divergências detectadas. Um
gerador futuro só se justifica se remover duplicação recorrente de consumidores
concretos. Nesse caso, deverá ter fluxo unidirecional, saída determinística, versão
fixada, arquivos gerados separados de hooks manuais e modo de checagem sem reescrita. A
escolha YAML→DTO ou outra origem depende desse estudo; não está resolvida aqui.

**Consequências.** Descoberta pode avançar sem um gerador. Schema de entrada, PATCH e
saída permanecem distintos; tipos de ID e formatos de data seguem codecs/contrato do
recurso. Migração de dados e DDL não se tornam responsabilidade do schema REST.

**Riscos.** Expandir o escopo para um compilador geral; omitir semânticas Cerberus como
`AllowEmpty`; tratar callbacks como validação exportável; supor que adicionar campo
requerido é mudança compatível para todos os clientes.

**Critérios de validação.** Comparar contrato exportado com validação real para os casos
acima; rejeitar construções não suportadas com diagnóstico; não exigir edição de dois
schemas para o mesmo campo; manter exemplos atuais funcionando. Geração permanece adiada
se o estudo não demonstrar ganho. Alterações de schema devem explicitar migração e
compatibilidade, inclusive campos requeridos e mudanças de tipo.

**Evidência do estudo — atualizada em 2026-11-17.** O [estudo da Fase 6](phase-6-contract-study.md)
comparou fixtures YAML/Go equivalentes e 21 casos de runtime/schema. As seis
diferenças originais são históricas: duas divergências PATCH aninhadas foram
corrigidas; restam quatro diferenças documentadas dependentes de estado/servidor.
Schemas de escrita também sinalizam validação customizada. Não há gerador nem
aceitação formal concluída; o estudo sustenta manter a geração adiada.

## ADR-0008 — Catálogo opcional de ferramentas sobre operações existentes

**Status:** Proposto para registry genérico; ferramentas locais limitadas implementadas.

**Contexto.** `rest.API.Register` retorna um `Service`; `Service.Resource()` permite
consultar a definição. Serviços já implementam operações e selecionam o backend via
`Adapter`.

**Problema.** Expor diretamente cada método Go duplicaria o CRUD, vazaria parâmetros
internos e poderia permitir operações desabilitadas no contrato público.

**Alternativas consideradas.** Gerar ferramentas automaticamente para todos os recursos;
executar requests HTTP sintéticos; criar novo CRUD para IA; registrar explicitamente
operações limitadas que invocam o serviço existente.

**Decisão proposta.** Registro explícito e fechado por padrão. Para cada ferramenta
declarar nome estável, versão do contrato, descrição, operação permitida, campos de
entrada/saída, política exigida, limites e classificação de efeitos. Derivar a estrutura
dos argumentos do recurso e acrescentar apenas a semântica específica da operação.
Inicialmente registrar apenas consulta limitada de um recurso escolhido; operações de
domínio devem apontar para funções de aplicação existentes, não montar workflows por
reflexão.

O catálogo recebe os serviços registrados pela aplicação, sem acessar campos privados de
`rest.API` nem percorrer banco para inferir schema. Construção é finalizada antes de
servir. Validar colisões de nomes, operações incompatíveis com métodos configurados e
capacidades não suportadas pelo backend. Flags `SkipValidation` e `SkipConcurrency`,
credenciais, expressões SQL e acesso bruto a adapters nunca são argumentos de
ferramenta.

**Consequências.** `Service` continua responsável por regras de persistência. O wrapper
precisa reproduzir a preparação relevante do handler: parsing por `Adapter`, filtros
obrigatórios, paginação limitada, contexto, rendering e precondições. Escolher `Service`
reduz chamadas de rede, mas exige provar paridade; se essa prova falhar, reconsiderar um
cliente do contrato HTTP autenticado em vez de manter dois comportamentos.

**Riscos.** Confundir recurso público com ferramenta automaticamente autorizada;
serializar `Doc` com campos internos; descrever consultas Mongo como universalmente
válidas em SQLite; alterar descritores durante requisições concorrentes.

**Critérios de validação.** Ferramenta desconhecida/oculta não executa; método
desabilitado não pode ser reativado pelo catálogo; consulta válida retorna envelope e
campos documentados; entrada inválida falha antes do acesso ao adapter; mesmas
validações, hooks e precondições da operação REST são observadas. Rodar prova por
backend habilitado, sem exigir paridade que SQLite não oferece. Escritas dependem também
do ADR-0010.

**Evidência de implementação — 2026-11-17.** Os pilotos explícitos implementam `tags.lookup` pelo Service existente e
`knowledge.search`/`knowledge.fetch` por Store com escopo. O registro fica nos
adapters MCP locais; não há registry genérico público, exportação automática de
recursos ou pacote de framework `aitools`. O [produtor de artigos](knowledge-producer.md)
reutiliza CRUD Service e hooks transacionais sem criar ferramentas MCP de escrita.

## ADR-0009 — Context Discovery derivada, limitada e autorizada

**Status:** Proposto para descoberta genérica; descoberta local autorizada implementada.

**Contexto.** `rest/openapi.go` e `rest/swagger.go` já descrevem recursos e operações a
partir das definições registradas. As funções de geração são internas ao pacote; não
existe um exportador público genérico pronto para MCP.

**Problema.** Clientes precisam descobrir operações, formatos e restrições sem receber
dados de negócio, segredos ou descrições de ferramentas que não podem usar.

**Alternativas consideradas.** Introspecção do banco; cópia independente do schema para
prompts; ingestão irrestrita do OpenAPI; projeção de contrato baseada em recursos
registrados e política de exposição.

**Decisão proposta.** Projetar uma visão autorizada do mesmo contrato: recursos
disponíveis, operações permitidas, argumentos, limites, semântica do backend e exemplos
sintéticos. Não incluir documentos reais por padrão. `DisableSwagger` e visibilidade de
IA são decisões independentes; ocultar Swagger não protege a rota nem autoriza
publicação no catálogo.

Na implementação, comparar uma projeção externa de `Resource`/`Schema` com uma pequena
extração aditiva do serializador OpenAPI existente. Se for necessária uma API exportada
em `rest`, ela deve ser neutra a IA, revisada como nova superfície pública e sem mudar o
documento OpenAPI atual. Não há promessa de simplesmente chamar uma função já exportada.

Cachear metadados estáticos por versão/hash do contrato. Uma visão filtrada por
usuário/tenant precisa chave de cache que inclua o escopo de autorização; nunca
reutilizar resposta de principal privilegiado para outro. Listagem e execução verificam
permissões separadamente.

**Consequências.** Validação customizada permanece regra do servidor. Campos sensíveis
exigem allowlist de exposição; não se infere confidencialidade a partir do nome.
Descrições são conteúdo revisado, sem interpolação livre de registros do banco. Dados
recuperados são conteúdo não confiável e não alteram políticas do executor.

**Riscos.** Vazamento por exemplos, descrições, caches ou erros; divergência entre
exportador e validação; confundir igualdade de hash com autorização atual.

**Critérios de validação.** Dois perfis com escopos diferentes recebem visões corretas e
isoladas; descrições não contêm segredos; saída é determinística; mudança incompatível
altera versão/hash; amostras de entrada aceitas/rejeitadas correspondem ao servidor;
estruturas não representáveis são declaradas como tais. OpenAPI atual mantém seu
contrato.

**Evidência de implementação — 2026-11-17.** A descoberta de tags deriva schemas escalares de Resource/Schema e compartilha
restrições de operação com a execução. Publica limites, regras do servidor e hash
SHA-256 estável após autorização/auditoria confirmada, sem ler registros. As
ferramentas de conhecimento também possuem listagem MCP autorizada e schemas de
entrada específicos; não são um exportador genérico de Resource. Não foi extraída
API pública neutra de serialização nem cache de catálogo compartilhado por autorização.

## ADR-0010 — Safe Execution com identidade, autorização e efeitos explícitos

**Status:** Proposto para o modelo completo; proteção de leituras locais e escritas
de curadoria implementada. Mutações MCP controladas continuam abertas.

**Contexto.** A autenticação HTTP não está embutida em cada método de `Service`.
`WriteOptions` contém opções internas que podem desabilitar checks. O dry-run existente
foi projetado como modo de deployment e depende da composição correta de transações.

**Problema.** Reutilizar `Service` sem esse perímetro poderia permitir leitura entre
tenants, alterações sem precondição ou efeitos externos durante uma suposta simulação.

**Alternativas consideradas.** Confiar no prompt/modelo; confiar apenas em annotations
MCP; usar rollback como sandbox universal; executar toda operação em subprocesso
genérico; wrapper autorizado por operação com capacidades restritas.

**Decisão proposta.** Usar o wrapper restrito. Pipeline obrigatório: autenticar no
transporte → construir principal confiável → localizar ferramenta → autorizar ação,
recurso, tenant e campos → validar e limitar argumentos → executar a operação existente
→ filtrar/renderizar resposta → registrar resultado. Principal vem de credenciais verificadas; contexto de recurso/corpus/tenant vem
de configuração confiável da aplicação ou claims verificados, nunca de argumentos
escolhidos pelo modelo. A aplicação injeta
política explícita; ausência de política nega exposição.

Filtros de tenant são impostos pelo servidor em todas as leituras e escritas; sua
combinação deve impedir substituição pelo filtro do cliente. Projeção e pós-filtragem de
campos não substituem autorização por registro. A política precisa abranger `Get`,
`List`, `Insert`, `Patch`, `Replace` e `Delete`, incluindo registro-alvo após lookup e
campos alteráveis. Proibir consultas arbitrárias, bypass de validação e bypass de
concorrência.

Toda chamada recebe deadline, limite de payload, máximo de resultados e orçamento de
execução. Listagens obedecem limites mesmo em recursos REST com paginação desabilitada.
Escritas exigem ETag concreto quando aplicável; não usar `*` como atalho para aprovação
de ação sensível. Registrar ator, ferramenta/versão, recurso, identificador de
correlação, decisão de autorização e resultado, com argumentos sensíveis redigidos.

Operações destrutivas ou com efeitos externos exigem aprovação conforme política da
aplicação. A evidência de aprovação fica vinculada ao principal, ferramenta/versão,
argumentos canônicos, recurso/ETag, prazo e identificador de uso único; mudanças
invalidam a aprovação. Confirmar novamente autorização e estado na execução evita
TOCTOU. Isso é desenho do produto futuro, não uma nova exigência de aprovação para esta
rodada documental.

Para repetição de mutações, a aplicação precisa fornecer idempotência durável: chave
vinculada ao principal/operação/argumentos, conflito para conteúdo diferente, registro
atômico com a mutação quando possível e política de retenção. Não repetir cegamente uma
operação após timeout com resultado desconhecido. ETags sozinhos não tornam inserts ou
efeitos externos idempotentes.

**Dry-run.** O primeiro incremento deve ser leitura e, se necessário, preview de
validação sem executar hooks com efeitos. Reutilizar a composição de deployment
existente para ensaios de rollback em Mongo/Postgres, após inventário dos hooks; não
mudar uma variável global para simular só uma chamada. Em Mongo, usar o `WriteGuard`
como detector adicional, reconhecendo que ele não bloqueia violação. Em Postgres,
validar a composição transacional específica. Em SQLite, retornar capacidade não
suportada. Endpoints ou hooks com efeitos não reversíveis são recusados nesse modo;
`RefuseInDryRun` é precedente, não cobertura automática de todo código Go.

**Consequências.** Não há sandbox para código arbitrário nem SQL gerado por IA. O
executor oferece somente operações selecionadas. Auditoria de tentativas negadas e
falhas precisa ocorrer fora da transação de domínio abortada; eventos de alteração
bem-sucedida continuam na transação/outbox já existente.

**Riscos.** Identidade sem autorização de domínio; vazamento por logs; repetição após
timeout; escrita fora da transação; efeitos externos em pre-hooks; aprovação aplicada a
argumentos diferentes.

**Critérios de validação.** Negar chamada sem identidade/política, acesso entre tenants
e campos proibidos; provar limites e cancelamento; rejeitar ETag ausente/obsoleto e
aprovação expirada/alterada; testar chamadas concorrentes com mesma chave de
idempotência; demonstrar ausência de alterações persistidas e efeitos externos nos modos
anunciados como preview/dry-run. Falha desconhecida não pode ser reportada como rollback
confirmado. Falha de auditoria obrigatória precisa de comportamento explícito definido
pela aplicação.

**Evidência de implementação — 2026-11-17.** Ferramentas locais autenticam/autorizam
antes do processamento da consulta e embeddings, limitam tempo/entrada/saída e
retêm a saída quando a auditoria SQLite obrigatória falha. Retrieval verifica ACL,
tombstone, versão e contrato indexado atuais da fonte. A CLI separada de curadoria
exige `knowledge:write`, ETags específicos e validação normal; recurso/fonte/outbox
revertem juntos. Auditoria do domínio continua como política da aplicação. Não há
mutação MCP, ledger durável de aprovação/idempotência, sandbox por chamada ou nova
garantia de dry-run comprovada. Authelia/OIDC e delegação remota não foram integrados.

## ADR-0011 — MCP como adaptador opcional do catálogo

**Status:** Direção proposta; piloto local SDK/stdio implementado (ver nota datada).
Adoção em produção e autorização remota permanecem abertas.

**Contexto.** MCP padroniza descoberta e chamada de ferramentas e recursos. O contrato
evolui; as versões e fontes consultadas estão indicadas abaixo. A base inicialmente
auditada não continha servidor MCP; o módulo standalone agora contém adapters SDK locais.

**Problema.** Acoplar protocolo e SDK ao núcleo obrigaria todos os consumidores do
framework a acompanhar a evolução de IA e poderia introduzir uma segunda política de
execução.

**Alternativas consideradas.** Embutir MCP em `rest`; substituir HTTP por MCP;
implementar protocolo inteiro artesanalmente; adaptador isolado usando SDK compatível
com a versão escolhida.

**Decisão proposta.** Adaptador isolado, por exemplo `mcpbridge`, importando o
catálogo/executor, sem importação inversa. Um composition root opt-in o inicializa.
`tools/list` descreve ferramentas permitidas; `tools/call` sempre passa pelo executor do
ADR-0010. Resources MCP, se necessários, expõem somente conteúdo autorizado da
descoberta, sem exportar tabelas ou documentos indiscriminadamente. Prompts e
notificações ficam fora do MVP até demanda.

Selecionar um cliente real e um transporte inicial em uma prova: stdio para integração
local ou HTTP para serviço remoto autenticado. Antes de implementar, fixar versão de
protocolo/SDK, compatibilidade do cliente e mecanismos exigidos pelo transporte. Para
HTTP remoto, tratar autenticação/autorização de transporte, validação de tokens e
isolamento de sessão como requisitos próprios; autenticação JWT REST existente não prova
conformidade MCP. Stdio não concede automaticamente identidade de usuário final. Não
encaminhar tokens para outros serviços sem validação de audiência e desenho explícito de
delegação.

Annotations são descrições auxiliares, não controles de acesso. Conversão de erros
preserva distinção entre erro de protocolo e falha de execução de ferramenta segundo a
versão selecionada, sem stack traces, SQL, DSNs ou documentos internos.

**Consequências.** O mesmo catálogo pode atender outros clientes sem MCP. A implantação
opt-in recebe limites de conexões, cancelamento e política de credenciais. Clientes REST
continuam funcionando sem configuração de IA.

**Riscos.** Negociação incompatível, sessão compartilhada entre usuários, confiança
indevida em annotations, exposição de catálogo sem filtro e duplicação de políticas.

**Critérios de validação.** Um cliente MCP real descobre e chama a consulta inicial;
invocação direta de ferramenta oculta é negada; dois usuários têm isolamento;
desconexão/deadline cancelam trabalho; entradas malformadas e versões não suportadas
falham previsivelmente; o mesmo executor é usado para toda chamada. Acrescentar mutações
somente após os critérios do ADR-0010.

Fontes primárias consultadas: [MCP tools — especificação
2026-07-28](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/server/tools.mdx),
[MCP authorization — especificação
2026-07-28](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/basic/authorization/index.mdx)
e [MCP tools — versão 2025-06-18, incluindo confiança em
annotations](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2025-06-18/server/tools.mdx).
A [matriz do piloto](mcp-pilot.md) registra SDK/protocolo e clientes SDK reais
validados. Outros clientes/transportes exigem validação própria.

**Nota de implementação — 2026-11-16.** O [piloto MCP local](mcp-pilot.md)
selecionou SDK oficial v1.8.0 e protocolo 2025-11-25, com adapter interno separado,
cliente SDK real em memória/stdio e autorização/auditoria em cada operação.
Adoção por cliente de produção, HTTP MCP/OAuth e sessões remotas multiusuário continuam
abertos; esta evidência não transforma o escopo completo do ADR em implementado.

## ADR-0012 — Retrieval opcional, dirigido por consumidor e sincronizado por outbox

**Status:** Proposto para aceitação formal/generalização; retrieval local, avaliação
com modelo real, fonte Postgres versionada/outbox e produtor concreto implementados.
Implantação e aceitação ampliada de qualidade permanecem abertas.

**Contexto.** A auditoria inicial encontrou indexação Elasticsearch e outboxes
Mongo/Postgres, sem consumidor vetorial. O consumidor escolhido agora inclui
documentação do repositório e artigos de curadoria no módulo standalone opcional.
Isso substitui o adiamento inicial da implementação para esse consumidor.

**Problema.** Uma abstração universal de embeddings/retrieval antes de conhecer corpus e
consultas esconderia decisões sobre autorização, consistência, custo e qualidade.
Enriquecer uma escrita síncrona com chamadas a um provedor externo aumentaria latência e
quebraria a atomicidade que o outbox preserva.

**Alternativas consideradas.** Embeddings síncronos em hooks; converter `searchsync` em
mecanismo durável genérico; criar vector store universal; escolher pgvector ou
Elasticsearch por conveniência; adiar implementação, registrando uma composição mínima
para um consumidor concreto.

**Decisão do piloto limitado.** Usar pgvector com o SDK MCP existente e interface
de armazenamento, ACL explícita por corpus/sujeito e verificação de versão da fonte.
Embeddings ficam fora das transações de fonte/índice e a geração de respostas no
modelo do cliente. A [avaliação local](knowledge-evaluation.md) usou EmbeddingGemma
300M q8 já disponível, 768 dimensões, oito documentos/113 trechos e dez perguntas
bilíngues: ambas as buscas tiveram Hit@5 10/10; MRR@5 foi 0,883 versus 0,753.
São resultados exploratórios para este incremento local, não superioridade geral
ou aceitação de produção. Qdrant é backend/integração futura, sem instalação atual.

No primeiro consumidor, worker e retrieval ficam opcionais e acima dos adapters de
infraestrutura. Comparar lexical, vetorial e híbrido usando o mesmo corpus. Escolher uma
implementação específica do consumidor antes de extrair contratos públicos de
framework. Um segundo backend real fornece evidência para generalização; a
interface Store atual é interna ao consumidor standalone.

**Fluxo de referência e implementação local.** A escrita do documento e um evento mínimo
de reindexação entram na mesma transação local. O produtor atual chama QueueSource
em seus hooks de pré-escrita e usa `outboxpostgres`; outros recursos podem usar
`behaviors/events` com seu outbox existente. Um worker consome ao menos uma vez, relê a versão autorizada,
seleciona campos permitidos, particiona em chunks e calcula embeddings fora da
transação. IDs de chunk e versão do documento/modelo tornam o processamento idempotente.
A chave inclui tenant e recurso. Duplicação, atualização concorrente e entrega fora de
ordem são tratadas por comparação de versão/tombstone; não presumir ordenação por chave
no outbox Postgres.

Não usar somente `ConditionalHooks` para remoções sem verificar o caminho de delete: a
implementação observada cobre insert/update/replace, enquanto `events.Hooks` também
cobre delete. Remoções e alterações de ACL exigem eventos explícitos adequados.
Documento apagado não deve ser ressuscitado por evento antigo; um backfill precisa
compartilhar as mesmas regras de versão e conciliar com alterações ao vivo.

Manter versionados modelo de embedding, dimensão, chunker, campos indexados e contrato
do índice. O piloto reconstrói explicitamente por documento e bloqueia versões
incompatíveis. Troca controlada de um índice novo para o corpus inteiro permanece
como desenho de produção. Lidar com
indisponibilidade do provedor por retry limitado, backoff e quarentena/DLQ conforme
suporte real do backend; Mongo não deve ser descrito como tendo a mesma DLQ já oferecida
pelo Postgres. Nenhuma falha do índice reverte a mutação já confirmada no banco fonte.

**Retrieval e RAG.** Autorizar consulta antes da busca; aplicar restrições de tenant/ACL
no retrieval e revalidar os documentos no banco fonte antes de devolver contexto. O
índice não é autoridade de permissões. Limitar top-k, tamanho total e orçamento de
tokens. Propagar exclusões e revogações de acesso; caches usam escopo de autorização e
versão. Resultados levam referência ao documento, versão e trecho para permitir citação.
Conteúdo recuperado é dado não confiável: não altera política, aprovação ou lista de
ferramentas. Uma etapa de geração, se necessária, pertence à aplicação, com tratamento
explícito de resposta sem evidência e abstinência.

**Consequências.** Eventual consistency fica documentada e mensurável. Não há garantia
de exactly-once. SQLite não participa do pipeline transacional proposto enquanto não
tiver outbox suportado. A aplicação não precisa adotar RAG para usar catálogo ou MCP.

**Riscos.** Vazamento entre tenants, PII enviada ao provedor, custo de reindexação,
eventos fora de ordem, documentos removidos ainda recuperáveis, resultados plausíveis
sem evidência e regressão silenciosa de qualidade.

**Critérios de validação.** Antes da implementação, definir limiares de Recall@k/nDCG,
qualidade de citação e abstinência, latência p95, custo por consulta/indexação e atraso
de índice. Medir em conjunto de consultas representativas com julgamentos documentados e
comparar com baseline lexical. Critérios obrigatórios de segurança: nenhum retorno não
autorizado na suíte de isolamento; exclusão/revogação bloqueia o retorno mesmo com
índice atrasado. Testar duplicação, reordenação, replay, crash após escrita de índice e
retomada de backfill. A medição local não predefiniu um gate de produção; ampliar
a adoção depende de ganho mensurável e orçamento operacional viável.

**Evidência de implementação — 2026-11-17.** O [caminho fonte/outbox](knowledge-sync.md)
implementa escrita atômica de fonte/evento, verificação de ACL/versão/estado indexado,
tombstones, retries/DLQ/replay e comparação de versão na indexação. O
[produtor de artigos](knowledge-producer.md) mapeia título/corpo/leitores por hooks
Service na mesma transação, exclui notas privadas e isola corpora. Testes reais de
Postgres/MCP cobrem esses caminhos. Reindexação é explícita por documento/contrato;
não há troca de shadow index do corpus inteiro, watcher de arquivos, mapeamento
automático de outras aplicações, retrieval híbrido ou implantação em produção.
A futura integração Qdrant deve preservar a autorização na fonte.

## ADR-0013 — Escopo transacional e boot PostgreSQL em dry run

**Status:** Implementado para o boot gerenciado pelo framework e SQL personalizado opt-in.

O framework instala as funções SQL compartilhadas no boot normal. O dry run usa
um schema provisionado e verifica os índices declarados. O serviço de comentários
também pula o DDL do outbox em dry run.

Escritas PostgreSQL personalizadas podem exigir a transação do contexto via
`Transactor.TransactionQuerier`, que rejeita transações ausentes ou de outro pool.
O pool permanece disponível para compatibilidade e administração. Setup do
aplicativo, uso direto do pool, efeitos externos e alocação de sequências ficam
fora do rollback gerenciado. O WriteGuard do Mongo é diagnóstico. SQLite rejeita
dry run no boot. Veja [Dry run](dry-run.md) para migração e uso.
