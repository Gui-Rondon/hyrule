# Contributing

Start with [the roadmap](ROADMAP.md) and [compatibility policy](COMPATIBILITY.md).
Issues labelled `good first issue` should have a small scope, reproduction or
example, and acceptance criteria. Candidate tasks are in [the backlog](docs/good-first-issues.md).

Use Go 1.26 or 1.27. `make unit` runs tests without databases. For the complete
suite, install Docker, run `make env-up`, then `make test`, `make vet` and
`make cover` (95% runtime-framework gate). `make router-example-test` exercises
Gin/Chi/Echo in a separate module. From website/, `npm ci && npm run build`
validates both documentation languages. SQLite tests need no Docker or C compiler.

## Pull requests and review

1. Open an issue for substantial behavior or contract changes; explain the actual consumer.
2. Add a focused implementation and meaningful regression/contract coverage.
3. Update CHANGELOG.md for observable behavior, migration notes for breaking
   changes, and EN/PT documentation together.
4. Open a PR describing trigger, resulting behavior, validation and limitations.
5. The current maintainer, Gui-Rondon, reviews and merges after required checks
   pass and review conversations are resolved. The current single-maintainer
   process does not claim independent review. Add required independent approvals
   when more maintainers join; an author cannot approve their own PR on GitHub.

Use drafts for incomplete work. Never publish private vulnerability details in a
public issue or PR; follow [SECURITY.md](SECURITY.md). Be respectful and follow
[the code of conduct](CODE_OF_CONDUCT.md).
