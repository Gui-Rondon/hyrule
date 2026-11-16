---
sidebar_position: 20
---
# SQLite para experimentação local

SQLite é um backend experimental, disponível na próxima release contendo esta
entrega. `hyrule new my-api` gera uma API única; `hyrule service create products`
adiciona um recurso e baixa dependências. `go run .` atende na porta 8080. Os dados
persistem em hyrule.db; SQLITE_PATH altera o caminho. Dispensa Docker e compilador C.
Sem CONFIG_FILE ou CONFIGSERVER_ADDRESS, a autenticação local fica desativada.
Forneça configuração explícita ao expor o serviço.

`app.BackendSQLite` usa `Deps.SQLite` pertencente ao chamador em Build; Run abre e
fecha sua própria conexão. No setup, chame
`restsqlite.NewAdapter(ctx, env.SQLite, tabela, recurso)`, atribua Resource.Adapter e
registre o recurso. SQLiteTx mantém hooks e inserções em lote na mesma transação;
transações aninhadas do mesmo banco compartilham o contexto e falhas fazem rollback.

Suporta CRUD, validação, UUID, paginação, índices únicos escalares, ETags com versão
inteira e compare-and-swap, merge patch RFC 7396, projeção/ordenação no primeiro
nível e igualdade/comparações escalares com AND. Campos e tabelas exigem
identificadores simples. Hífens de nomes gerados viram underscores nas tabelas.
Objetos e arrays podem ser armazenados, mas não comparados ou indexados como únicos.
Valores usam parâmetros SQL; operadores e identificadores são validados.

Não suporta soft delete, operadores avançados Mongo/Postgres, outbox, dry run
ou garantias de paridade/migração. `resources.yaml` aceita `backend: sqlite`,
com as mesmas restrições desta página; veja [recursos em YAML](resources-yaml.md). Operadores incompatíveis
retornam 400; configuração incompatível falha no startup. Índices únicos são
restrições reais do banco. Campo ausente e null comparam como SQL NULL, sem prometer
paridade Mongo. Uma conexão serializa escritas locais; o foco é experimentação.

Tabelas/índices são criados ao registrar recursos. CREATE IF NOT EXISTS não migra
schemas existentes. Alterações exigem migração explícita; excluir o arquivo local
reinicia a experiência e perde os dados.

## Migração do CLI

`hyrule new` agora gera a API SQLite única; use `--layout=platform` para manter a
plataforma Mongo. `hyrule.json` registra a escolha; projetos antigos sem manifesto
continuam funcionando. O CLI publicado fixa a própria versão; builds de
desenvolvimento exigem `--framework-version` compatível. Nenhum recurso existente
é sobrescrito, inclusive nomes diferentes que normalizam para o mesmo identificador.
