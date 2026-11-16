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

Every job's display name in the shared workflows is static (for example `ci / Generate Docs`), so
a required status check can name it literally; no input changes a job's reported context.

## Running tfkit

```bash
go run github.com/caberdo/terraform-provider-kit/cmd/tfkit@v0.4.0 --help
```

`go run` is fine for interactive use, but it flattens tfkit's exit codes (a child 2 or 3 becomes 1), so CI and the drift check invoke the installed binary instead.

## Adopting the scaffold check

The kit owns the canonical repository scaffold; `tfkit scaffold check` reports where a provider has drifted and `tfkit scaffold fix` repairs the files that are enforced. Adoption is deliberately staged so a repository can enable the check before it is clean.

1. See the repair before making it:

   ```bash
   tfkit scaffold fix --dry-run \
     --product Trino --year 2026 --copyright "Dennis de Jong" \
     --kit-version v0.11.0 --upstream trinodb/trino
   ```

   The dry run prints every path it would write and changes nothing. Only the enforced set is written: the community files, the editor/ignore/lint configuration and the issue templates. README prose and provider-specific files are never touched.

2. Apply the repair, then run the check to see what remains:

   ```bash
   tfkit scaffold fix …    # same flags as step 1
   tfkit scaffold check …  # exit 0 clean, 1 drift, 2 configuration problem
   ```

3. Commit the repaired files and enable the job in the provider's `test.yml` caller with report-only mode:

   ```yaml
   with:
     scaffold: true
     scaffold-strict: false
     scaffold-upstream: trinodb/trino   # owner/name of the upstream project
     # scaffold-license: Apache-2.0     # only when the default MIT is wrong
   ```

   The `scaffold` and `scaffold-strict` inputs belong to `provider-ci.yml`; `scaffold-upstream` is required when `scaffold: true`. In report-only mode the `Scaffold` job prints the drift and passes, so the check can land before the repository is clean.

4. Record each remaining deliberate deviation in `.github/scaffold-allow.txt`, one `path: reason` per line, with `#` comments and blank lines ignored:

   ```text
   # durable deviations; --allow on the command line is for one-off debugging only
   .editorconfig: the organisation's editor standard adds a section
   .github/ISSUE_TEMPLATE/bug_report.yml: the bug form is a YAML issue form here
   ```

   The reason is mandatory: a line without one (or a duplicate path) exits 2. The same parser reads the file and repeated `--allow` flags. An allowed path is never written by `fix`.

5. When `tfkit scaffold check` is clean, remove `scaffold-strict: false` (it defaults to `true`). Only then add `Scaffold` to the repository's ruleset: a job that is skipped satisfies a required check, so adding the opt-in job early would let a repository merge with the check never having run.

## Licence

MIT — see [LICENSE](LICENSE).
