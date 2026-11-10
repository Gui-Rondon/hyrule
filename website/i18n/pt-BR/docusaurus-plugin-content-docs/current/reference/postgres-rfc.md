---
sidebar_position: 9
---

# Adicionando um backend Postgres

Esta página é um documento de design, não uma referência de feature: ela
registra as decisões abertas para um segundo backend, além do Mongo, e
uma recomendação pra cada uma — não algo já construído. Hoje existe um
esqueleto (`platform/postgres`: um tipo no formato de `Store`,
com seu próprio tipo de filtro, provado que compila e segue o mesmo
padrão de `platform/mongodb`) e nada ligado a `rest` ainda.
Trate cada "recomendado" abaixo como ponto de partida pra discussão, não
como decisão já tomada; os dois pontos marcados como abertos estão
explicitamente não decididos.

## Por que isso existe

[`store.md`](store.md) e [`architecture.md`](architecture.md) já dizem,
explicitamente, que a portabilidade de storage de hoje é a *interface*
`Store`, não a linguagem de query nem a semântica de erro: os métodos de
`rest.Store` são tipados em `bson.M`/`bson.D`, e a tradução de erro de
`rest.Service` (`queryError`, `duplicateKey` em
`rest/service.go`) checa tipos do driver Mongo direto
(`mongo.IsDuplicateKeyError`, códigos de `mongo.ServerError`,
`mongo.IsTimeout`). Isso foi uma escolha deliberada pra primeira rodada,
não um descuido — esta página é a "próxima rodada" que essas páginas
apontam.

## 1. A forma de um `Store` do Postgres

**Decidido.** `platform/postgres.Store` não tenta satisfazer
`rest.Store` — essa interface é Mongo-shaped de propósito. Em vez disso
tem seu próprio método-set, inspirado nele (`Find`, `CountDocuments`,
`FindOne`, `InsertMany`, `UpdateOne`, `ReplaceOne`, `DeleteOne`), com
tipos nativos de Postgres pro filtro/update em vez de
`bson.M`/`bson.D`. Isso espelha como `mongodb.Store` já "satisfaz
estruturalmente" `rest.Store` sem importar `rest` — o mesmo padrão, um
pacote diferente, com seus próprios tipos.

Explicitamente **não** perseguido: uma AST de filtro compartilhada que
tanto `mongodb.Store` quanto `postgres.Store` compilam a partir dela.
`where=` continua sendo a linguagem de query do Mongo como está; um
recurso em Postgres define sua própria convenção de filtro, no seu
próprio tempo — não uma tradução de operadores Mongo pra SQL.

## 2. Como um recurso de fato usaria isso — aberto

Esta é a pergunta que a separação de interface do ponto 1 não responde:
`rest.Resource.Store` é tipado na interface `Store` Mongo-shaped hoje,
então um `Store` do Postgres não entra no lugar por troca simples. Dois
caminhos, nenhum escolhido ainda:

- **Contornar `rest` inteiramente (custo menor, ponto de partida
  recomendado).** Um recurso em Postgres fala com
  `platform/postgres.Store` direto do seu próprio handler HTTP, do
  jeito que `outbox` já fala com `*mongo.Collection` direto, de
  propósito, contornando `rest.Store` (`architecture.md`: "Storage é
  uma decisão separada da de eventos"). Não ganha nada da máquina do
  `rest` de graça — sem `where=`, sem paginação/links/etags estilo Eve
  — e define sua própria convenção de query string.
- **Generificar `rest.Store`/`Service`/`Resource` (corte maior, não
  tentado ainda).** Generics do Go (`Store[F, U any]`) poderiam deixar
  `rest` continuar dirigindo paginação/links/etags pros dois backends,
  cada backend fornecendo seu próprio tipo de filtro `F` e de update
  `U`. Isso toca a maior parte de `rest/service.go` e força
  uma decisão sobre o que mais precisa virar plugável por backend —
  começando pelo ponto 3 abaixo, já que `queryError`/`duplicateKey` não
  podem continuar hardcoded em tipos de erro do Mongo quando um segundo
  backend for real.

## 3. Erros

`queryError`/`duplicateKey` de `rest/service.go` checam tipos
do driver Mongo direto hoje. Um equivalente em Postgres checaria
`*pgconn.PgError`:

| Mongo hoje | Equivalente Postgres | Significado |
|---|---|---|
| `mongo.IsDuplicateKeyError` | `Code == "23505"` (`unique_violation`) | → 409 no insert, 400 no update/replace, igual ao `duplicateKey` de hoje |
| códigos de erro `2`, `9`, `51091`, `51108` (`clientQueryErrors`) | `22P02` (`invalid_text_representation`), `42601` (`syntax_error`) e similares | → 400, causado pelo cliente |
| `mongo.IsTimeout` / `context.DeadlineExceeded` | timeout de statement/context | → 503, igual a hoje |

Não perseguido ainda: uma interface de classificação de erro
compartilhada entre backends. O ponto 2 acima precisa se resolver
primeiro — não há um lugar único pra pendurar isso até então.

## 4. Índices

O formato `IndexSpec`/`indices.conf` de `platform/mongodb`
(direções `1`, `-1`, `text`, `hashed`, `2dsphere`; opções `unique`,
`sparse`, `name=`, `ttl=`) é Mongo-shaped do início ao fim — ver
[Performance de query hoje](store.md#performance-de-query-hoje). Um
equivalente em Postgres precisa do seu próprio formato, não um reuso
deste parser: *tipo* de índice (`btree` padrão, `gin` pra
`jsonb`/array/full-text, `gist` pra tipos geométricos/de intervalo,
`brin` pra tabelas grandes e naturalmente ordenadas) em vez de direção,
mais índices parciais e de expressão, que não têm equivalente no Mongo.
Sem `ttl` nativo — Postgres precisa de cron job ou trigger pra expiração.

## 5. Forma dos dados — aberto, sem recomendação ainda

Mongo guarda documentos sem schema; `rest.Doc` é `map[string]any` em
todo `rest`/`declarative`. Postgres é relacional, e há dois caminhos
genuinamente diferentes até lá, registrados aqui sem recomendação:

- **Colunar.** O `rest.Schema`/YAML declarativo de um recurso
  precisaria gerar DDL de tabela real (e migrations). Ganha tipos
  nativos e indexação — o motivo real de usar Postgres — mas abre mão
  da suposição de `Doc` sem schema que hoje está embutida em
  `rest`/`declarative`.
- **Uma única coluna `jsonb`.** Tradução quase zero — `Doc =
  map[string]any` mapeia pra `jsonb` quase direto — mas enfraquece o
  motivo de usar Postgres: você estaria comprando principalmente
  garantias transacionais/relacionais, não colunas tipadas ou indexação
  de verdade.

## 6. Agregações — não é uma lacuna de paridade

Diferente do Mongo, Postgres não precisa de uma válvula de agregação
como a que `GET /categories/tree` do `categories-api` precisa hoje (ver
[`store.md`](store.md#pontos-que-ainda-usam-mongo-direto-de-propósito)):
joins, window functions e CTEs são SQL nativo. Registrado aqui pra não
ser confundido com uma feature faltando depois — é uma diferença de
natureza, não algo que o suporte a Postgres ainda deve.

## 7. Driver

**Recomendado:** [`github.com/jackc/pgx/v5`](https://github.com/jackc/pgx)
(`pgxpool` pro pooling). Segue o padrão já existente no projeto de driver
direto e idiomático sem ORM no meio (`mongo-driver/v2`, `go-redis/v9`), e
`pgconn.PgError` dá os códigos de erro do ponto 3 nativamente, sem uma
segunda camada de parsing. Também satisfaz tudo que os pontos 8 e 9
abaixo precisam — `LISTEN`/`NOTIFY`, `FOR UPDATE SKIP LOCKED`, `COPY`,
batching, ajuste de pool — nada disso exige outro driver ou uma segunda
biblioteca.

## 8. Segurança: SQL injection e além

O esqueleto de hoje não tem o problema de SQL cru vindo de entrada não
confiável: todo valor chega ao banco como parâmetro de query (`$N`),
nunca construído dentro do texto da query, e todo identificador — nome
de tabela e de coluna — passa por `pgx.Identifier{...}.Sanitize()`, a
mesma técnica do `quote_ident()` do próprio Postgres, antes de ser
concatenado no SQL. Não existe hoje um caminho de um valor de
`Filter`/`SortField`/lista de colunas até texto SQL cru.

Isso vale porque o único chamador de hoje é código Go escrito por quem
monta o `Store` — o mesmo modelo de confiança que `mongodb.Store` tem.
Isso deixa de valer no momento em que o ponto 2 se resolver e uma query
string de requisição HTTP equivalente a `sort=`/`projection=`, ou o
corpo de um write, alimentar `SortField.Column`, o argumento
`columns []string`, ou as chaves de `Filter` direto. O `Sanitize()`
ainda previne *injeção* nesse caso — um campo chamado
`x"; DROP TABLE y; --` vira uma referência de coluna sintaticamente
segura e inútil, não SQL executável — mas não previne *acesso não
autorizado*: nada impede um cliente de pedir pra ordenar por, ou ler,
uma coluna que o Schema do recurso nunca quis expor. O que quer que
ligue HTTP a `postgres.Store` precisa validar todo nome de coluna vindo
do cliente contra uma allowlist — o Schema declarado do recurso — antes
de chegar no `Store`, o mesmo trabalho que `rest` já faz pro Mongo ao
rejeitar campos desconhecidos durante a validação. Sanitização sintática
e autorização são dois trabalhos diferentes; o código de hoje só faz o
primeiro, porque ninguém o chama com nomes de coluna não confiáveis
ainda.

Mais duas lacunas que um backend Postgres de produção tem e que o
esqueleto ainda não fecha:

- **Um timeout de query.** Nada define `statement_timeout` hoje, então
  um filtro patológico pode segurar uma conexão do pool indefinidamente
  — `IsTimeout` já antecipa o erro resultante (código `57014`), mas
  nada o produz ainda. `pgxpool.Config.ConnConfig.RuntimeParams` (ou um
  `SET LOCAL statement_timeout` por chamada) fecha isso.
- **Uma connection string que force TLS do jeito certo pro ambiente**
  (`sslmode=require`/`verify-full` fora do dev local) — a mesma pergunta
  operacional que a conexão do `mongodb` já responde, só que ainda não
  escrita pro Postgres (ver a lacuna do `Connect` no ponto 9).

Uma linguagem de filtro mais rica no futuro (o ponto 1 ainda não está
decidido) carrega a mesma regra adiante: seja qual for a sintaxe que
substituir o `Filter` só-de-igualdade, todo valor que ela aceitar —
inclusive um padrão de `LIKE` — ainda precisa virar parâmetro
vinculado, nunca texto emendado na query. É a mesma postura que o
`where=` do próprio Mongo adota com sua denylist de operadores
(`$where`, `$function`, `$accumulator` bloqueiam execução de código,
não só sintaxe ruim — ver [`querying.md`](querying.md)).

## 9. Além da paridade: o que o Postgres faz que esse esqueleto ainda não usa

Tudo acima é sobre alcançar a forma que o `Store` do Mongo já tem. O
Postgres também tem capacidades que o lado Mongo deste framework não
tem — não necessárias pra paridade, mas que vale nomear pra uma rodada
futura não precisar redescobrir. O `pgx/v5` já cobre tudo isso; nada
disso exige um driver diferente.

- **Um outbox transacional que não precisa de um mecanismo de
  transação separado.** `outbox` grava na coleção `outbox` do
  Mongo na mesma transação multi-documento do write do recurso — uma
  feature real, mas que no Mongo exige replica set e um
  `WithTransaction` explícito. No Postgres, gravar a linha do recurso e
  a linha do outbox juntas é o padrão: um `BEGIN`/`COMMIT`, sem setup
  especial. O lado do relay também pode ir além do polling: um trigger
  que dá `NOTIFY` no insert, mais um relay que dá `LISTEN`
  (`pgx.Conn.Exec(ctx, "LISTEN outbox_insert")` +
  `conn.WaitForNotification(ctx)`), entrega eventos novos sem
  latência de polling, e `SELECT ... FOR UPDATE SKIP LOCKED` deixa vários
  workers de relay tirarem da fila ao mesmo tempo sem brigar pelas
  mesmas linhas — SQL puro, nada específico do pgx pra adicionar. A
  pegadinha real: `LISTEN`/`NOTIFY` precisa de uma única conexão
  dedicada, aberta durante toda a vida do relay, não o padrão normal de
  pegar-e-devolver do `pgxpool.Pool` — a conexão que escuta não pode ser
  uma que o pool entregue pra outro lugar no meio da espera. Ter isso de
  verdade significa dar a `outbox` uma contraparte em Postgres
  (hoje ele é hardcoded pra `*mongo.Collection`) — uma refatoração que
  depende do ponto 2 se resolver primeiro, não algo pra começar antes de
  existir um recurso real em Postgres emitindo eventos.
- **Inserção em massa via `COPY`, não `INSERT`s em batch.** `InsertMany`
  hoje faz batch de um `INSERT` por doc via `pgx.Batch` (ver
  [`store.md`](store.md)) — correto, mas `pgx.CopyFrom` é
  substancialmente mais rápido pra slices grandes, usando o protocolo
  binário de cópia do Postgres em vez de N statements de insert. Vale a
  pena quando `InsertMany` estiver lidando com batches de tamanho de
  produção, não antes disso.
- **Planos de query de verdade.** [Performance de query
  hoje](store.md#performance-de-query-hoje) já contrasta a falta de
  `explain()` do Mongo com o `EXPLAIN ANALYZE`; nada no esqueleto expõe
  isso ainda. Um `Store.Explain` que roda `EXPLAIN (FORMAT JSON) <query>`
  e devolve o plano seria uma adição pequena quando houver uma query de
  verdade pra otimizar.
- **Ajuste do pool de conexões.** `pgxpool.New` hoje usa só os defaults
  da própria connection string — nenhum `MaxConns`/`MinConns`/intervalo
  de health-check exposto, do jeito que `mongodb.Connect` lê as chaves
  de config `mongo.*`. Um par `postgres.Connect`/`postgres.URI`
  espelhando o `mongodb.go` é o lugar natural pra isso, e pras
  configurações de TLS/timeout do ponto 8 — código novo, não uma
  refatoração do `Store` em si.

Uma coisa que *não* está nessa lista: o pgx não faz split de
leitura/escrita nem roteamento de conexão entre réplicas — isso é uma
preocupação de deploy (PgBouncer/PgCat, ou vários `pgxpool.Pool`
separados por papel), não algo que o driver ou este `Store` devam
assumir.

## O que existe hoje

- `platform/postgres.Store`: o método-set acima, sobre
  `pgxpool.Pool`, com um filtro só de igualdade (`map[string]any` lido
  como `coluna: valor`, sem operadores) — real, mas intencionalmente
  mínimo; não é o lugar de descanso do design de filtro do ponto 1, só o
  suficiente pra provar que a forma compila e tem onde crescer. Um valor
  `nil` significa `IS NULL` (não o `= NULL` do SQL, que é sempre falso),
  e `InsertMany` rejeita qualquer doc cujas chaves não batam exatamente
  com as de `docs[0]`, em vez de escrever `NULL` silenciosamente ou
  descartar uma coluna.
- `platform/postgres.IsDuplicateKeyError`/`IsTimeout`: as duas
  checagens da tabela do ponto 3, espelhando os helpers de mesmo nome do
  `mongo-driver`.
- Um serviço `postgres` no `docker-compose.yml`, junto de `mongo`,
  `redis` e `pubsub`, pros testes do esqueleto rodarem contra um banco
  real, não um mock.
- Nenhuma mudança em `rest` nem em nenhum recurso existente —
  é um pacote novo e isolado; nada acima carrega risco de regressão pro
  que já está em produção hoje.

Usado isolado, do jeito que o caminho recomendado do ponto 2 propõe
(sem `rest` em volta, igual a `outbox` falando com
`*mongo.Collection` direto):

```go
pool, err := pgxpool.New(ctx, os.Getenv("POSTGRES_CONNECTION_STRING"))
store := postgres.NewStore(pool, "tags")

docs, err := store.Find(ctx, postgres.Filter{"status": "draft"}, 0, 10,
	[]postgres.SortField{{Column: "created_at", Desc: true}}, nil)
```

Compare com o trecho do Mongo em [`store.md`](store.md) — mesma forma
(`NewStore`, `Find` com filtro/skip/limit/sort/colunas), tipo de filtro
diferente, e ainda sem um `rest.Resource` envolvendo isso.
