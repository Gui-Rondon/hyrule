---
sidebar_position: 3
---

# Outbox transacional com Mongo e Postgres

Ambos confirmam a escrita do recurso e o evento pendente na **mesma transação
local do banco**. O relay publica após commit, pelo menos uma vez. Consumidores
deduplicam por `outbox.AttrEventID` (`event_id`); perda de confirmação de recebimento pode
publicar novamente. Não há transação distribuída com Pub/Sub, Redis ou outro
serviço.

## Integração Mongo

```go
box := outbox.New(env.DB)
res.Hooks.Use(events.Hooks(box))
relay := &outbox.Relay{Outbox: box, Publisher: publisher}
env.Go("outbox-relay", relay.Run)
check, err := outbox.CheckFromConfig(env.Config, relay)
if err != nil { return err }
env.AddCheck(check)
```

Use transação em replica set. `outbox.New` define o verificador transacional. `relay.Run` garante os índices
pending/TTL; quem chama Drain diretamente deve executar
`relay.EnsureIndexes(ctx)` no código de inicialização. `Add` rejeita contexto fora da
transação com `outbox.ErrNoTransaction`. `AllowNoTransaction` serve para
standalone de desenvolvimento e elimina atomicidade. O relay Mongo usa lease
de líder único e ordena pendentes por esse relay; falhas têm backoff e retry
sem limite integrado de dead-letter/replay. Publicados têm retenção/TTL.
Veja `posts-api` para a integração Mongo completa.

## Integração Postgres

```go
if err := outboxpostgres.EnsureTable(ctx, env.Postgres, "outbox"); err != nil { return err }
box := &outboxpostgres.Outbox{Pool: env.Postgres, Table: "outbox"}
res.Hooks.Use(events.Hooks(box))
relay := &outboxpostgres.Relay{Outbox: box, Publisher: publisher, Workers: 1}
env.Go("outbox-relay", relay.Run)
check, err := outbox.CheckFromConfig(env.Config, relay)
if err != nil { return err }
env.AddCheck(check)
```

Use o **mesmo pool** do adapter e de `env.PostgresTx`. `Add` usa a transação do
contexto, rejeita contexto ausente/de outro pool com o mesmo
`outbox.ErrNoTransaction` e não inicia outra transação silenciosamente.
`EnsureTable` é DDL de código de inicialização, não requisição do recurso nem motor de migrações;
gerencie explicitamente mudanças incompatíveis em instalações existentes.

O relay Postgres usa `FOR UPDATE SKIP LOCKED`. Vários workers/processos podem
publicar em paralelo; **não há ordem por Key entre workers**. MaxAttempts padrão
é 10; eventos esgotados recebem `dead_at`, permanecem armazenados e saem da
busca de pendentes. `relay.Replay(ctx, id)` recoloca somente um ID bigint morto
na fila, limpando estado de retry/dead; os demais retornam `ErrNotReplayable`.
Publicados não têm TTL Mongo equivalente: defina retenção separadamente.
Veja `comments-api` para testes reais de HTTP, transação e entrega Pub/Sub.

`outbox.CheckFromConfig` funciona com ambos (`outbox.max_delay`, padrão 5m).
Mede a idade do backlog pendente; linhas mortas Postgres são excluídas.
Monitore/opere o backlog morto separadamente: pending verde não prova ausência
de eventos que falharam.

## Hooks e publicadores compartilhados

A lógica de negócio pode associar um evento antes da escrita:

```go
ctx = events.With(ctx, outbox.Event{Topic: "items-changed", Type: "item.changed", Key: id, Body: payload})
// Passe ctx à escrita do Service. events.Hooks grava no hook anterior à escrita.
```

Ou registrar condição de transição com qualquer box:

```go
condition := outbox.OnFieldChange("status", []string{"published"}, func(doc map[string]any) outbox.Event {
    return outbox.Event{Topic: "items-published", Type: "item.published", Key: fmt.Sprint(doc["_id"]), Body: doc}
})
res.Hooks.Use(events.ConditionalHooks(box, condition))
```

Use a formatação de ID do contrato de mensagem (ObjectID.Hex no Mongo quando
necessário; UUID.String no Postgres). PATCH comum que produz a mesma transição
efetiva também dispara a condição. Evite emitir o mesmo evento de negócio
involuntariamente pelas duas estratégias de hooks.

`publisher` implementa `outbox.Publisher`; `outbox.PubSub` e `outbox.Dispatcher`
funcionam com ambos. Provisione tópicos/subscriptions separadamente. Destinos
locais também executam **após commit**: atualização Redis não é atômica com a
escrita original Mongo/Postgres. Falhas locais são executadas novamente pelo relay;
handlers precisam de idempotência como consumidores remotos.

Hooks anteriores ficam na transação de banco. `env.Go` suprime relays em dry
run; dados e eventos pendentes sofrem rollback juntos. Build sem `Deps.Start`
não inicia relays: testes chamam Drain ou executam worker ao verificar entrega.
[Configuração](backends.md), [dry run](dry-run.md) e [testes](testkit.md) detalham o entorno.
