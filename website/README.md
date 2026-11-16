# hyrule docs site

The source of [gui-rondon.github.io/hyrule](https://gui-rondon.github.io/hyrule/),
built with [Docusaurus](https://docusaurus.io/). The actual content lives in
[`docs/`](docs) (English, default) and [`i18n/pt-BR/`](i18n/pt-BR) (Portuguese
translation of Learn, Platform, Reference and the site interface).

## Local development

```bash
npm install
npm start                      # English only, live reload, port 3000
npm start -- --locale pt-BR     # Portuguese only, live reload, port 3000
```

The development server serves **one locale per process**. Its language selector
cannot open another locale on that same server. In particular, plain `npm start`
does not serve `/hyrule/pt-BR/docs/...`. Stop the existing process before starting
a different locale on the same port. To run both development servers in parallel,
use another port, for example `npm start -- --locale pt-BR --port 3001`.

## Preview both languages

```bash
npm run preview    # builds both locales and serves them together on port 3000
```

Use this mode to check translations and switch languages through the site menu:

- English: http://localhost:3000/hyrule/docs/learn/quick-start
- Brazilian Portuguese: http://localhost:3000/hyrule/pt-BR/docs/learn/quick-start

The preview serves a static build and does not live-reload edits. Stop it and
run `npm run preview` again after changes. `npm run build` only generates the
files in `build/`; `npm run serve` serves an existing build without rebuilding.

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
