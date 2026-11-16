---
sidebar_position: 5
---

# CI e implantação: adapte o exemplo ao seu projeto

:::info[O que você vai aprender]
- Como aproveitar o fluxo de CI do exemplo `blog-apis`
- Como `ci/affected` identifica os serviços afetados por uma mudança
- Onde configurar o comando de implantação do seu provedor
:::

## Ainda não há `hyrule build --affected` nem `hyrule deploy`

Esses comandos não foram implementados; veja o
[ADR-0004](/docs/reference/architecture-decisions). Também não há um sistema
genérico de implantação por trás deles: o
[ADR-0006](/docs/reference/architecture-decisions) adiou essa abstração até
existir uma necessidade concreta de atender a vários provedores.

O que já existe é o fluxo de CI de
[`examples/blog-apis`](https://github.com/Gui-Rondon/hyrule/tree/main/examples/blog-apis),
que pode ser adaptado ao seu projeto.

## Como o fluxo funciona

[`ci/affected`](https://github.com/Gui-Rondon/hyrule/tree/main/ci/affected) é um
programa Go incluído no módulo `github.com/Gui-Rondon/hyrule`. A partir de um
commit de referência, ele lista os serviços cujos binários foram afetados:
por alterações nos próprios arquivos, em `deploy/<service>/` ou em dependências
de `go.mod` e `go.sum`. Mudanças compartilhadas, como a versão do Go ou um
`Dockerfile` comum, afetam todos os serviços.

O workflow
[blog-apis-affected.yml](https://github.com/Gui-Rondon/hyrule/blob/main/.github/workflows/blog-apis-affected.yml)
executa a ferramenta nos pushes e pull requests deste repositório:

```bash
go run ./ci/affected "$base"
```

Em seguida, compila somente os serviços listados e executa o
`deploy/<service>/deploy.sh` de cada um, quando esse arquivo existe.

## Adapte para o seu projeto

1. Use a estrutura `services/<name>` gerada pela CLI, compatível com
   `go build ./services/<name>`. Veja [Crie serviços](./creating-services.md).
2. Execute a ferramenta a partir do módulo do qual seu projeto já depende:
   `go run github.com/Gui-Rondon/hyrule/ci/affected -services services "$base"`.
   A opção `-services` aponta para o seu diretório, em vez do padrão
   `examples/blog-apis/services`.
3. Copie `blog-apis-affected.yml` e ajuste os caminhos do workflow.
4. Para cada serviço com implantação independente, crie um script executável
   `deploy/<service>/deploy.sh` com o comando do seu provedor, como `kubectl apply`,
   `flyctl deploy` ou `gcloud run deploy`. A ferramenta decide quando chamá-lo;
   o conteúdo do script é de sua responsabilidade. Os scripts do exemplo apenas
   exibem o que fariam, sem publicar em um ambiente real.

## Por que isso fica fora do framework e da CLI

Uma interface de implantação com implementações para vários provedores pode
fazer sentido no futuro. Por enquanto, o
[ADR-0006](/docs/reference/architecture-decisions) mantém os scripts por não haver
um segundo provedor real que ajude a definir essa interface. É o mesmo princípio
do [ADR-0003](/docs/reference/architecture-decisions): abstrações de portabilidade
devem partir de necessidades concretas.

Um script do projeto permite configurar a implantação sem assumir um contrato
que ainda não foi validado com usos diferentes.

:::tip[Retomando]
- `ci/affected` já pode ser usado diretamente pelo módulo Go.
- O workflow de `examples/blog-apis` serve de base para o CI do seu projeto.
- A implantação fica em `deploy/<service>/deploy.sh`, com os comandos do provedor
  escolhido.
:::
