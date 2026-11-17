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

1. **Commit the current state first.** `fix` replaces whole files, not individual lines. For trino it rewrites `SECURITY.md` (47 → 165 lines), `CONTRIBUTING.md` (261 → 215), both issue templates, and strips provider-specific comments from `.gitignore`, `.golangci.yml` and `.github/dependabot.yml`. A clean starting commit is the only way to review that change line by line and to restore a provider-specific answer deliberately.

2. **See the repair before making it:**

   ```bash
   tfkit scaffold fix --dry-run \
     --product Trino --year 2026 --copyright "Dennis de Jong" \
     --kit-version v0.13.0 --upstream trinodb/trino
   ```

   The dry run prints every path it would write and changes nothing. Only the enforced set is written: the community files, the editor/ignore/lint configuration and the issue templates. README prose, `AGENTS.md` and provider-specific files are never touched. A repository that already ships `.yml` issue forms is checked and repaired as `.yml`; `fix` does not add competing `.md` siblings.

3. **Apply the repair, then inspect every changed file:**

   ```bash
   tfkit scaffold fix …    # same flags as step 2
   git diff               # line by line, before committing
   tfkit scaffold check …  # exit 0 clean, 1 drift, 2 configuration problem
   ```

   Review the diff, not just the exit code. If a file's prose carries a provider-specific answer (a security contact, a support route, an authentication prompt), restore that answer and record the path in the allow list rather than accepting the generic text. A repository with substantial provider-specific prose should expect to allow-list roughly 8–11 of the 15 enforced paths.

   `CODE_OF_CONDUCT.md` names its enforcement contact with `{{CONDUCT_CONTACT}}`. The scaffold default is `conduct@caberdo.nl`; pass `--conduct-contact <address>` when adopting, or replace the address in the rendered file, so conduct reports reach the repository's own maintainers.

4. **Commit the result and enable the job in report-only mode.** The caller's `uses:` pin must move to the release that carries the `scaffold` job (v0.13.0 or later); an older pin has no such job:

   ```yaml
   jobs:
     ci:
       uses: caberdo/terraform-provider-kit/.github/workflows/provider-ci.yml@v0.13.0
       with:
         scaffold: true
         scaffold-strict: false
         scaffold-upstream: trinodb/trino   # owner/name of the upstream project
         # scaffold-license: Apache-2.0     # only when the default MIT is wrong
   ```

   `provider-display` is required when `scaffold: true`. In report-only mode the `Scaffold` job prints the drift and passes, so the check can land before the repository is clean.

5. **Record each accepted deviation in `.github/scaffold-allow.txt`**, one `path: reason` per line, with `#` comments and blank lines ignored:

   ```text
   # durable deviations; --allow on the command line is for one-off debugging only
   .editorconfig: the organisation's editor standard adds a section
   .golangci.yml: the repository keeps its own linter set
   ```

   The reason is mandatory: a line without one (or a duplicate path) exits 2. The same parser reads the file and repeated `--allow` flags. An allowed path is never written by `fix`. Only a path the checker enforces can be allow-listed; a path outside the enforced set is a configuration error, not an exemption.

6. **When `tfkit scaffold check` is clean, remove `scaffold-strict: false`** (it defaults to `true`). Only then add `Scaffold` to the repository's ruleset: a job that is skipped satisfies a required check, so adding the opt-in job early would let a repository merge with the check never having run.

## Releasing the kit

Tag the merge commit `vX.Y.Z` and push the tag. `TFKIT_VERSION` in
`.github/workflows/provider-ci.yml`, `provider-acceptance.yml` and
`provider-protection.yml` must equal that tag in the same commit: a reusable
workflow ships inside its own release, so it can install the CLI it carries. A
pin older than the tag ships the older behaviour to every caller of the tag —
v0.12.0 shipped the inverted release check exactly this way. The `tfkit Commands`
self-ci job installs the pinned release and proves its `release check` passes a
release pull request, so a stale pin is caught before the tag is published.

## Licence

MIT — see [LICENSE](LICENSE).

## Releasing the kit

The workflows must name a tag that only exists after the release, so the release being prepared lives
in `KIT_VERSION`. To cut a release:

1. Bump `KIT_VERSION` and `TFKIT_VERSION` in the three reusable workflows to the same tag `vX.Y.Z`.
   `TestProviderCIPinsItsOwnRelease` fails when they disagree.
2. Run `go test ./...` and `actionlint .github/workflows/*.yml`.
3. Commit, tag `vX.Y.Z`, push the commit and the tag.
4. Bump the five providers' `uses:` pins and their `GNUmakefile` `TFKIT ?=` to the new tag.

`scripts/check-tfkit-commands.sh` installs the pinned tag and exercises it, so a command added to the
CLI without a release that contains it fails self-ci rather than a provider's pipeline.
