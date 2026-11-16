# Maintainer releases and supply chain

Releases are manually dispatched by Gui-Rondon from main after PR review and green
checks. The workflow validates the selected commit on Go 1.26/1.27, runs real
backend integrations and coverage, then builds `hyrule` and `hyrule-serve` for
Linux and Windows (amd64 and arm64, no CGO). No GoReleaser is needed for this small binary matrix.
Cosign may be added later; it is not a prerequisite.

## Publish

1. Review COMPATIBILITY.md, migration notes and CHANGELOG.md. Before v1, a breaking
   change requires a new minor. This delivery changes the scaffold default and
   must not be published as a patch to v0.102.x.
2. In Actions, dispatch **Release** on main with an unused `v0.X.Y` tag (or
   `v0.X.Y-rc.N`). This workflow is restricted to the current maintainer and main.
   v1 requires a separate maturity decision; v2+ requires a new module path.
3. Inspect checksums and verified signed SLSA provenance. The workflow publishes
   eight binaries (both commands for each OS/architecture), a source archive,
   SHA256SUMS and provenance.sigstore.json.
4. Verify the published binary yourself:

   ```sh
   gh attestation verify hyrule_v0.X.Y_linux_amd64 --repo Gui-Rondon/hyrule \
     --signer-workflow Gui-Rondon/hyrule/.github/workflows/release.yml
   ```

   Also verify the selected `hyrule-serve_v0.X.Y_OS_ARCH` artifact. Both commands,
   the source tarball and SHA256SUMS are attestation subjects. For a strict check:

   ```sh
   gh attestation verify ARTIFACT --bundle provenance.sigstore.json \
     --repo Gui-Rondon/hyrule \
     --signer-workflow Gui-Rondon/hyrule/.github/workflows/release.yml \
     --source-digest EXPECTED_COMMIT_SHA \
     --predicate-type https://slsa.dev/provenance/v1
   ```

   Inspect the source commit and workflow identity. A changed byte must cause
   verification to fail. Download SHA256SUMS and verify your selected artifact.
5. The workflow requests the exact module version from the public Go proxy.
   Visit `https://pkg.go.dev/github.com/Gui-Rondon/hyrule/app@v0.X.Y` and inspect
   Overview and Examples. Use Request if needed. Stable-version status remains
   unset until v1; it is not a publication failure.

GitHub Artifact Attestations generate signed SLSA provenance for the listed files.
This is not a claim of a certified SLSA level. The source tarball is not the Go
proxy module zip; CLI provenance does not automatically attest the module zip.
Consumers still use the Go checksum database and immutable version identity.

## Retry and propagation

Never move/reuse a published tag. Failures before release publication can be
rerun for the same commit/tag; an existing tag targeting another commit fails.
Draft assets may be replaced on retry; published releases are not overwritten.
After publication, use **Index release** to retry proxy indexing, including for
v0.102.1. Download uses `GOPROXY=https://proxy.golang.org` and does not edit go.mod.
Indexing has bounded retries; pkg.go.dev/deps.dev propagation may remain pending.
No pseudo-version is retracted just to remove it from a listing.

## External evidence

Record the date, version and source commit when checking:

```sh
curl --fail 'https://api.deps.dev/v3/systems/go/packages/github.com%2FGui-Rondon%2Fhyrule/versions/v0.102.1'
curl --fail 'https://api.deps.dev/v3/projects/github.com%2FGui-Rondon%2Fhyrule'
```

Inspect scorecard.date, repository.commit and individual check reasons, not only
an aggregate number. Missing data means unavailable, not insecure or secure.
The deps.dev slsaProvenances field is npm-only; for Go inspect attestations when
available and verify published assets directly. No deps.dev ingestion SLA is promised.

## GitHub activation after merge (separate administrative step)

- Enable private vulnerability reporting and confirm SECURITY.md's private URL.
- Enable code scanning advanced setup (the versioned CodeQL workflow); avoid a
  simultaneous duplicate default-setup configuration.
- Run the Test workflow, then protect main with required checks **Go 1.26.x
  integration**, **Go 1.27.x integration**, **Documentation EN and PT**,
  **Windows SQLite and CLI**, **CodeQL Go**, and **Go vulnerabilities**, once
  those exact check names have appeared successfully. Require the branch to be
  up to date before merging. Bind required checks to the GitHub Actions app where
  available. Website security regressions and npm audit run inside the existing
  documentation check, without another build job.
- Require PRs and resolved conversations; block force pushes/deletion, including
  administrators. With one maintainer, require zero independent approvals:
  Gui-Rondon reviews and merges, and cannot approve their own PR. Revisit this
  when new maintainers join. Do not describe self-review as independent review.
- Give default workflow tokens read-only permissions. Jobs needing Pages,
  security reports, releases or attestations declare their own narrowly scoped writes.
- Check `gh api repos/Gui-Rondon/hyrule/branches/main/protection` and active rulesets.
  GitHub settings are separate from files in a PR; do not claim activation merely
  because workflow YAML exists. This PR does not enable protection before its
  checks are available and passing.
