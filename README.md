# terraform-provider-kit

Shared CI and tooling for the `caberdo/terraform-provider-*` repositories. The kit holds one
reusable `workflow_call` workflow per concern (CI, acceptance, release, scorecard, protection,
freshness, drift), the canonical configuration text those workflows and a drift check keep in step
across the providers (`.golangci.yml`, `.gitignore`, workflow caller shape, kit pin), and `tfkit` —
one Go CLI that replaces the per-repository shell scripts for coverage merge/gate/README rendering,
example validation, branch-protection check/setup and release checks. A provider repository becomes
a short caller of these workflows plus a `GNUmakefile` whose targets run `tfkit`; nothing in the kit
lives in a provider's own history.

## Using the kit from a provider

Replace the provider's `test.yml` with a caller whose job ids stay `ci` and `acceptance`:

```yaml
name: test
on:
  push: { branches: [main] }
  pull_request:
  merge_group:
permissions:
  contents: read
  security-events: write
jobs:
  ci:
    uses: caberdo/terraform-provider-kit/.github/workflows/provider-ci.yml@v0.1.0
    with:
      provider: trino
      module: github.com/caberdo/terraform-provider-trino
  acceptance:
    if: github.event_name == 'push' && github.ref == 'refs/heads/main'
    uses: caberdo/terraform-provider-kit/.github/workflows/provider-acceptance.yml@v0.1.0
    with:
      provider: trino
      versions-file: .github/trino-versions.txt
```

Pin the caller to a release tag (or a full commit SHA), never to `@main`: the required
status-check names derive from these job ids, and a drifting pin is what the kit's drift check
exists to catch.

## Running tfkit

```bash
go run github.com/caberdo/terraform-provider-kit/cmd/tfkit@v0.3.0 --help
```

`go run` is fine for interactive use, but it flattens tfkit's exit codes (a child 2 or 3 becomes 1), so CI and the drift check invoke the installed binary instead.

## Licence

MIT — see [LICENSE](LICENSE).
