---
sidebar_position: 2
---
# Criando um projeto de plataforma

O padrão `hyrule new my-api` cria uma API SQLite única. Para gerar o layout
existente com vários serviços Mongo, selecione a plataforma explicitamente:

```sh
go install github.com/Gui-Rondon/hyrule/cmd/hyrule@latest
hyrule new my-platform --layout=platform
cd my-platform
hyrule service create articles
go mod tidy
make env-up
make build SERVICE=articles
make run SERVICE=articles
```

O go.mod fixa o framework na versão do CLI instalado. Builds de desenvolvimento
exigem `--framework-version=vX.Y.Z` ou uma pseudo-versão publicada. Não execute tidy
antes de criar o primeiro serviço: o projeto vazio ainda não importa o Hyrule e
a dependência seria removida.

`hyrule.json` registra layout/backend. A plataforma contém go.mod, Makefile,
docker-compose.yml, README.md e services/. O layout único cria main.go e arquivos
de recursos na raiz. Projetos existentes com services/ e sem manifesto preservam
o comportamento anterior. Arquivos existentes não são sobrescritos.

A próxima release contendo esta entrega oferece o novo padrão; versões antigas
continuam gerando a plataforma. Veja [SQLite](../reference/sqlite.md) para
experimentação e [compatibilidade](../reference/stability.md) para migração.
