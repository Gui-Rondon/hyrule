# hyrule docs site

The source of [gui-rondon.github.io/hyrule](https://gui-rondon.github.io/hyrule/),
built with [Docusaurus](https://docusaurus.io/). The actual content lives in
[`docs/`](docs) (English, default) and [`i18n/pt-BR/`](i18n/pt-BR) (Portuguese
translation of Learn, Platform, Reference and the site interface).

## Local development

```bash
npm install
npm start                      # English, live reload
npm start -- --locale pt-BR     # Brazilian Portuguese, live reload
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

## Translation workflow

Every Markdown page in `docs/` must have a Brazilian Portuguese counterpart at
`i18n/pt-BR/docusaurus-plugin-content-docs/current/`, with the same relative
path and document ID. Translate headings, prose, admonition titles and example
comments naturally; keep API identifiers, commands and example payloads intact.
Preserve existing heading IDs when rewording headings with inbound links, using
MDX comment IDs (`{/* #existing-id */}`) with the current Docusaurus setup.

Wrap custom React text in `Translate` / `translate` from
`@docusaurus/Translate`, then run `npm run write-translations -- --locale pt-BR`
and review the generated catalogs. `code.json` covers React/theme messages;
navbar, footer and sidebar labels use their plugin catalogs. Translate message
values, preserving IDs and interpolation placeholders. Product names and release
labels may stay unchanged. The copyright notice is language-neutral and keeps
its year dynamic in the site configuration; leave `copyright` out of the
footer catalog when reviewing generated entries.

`npm run check:translations` rejects missing, empty or copied English pages and
missing homepage messages. It runs automatically before `npm run build`, also
in the deployment workflow. This checks completeness, not translation quality;
review the prose and both locales, including navigation, before publishing.
