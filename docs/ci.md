# CI responsibilities and dependency updates

`Test` keeps separate jobs for Go 1.26, Go 1.27, native Windows, and the EN/PT
website build. Linux and Windows require different runners; separate Go jobs
prove the supported compiler matrix. CodeQL and vulnerability checks remain
independent so a failing test cannot prevent the security report from running.

The Go 1.27 job runs the full integration suite once with both race detection
and the 95% runtime coverage gate. Go 1.26 runs the same suite with race detection.
SQLite and generated-project tests are included in those runs. `make unit`
remains available locally for a quick run without backend infrastructure.

## External examples

The root `go test ./...` does not descend into the two example modules. Each has
its own go.mod/go.sum and replaces Hyrule with this checkout. Updating a root
requirement can change the effective dependency graph of both examples.

Before their tests or scans, CI runs `make examples-tidy`. This explicitly refreshes
only the example manifests for the checked-out root, then performs real tests and
scans. It does not tidy the root or suppress a security finding. Contributors can
run the same command and review/commit example manifest changes alongside root
updates. A module-resolution error is different from a vulnerability finding.

## Avoiding repeated work

- No separate full-suite run before infrastructure and no second integration
  run just for coverage on Go 1.27.
- New commits cancel superseded running PR executions within the same workflow.
- Dependabot groups version updates by ecosystem (Go, Actions, website), limiting
  simultaneous open update PRs. Already-open PRs are not closed or rewritten by
  this configuration change; they need an update/rebase after the CI fix merges.
- Workflow/check names stay stable for README badges and branch protection.

Combining everything into one job would serialize unrelated checks and prevent
one runner from validating both operating systems. Fewer duplicated commands and
fewer dependency PRs reduce the work while preserving the independent checks.
