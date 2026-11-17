---
sidebar_position: 5
---

# Dry run com Mongo e Postgres

`DRY_RUN=1` executa requisições com rollback das escritas de banco. É uma opção
de deploy, não configuração compartilhada: `app.Build` rejeita a chave
`dry_run` na configuração. `app.BackendMongo` e `app.BackendPostgres` suportam.

```sh
DRY_RUN=1 DRY_RUN_TIMEOUT=10s ./my-api
```

## Integração Mongo

`app.Run` instala `mongodb.Transactor{Rollback: true}` e um monitor de comandos
`mongodb.WriteGuard`. Replica set é obrigatório; Mongo standalone não pode
reverter as escritas. Quem chama `app.Build` diretamente fornece `Mongo`, `DB`,
`DryRun: true` e `WriteGuard` no cliente monitorado (veja `testkit.DryRun`).
O monitor registra escritas fora de transações; não as bloqueia. Após violação,
`/healthcheck` permanece falhando até reiniciar, mas `/status` continua
respondendo. Escritas personalizadas devem participar da transação da requisição.

## Integração Postgres

`app.Build` configura `env.PostgresTx` como `postgres.Transactor{Pool: pool,
Rollback: true}`. Passe esse mesmo valor a `restpostgres.NewAdapter`. Escritas
personalizadas em hooks anteriores à escrita devem obter o executor com
`env.PostgresTx.TransactionQuerier(ctx)`. O método rejeita contextos sem
transação ou com transação de outro pool. Em rotas personalizadas, abra a
transação com `env.PostgresTx.WithTransaction` e use o contexto do callback. Nos testes,
use `Deps{Postgres: pool, DryRun: true}`; não precisa de cliente Mongo/WriteGuard.
`pool.Exec` direto ignora a transação e pode persistir dados: Postgres não tem
WriteGuard equivalente para detectar isso. Dry run protege o fluxo transacional
da requisição, não chamadas arbitrárias ao banco nem DDL administrativo.

## Comportamento compartilhado e limites do código de inicialização

- O cliente recebe a resposta da tentativa de escrita; dados de negócio e
  inserts do outbox transacional são revertidos juntos. Uma próxima requisição
  não enxerga essas mudanças tentadas.
- Hooks posteriores (`OnInserted`, `OnUpdated`, `OnReplaced`, `OnDeleted`) são
  suprimidos. Hooks anteriores executam e devem limitar efeitos à transação.
  Tarefas `env.Go`, incluindo relays, não iniciam.
- `DRY_RUN_TIMEOUT` limita cada requisição (padrão 3s). Timeouts HTTP/banco não
  revertem efeitos externos; o [outbox](outbox.md) é a fronteira de entrega.
- Índices declarados usam check em vez de create, salvo mode já off.
  O boot do PostgreSQL em dry run pula a instalação das funções SQL. Execute
  `postgres.EnsureFunctions` no provisionamento ou inicie em modo normal antes
  de espelhar tráfego. Prepare as tabelas dos recursos e do outbox previamente.
  O Setup do aplicativo ainda executa e deve usar `env.DryRun` para evitar
  DDL e efeitos irreversíveis.
- Sequences/identity Postgres podem deixar lacunas após rollback. Dry run
  promete ausência de linhas de negócio confirmadas, não metadados/contadores
  do servidor inalterados.
- `Spec.NoDryRun` recusa serviços que não podem espelhar tráfego com segurança.

Rotas com chamada externa ou efeito sem rollback usam:

```go
env.API.Handle("POST /items/{id}/notify", env.RefuseInDryRun(
    "calls another service, no rollback", http.HandlerFunc(notify)))
```

Em dry run a resposta é 503 com o motivo. Veja [código de inicialização dos backends](backends.md)
e [testes com bancos reais](testkit.md) para os dois caminhos de registro.
