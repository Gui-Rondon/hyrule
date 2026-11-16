---
sidebar_position: 0.5
---

# Revisão da integração: Mongo e Postgres

Data: **2026-11-15**. Base da integração: `a4fef45` (merge #44); main:
`4aa6195`; ancestral comum: `bf3383b`. Este registro consolida comparação,
compatibilidade e evidências reproduzíveis. Não autoriza merge em main nem
certifica deploy/desempenho de produção. O PR continua contra
`integration/postgres-portability`.

## Comparação com main

Revisados bootstrap/ciclo de dependências, seam Store/Adapter, IDs/ETags/CAS,
classificação de erros, números/schema, transações/outbox, opt-ins de índices/
consultas, migrations/gates HA manuais, APIs públicas e instruções EN/PT.
Comparamos histórico de três pontos e tips diretamente: main tem o commit
independente de e2e `048bc22`; olhar só commits acrescentados à integração não
cobriria isso.

`git merge-tree --write-tree --name-only 4aa6195 a4fef45` encontra **dois
conflitos add/add**, sem mudar branch ou checkout:

- `examples/blog-apis/e2e/run.sh`
- `examples/blog-apis/e2e/jmeter/hyrule-e2e.jmx`

As versões da integração preservam os caminhos Mongo e acrescentam comments
Postgres, CAS, provisionamento/cleanup de subscription Pub/Sub isolada,
migrations explícitas e validador check-results. Num futuro merge autorizado,
compare novamente ambos os tips e preserve essas adições junto dos cenários
Mongo originais de main. Não resolva descartando um backend. Após resolução,
rode suíte completa e JMeter/PubSub de 42 amostras na **árvore resolvida**;
checkout da integração aprovado não comprova um resultado futuro de merge.
Makefile combinou automaticamente nesta prévia; isso não garante ausência de
conflitos após mudanças em qualquer branch.

## Achados tratados nesta consolidação

| Achado | Resolução e validação |
|---|---|
| README/Learn/referências ainda chamavam outbox ou dry run de Mongo-only; ADR-0003/roadmap diziam que não existia outbox Postgres | Guia paralelo Mongo/Postgres e atualização EN/PT de outbox, dry run, testes, arquitetura e ADR. Decisões históricas continuam identificadas como histórico. |
| Texto de agendamento Redis implicava transação compartilhada Mongo/Redis | Explicitar commit local do outbox e entrega assíncrona por relay; Redis/PubSub têm efeitos externos retentáveis, sem transação distribuída. |
| Spec.Backend desconhecido selecionava Mongo silenciosamente e podia causar panic com dependências erradas | Run/Build rejeitam antes de configuração/auth/conexão. Teste falhou antes da correção; Mongo padrão e Postgres explícito são exercitados por CRUD HTTP real. |
| Configurações de duration do pool tinham cobertura incompleta de adoção | Asserts no pool real cobrem lifetime, idle, health period e connect timeout; tabela de rejeição cobre todas as opções expostas antes de conectar. |
| Comentário Spec.DefaultDB sugeria fallback em URI Postgres sem banco | Documentada resolução real do pgx e banco explícito nas URIs de exemplo; comportamento de conexão existente preservado. |
| Refactor do bootstrap mudou a ordem de defer de main: Mongo podia desconectar antes do cleanup dos workers | Cancelar/aguardar workers Mongo antes de Disconnect, como em Postgres. Teste real com falha de Setup nos dois backends reproduz o erro Mongo anterior e verifica banco disponível durante cleanup. |
| Módulo externo standalone não tinha checksums pgx exigidos pelo novo grafo de dependências do bootstrap | Atualizar go.mod/go.sum com go mod tidy e repetir o teste Mongo real; versões de dependências da raiz inalteradas. |

## Compatibilidade e migração

- `Resource.Store` permanece tipado em BSON; quando Adapter é nil, o wrapper
  Mongo interno é criado. Mongo continua `app.BackendMongo`, valor zero (`""`).
  Use essa constante: `Backend("mongo")`, erros de digitação e valores
  desconhecidos agora são rejeitados.
- `rest.Adapter` é a ponte JSONB via `restpostgres`; Store Postgres não implementa
  rest.Store. Backend do serviço e adapter devem concordar sobre banco/transação.
  Trocar Spec.Backend sozinho não migra documentos, IDs, índices, hooks nem
  opções de datasource.
- **Mudanças de fonte contra main:** `WriteOptions.IfMatch` agora é `rest.IfMatch`;
  argumentos de lookup/query do Service são `any` em vez de BSON. Argumentos
  BSON continuam passando, mas valores de função tipados, interfaces/mocks e
  literais string IfMatch precisam de recompilação/ajuste. `events.Hooks`/
  ConditionalHooks e funções de health do outbox recebem interfaces estreitas;
  chamadas normais com ponteiros continuam válidas, mas atribuições a tipos de
  função exatos podem mudar. Structs exportadas ganharam campos; prefira
  literais com chaves. Siga a política pre-1.0 do CHANGELOG/ADR-0005; não é
  promessa de atualização de código sem ajustes.

```go
match, ok := rest.ParseIfMatch([]string{`"` + etag + `"`})
if !ok { return rest.ErrPreconditionRequired }
opts := rest.WriteOptions{IfMatch: match}
```

- IDs/ETags são opacos: Mongo usa ObjectID/hash de conteúdo; Postgres usa
  UUID/version. Parsing HTTP de If-Match strong/lista/wildcard é explícito;
  weak nunca strong-match. Mongo mantém conflito wildcard de tentativa única
  e labels de retry do driver. Postgres retenta CAS só no PATCH wildcard,
  dentro dos orçamentos configurados de tempo/tentativas.
- PATCH Postgres segue RFC 7396 (`null` remove chave); Mongo mantém deep merge/
  null do Eve. Postgres verifica campos obrigatórios após merge;
  RequireMergePatchContentType segue opt-in explícito. Inteiros preservam
  precisão int64 e rejeitam overflow; isso não iguala toda representação
  numérica/armazenamento nos bancos.
- Datasource Filter/DefaultSort/Projection, lookup adicional BSON e YAML/
  scaffolding permanecem específicos do Mongo. Tipos do schema Postgres
  orientam o compilador; nested sort continua textual. Auth valida JWTs,
  sem sessão de usuário ou estado de refresh token.

## Opt-ins e limites aceitos

Veja a [tabela de adoção](backends.md#adote-os-opt-ins-postgres-explicitamente)
e os [detalhes do adapter](adapter.md). Nenhum recurso de produção é ativado
para índice de sort tipado, política custom ou datetime seguro nesta revisão.
Defaults, falha datetime estrita, DDL legado e contratos Mongo são preservados
para configurações válidas.

Limites: parâmetros de campo no generic podem impedir índice de expressão/
parcial; offsets profundos podem exigir Sort. Presença sparse é explícita,
pois a guarda implícita regrediu UUID. Índice tipado pode rejeitar casts
existentes/futuros, exige nome novo e recusa Unique/Sparse/datetime typed.
Índices com mesmo nome não têm definição comparada. Datetime seguro é opt-in
por adapter e mantém normalização/NULL do helper. Custom custa planejamento
no pool inteiro. Nenhum desses mecanismos garante latência de produção.

Ambos os outboxes entregam pelo menos uma vez. Mongo usa lease de líder e
retry sem limite; Postgres tem SKIP LOCKED, dead_at/replay e não garante ordem
por Key entre workers. Retenção e monitoramento de mortos Postgres exigem
política operacional. Dry run reverte escritas de negócio transacionais,
não chamadas diretas ao pool, DDL, efeitos externos ou alocação de sequences.
Migrations são explícitas: startup verifica compatibilidade do schema comments,
mas não o avança automaticamente.

Permanece uma limitação de validação: o teste existente de conflito real usa
orçamento de 60ms e ocasionalmente recebe 503 sem RetryExhaustedReason com
log de query timeout; a causa exata não é corrigida nesta revisão. Falhou numa
execução completa desta revisão; a ocorrência é registrada, sem atribuí-la
à correção de shutdown. Distinguir aprovação isolada/reexecução de uma correção
da classificação de retry. Esta consolidação não altera esse contrato.

## Evidências reproduzíveis dos gates

Todos os bancos são reais. Gates de planos usam 20 mil linhas e scans ligados.
Contagens descrevem fixtures verificadas, não todo índice/query nem benchmark
de capacidade. Observações auto/generic não devem ser generalizadas.

| Gate | Evidência esperada | Artefato local |
|---|---|---|
| postgres-ha-test | Topologia, replay em réplicas e WAL archive | Saída Go; laboratório manual/local |
| postgres-ha-migrations-test | Expand/backfill/contract, dirty/version e HTTP real | Saída Go; schemas isolados |
| postgres-ha-explain-test | Inventário A6 observacional com 30 planos e controles de catálogo | .local/postgres-explain/ |
| postgres-ha-where-test | 30 planos; Index Cond auto/custom; helper datetime generic uma vez por linha | .local/postgres-explain/ |
| postgres-ha-id-sort-test | 18 planos, 12 checks PK sem Sort; offset profundo observacional | .local/postgres-id-sort/ |
| postgres-ha-sparse-test | 30 planos, 2 checks de presença explícita Index Cond | .local/postgres-explain/ |
| postgres-ha-typed-sort-test | 48 planos, 24 checks auto/custom sem Sort, paridade completa | .local/postgres-typed-sort/ |
| postgres-ha-plan-policy-test | 12 planos do statement realmente em cache; URI em duas conexões/transações | .local/postgres-plan-policy/ |
| postgres-ha-datetime-sort-test | 48 planos, 24 checks; padrão estrito e modo seguro explícito, dois fusos | .local/postgres-datetime-sort/ |

```sh
make test GOFLAGS='-count=1 -p=1'
make vet
make cover GOFLAGS=-p=1
make standalone-example-test
make postgres-ha-status POSTGRES_HA_PORT=5540
make postgres-ha-test POSTGRES_HA_PORT=5540
make postgres-ha-migrations-test POSTGRES_HA_PORT=5540
make postgres-ha-explain-test postgres-ha-where-test postgres-ha-id-sort-test \
  postgres-ha-sparse-test postgres-ha-typed-sort-test postgres-ha-plan-policy-test \
  postgres-ha-datetime-sort-test POSTGRES_HA_PORT=5540
# Só depois dos testes/cobertura, sem builds Docker concorrentes:
make e2e
cd website && npm run build
```

Relatórios contêm SQL/dados determinísticos de fixture, sem credenciais.
Confira report.json.complete, contagens, erros e SQL capturado; PASS Go não
basta se a matriz estiver incompleta. Schema é removido antes da publicação
completa. A cobertura normal exclui caminhos HA manuais desabilitados e
testkit; percentual de cobertura e número de planos medem coisas diferentes.

Failover e PITR foram validados no trabalho local anterior A2/A3; harness e
runbooks estão em infra/postgres-ha/. São evidências históricas, não reexecução
pelos gates de rotina desta consolidação. Comandos matam um primário ou criam
sandbox de restore e exigem execução manual deliberada. Não há provisionamento/
execução HA no GitHub Actions; 5434 pertence a outro projeto local. O
[runbook HA](https://github.com/Gui-Rondon/hyrule/blob/integration/postgres-portability/infra/postgres-ha/README.md)
registra escopo/limites, sem ser projeto de deploy de produção.

## Execução desta consolidação

Passaram nesta consolidação: suíte completa com race (`-count=1 -p=1`), vet,
teste Mongo do módulo externo, topologia, migrations, sete relatórios completos
(**216 planos**), compilação dos programas Mongo/Postgres da documentação e
JMeter/PubSub (**42 amostras, zero falhas**). Cobertura subiu de **85,5% para
85,9%** (gate 85%); Connect foi de 73,9% a 95,7%, e Env.Wrap/finishBuild
atingiram 100% no perfil medido. O build EN/PT passou.

O [snapshot versionado](https://github.com/Gui-Rondon/hyrule/blob/integration/postgres-portability/infra/postgres-ha/evidence/2026-11-15-consolidation.json)
registra contagens por gate, observações por modo e hashes dos relatórios locais.
Failover/PITR estão marcados como históricos, fora destas novas execuções.

Artefatos brutos ignorados ficam em .local/backend-consolidation/; o registro
versionado traz comandos e critérios reproduzíveis. Dados locais, planos não
versionados e credenciais não são dependências desta documentação. O merge em
main permanece decisão separada.
