---
sidebar_position: 4
---

# Dry run: roda tudo, não escreve nada

Dry run deixa um serviço receber tráfego real (inclusive escritas) sem
gravar nada de fato — útil pra validar uma versão nova contra tráfego de
produção antes de ela ficar com a palavra final. Liga pela variável de
ambiente `DRY_RUN` (nunca pela configuração — `app.Build` recusa a subir
se achar uma chave `dry_run` na config, de propósito: é um interruptor de
deploy, não algo que fica esquecido ligado numa config compartilhada).

```bash
DRY_RUN=1 DRY_RUN_TIMEOUT=10s ./minha-api
```

## Como funciona

- Toda escrita roda numa transação Mongo que sempre reverte
  (`mongodb.Transactor{Rollback: true}`, montado por `app.Build` quando
  `DRY_RUN=1`): o cliente recebe a resposta como se tivesse escrito, mas
  nada fica no banco.
- Os hooks pós-escrita (`OnInserted`, `OnUpdated`, `OnReplaced`,
  `OnDeleted`) não rodam — eles alcançam sistemas fora da transação (ES,
  Pub/Sub...), que não têm rollback. Ver `runPost` em
  `internal/evecompat/service.go`.
- Tarefas de fundo registradas com `env.Go` (relay do outbox, por exemplo)
  não iniciam.
- `DRY_RUN_TIMEOUT` limita cada requisição (default 3s,
  `internal/app/app.go`'s `defaultDryRunTimeout`); sem ela, um dry run
  poderia prender a próxima requisição do cliente por mais tempo do que
  o original levaria pra responder.
- Dry run exige um replica set (sem isso não há transação pra reverter) e
  um `*mongodb.WriteGuard` no cliente Mongo — `app.Run` monta os dois;
  quem chama `app.Build` direto (testes) precisa passar ambos.

## O vigia de escritas fora de transação

Nem todo write passa pelo caminho normal de `evecompat.Service` — um
endpoint custom pode escrever direto no `Store`. Se esse write não estiver
dentro de uma transação, o rollback automático não o alcança: ele seria
gravado de verdade, mesmo em dry run.

`mongodb.WriteGuard` (`internal/platform/mongodb/guard.go`) é um monitor
de comandos do driver: não consegue impedir a escrita (o driver não dá
esse gancho), mas conta e loga toda escrita vista fora de uma transação.
`/healthcheck` falha permanentemente a partir da primeira violação
(`dryRunCheck` em `internal/app/dryrun.go`) — fica vermelho até o pod
reiniciar, mas `/status` continua respondendo, então o pod não some da
rotação por causa disso.

## Rotas que não têm volta

Uma rota cujo efeito sai do Mongo e não tem rollback (uma chamada HTTP pra
outro serviço, por exemplo) usa `Env.RefuseInDryRun`:

```go
env.API.Handle("POST /posts/{id}/notify", env.RefuseInDryRun(
	"chama outro serviço, sem rollback", http.HandlerFunc(e.notify)))
```

Em dry run isso responde 503 com o motivo, em vez de executar.
