# Compor Hyrule com Gin, Chi ou Echo

Este módulo separado testa o checkout atual com `replace` local. Com Go 1.26 ou
1.27, execute `go run .` (Chi), `ROUTER=gin go run .` ou `ROUTER=echo go run .`.
Abra `/hyrule/api/docs/` e envie JSON para `/hyrule/products`. SQLite persiste em
hyrule.db; a autenticação está desativada para experimentação.

`Mount` preserva a URL completa ao encaminhar ao http.Handler.
`app.Spec.URLPrefix` remove `/hyrule` uma única vez. Não acrescente StripPrefix.
O roteador hospedeiro pode controlar outras rotas e middleware; o Hyrule mantém o
roteamento interno dos recursos, a autenticação e a validação.

`go test -race ./...` verifica CRUD, ETags, 404/405, middleware, lookup e Swagger
com prefixo. Para consumir a release publicada, remova `replace` e fixe a versão
que incorporar esta entrega. As dependências dos roteadores ficam neste exemplo.
