---
sidebar_position: 2
---

# Crie um projeto

:::info[O que você vai aprender]
- Como instalar a CLI `hyrule`
- Quais arquivos `hyrule new` gera
- Como adicionar o Hyrule às dependências do projeto
:::

## 1. Instale a CLI

```bash
go install github.com/Gui-Rondon/hyrule/cmd/hyrule@main
```

`@main` instala a versão da branch principal. Para fixar uma versão publicada,
substitua esse sufixo pela tag desejada. Consulte o
[roteiro de evolução](/docs/reference/roadmap) para entender o escopo da CLI.

## 2. Gere a estrutura do projeto

```bash
hyrule new my-platform
cd my-platform
```

O comando cria:

- `go.mod`, com `module my-platform` e a versão do Go. A dependência do Hyrule
  será adicionada no próximo passo, na versão que você escolher.
- `Makefile`, com os alvos `build`, `run`, `test`, `env-up` e `env-down`,
  seguindo a estrutura do Makefile deste repositório.
- `docker-compose.yml`, com um replica set Mongo de nó único em
  `localhost:27017`, suficiente para `rest.Store` e as transações da outbox.
- `README.md` e `.gitignore`.
- `services/`, inicialmente vazio. Na próxima página, você vai
  [criar os serviços](./creating-services.md).

## 3. Adicione o Hyrule como dependência

```bash
go get github.com/Gui-Rondon/hyrule@main
```

É uma dependência Go comum. Use `@main` para acompanhar a branch principal ou
uma tag para fixar uma versão. Para experimentar com um checkout local, veja o
`replace` no `go.mod` de
[`examples/standalone-tags-api`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/standalone-tags-api).

:::tip[Retomando]
- `hyrule new <project>` gera a estrutura de um projeto Go, sem formato próprio
  de configuração.
- A dependência do Hyrule é acrescentada com `go get`, na versão escolhida.
:::
