# hyrule docs site

The source of [gui-rondon.github.io/hyrule](https://gui-rondon.github.io/hyrule/),
built with [Docusaurus](https://docusaurus.io/). The actual content lives in
[`docs/`](docs) (English, default) and [`i18n/pt-BR/`](i18n/pt-BR) (Portuguese
translation of the Reference section).

## Local development

```bash
npm install
npm start          # http://localhost:3000, live reload
```

## Build

```bash
npm run build      # static site in build/
npm run serve       # serve that build locally
```

## Deployment

Deployment is automatic: `.github/workflows/deploy-docs.yml` builds this
site and publishes it via GitHub Pages (Actions-based deployment, not a
`gh-pages` branch) on every push to `main` that touches `website/`.
