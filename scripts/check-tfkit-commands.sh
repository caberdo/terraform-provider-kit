#!/usr/bin/env bash
# Verifies that the tfkit release the reusable workflows install exposes every
# command those workflows call, and — the part that matters for the release
# gate — that its `release check` behaves. The version is passed in so a
# failing release is reported against the version that shipped it.
#
# Wording is not a contract: v0.12.0's help already mentioned the changelog and
# the last git tag while its check still failed every release pull request. So
# the guard builds a throwaway repository in the release-pull-request state
# (changelog ahead of an annotated tag) and requires exit 0 with a
# `pending release` line.
#
# tfkit prints its command list for --help and exits 2 (it has no --help
# subcommand), so the output is captured rather than treated as a failure.
#
# Usage: scripts/check-tfkit-commands.sh <tfkit-binary> <version>
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: $0 <tfkit-binary> <version>" >&2
  exit 2
fi

version=$2

# Resolve the binary before the fixture's `cd`, so a relative path still works.
tfkit=$1
case "$tfkit" in
  */*) tfkit="$(cd "$(dirname "$tfkit")" && pwd)/$(basename "$tfkit")" ;;
  *) tfkit="$(command -v "$tfkit" || true)" ;;
esac
if [ -z "$tfkit" ] || [ ! -x "$tfkit" ]; then
  echo "tfkit $version: no executable at '$1'" >&2
  exit 2
fi

help=$("$tfkit" --help 2>&1 || true)

missing=0
for command in coverage examples branch-protection release scaffold; do
  if ! grep -qE "^  ${command}([[:space:]]|$)" <<<"$help"; then
    echo "tfkit $version --help does not list the '$command' command" >&2
    missing=1
  fi
done
if [ "$missing" -ne 0 ]; then
  echo "tfkit --help said:" >&2
  printf '%s\n' "$help" >&2
  exit "$missing"
fi

fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT
repo="$fixture/repo"
mkdir -p "$repo"

cat >"$repo/CHANGELOG.md" <<'EOF'
# Changelog

## Unreleased

## [1.3.0] - 2026-11-16

- A staged release.
EOF

cat >"$repo/README.md" <<'EOF'
# terraform-provider-fixture

```hcl
terraform {
  required_providers {
    fixture = {
      version = "~> 1.3"
    }
  }
}
```
EOF

git_env=(
  "GIT_AUTHOR_NAME=Fixture"
  "GIT_AUTHOR_EMAIL=fixture@example.com"
  "GIT_COMMITTER_NAME=Fixture"
  "GIT_COMMITTER_EMAIL=fixture@example.com"
  "GIT_EDITOR=true"
)
(
  cd "$repo"
  git -c init.defaultBranch=main init -q
  git -c commit.gpgsign=false add CHANGELOG.md README.md
  env "${git_env[@]}" git -c commit.gpgsign=false commit -q -m fixture
  env "${git_env[@]}" git -c tag.gpgsign=false tag -a v1.2.3 -m "release 1.2.3"
)

set +e
output=$(cd "$repo" && "$tfkit" release check 2>&1)
status=$?
set -e

failed=0
if [ "$status" -ne 0 ]; then
  echo "tfkit $version release check exited $status on a release-pull-request fixture; the pin still validates the changelog against the last tag" >&2
  failed=1
fi
if ! grep -qF 'pending release' <<<"$output"; then
  echo "tfkit $version release check did not report 'pending release' for a changelog ahead of the tag" >&2
  failed=1
fi
if [ "$failed" -ne 0 ]; then
  echo "tfkit release check said:" >&2
  printf '%s\n' "$output" >&2
  exit 1
fi

exit 0
