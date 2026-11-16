#!/usr/bin/env bash
# Verifies that the tfkit release the reusable workflows install exposes every
# command those workflows call. The version is passed in so a missing command is
# reported against the release that lacks it.
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

tfkit=$1
version=$2
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
fi

# The release check's behaviour is part of the contract: a pin whose help does
# not mention that a version file is optional and that the last git tag is what
# the check reports predates the changelog-based default. Every provider on that
# pin would still fail a release pull request against the last tag.
release_help=$("$tfkit" release check --help 2>&1 || true)
for phrase in '--version-file' 'last git tag'; do
  if ! grep -qF -- "$phrase" <<<"$release_help"; then
    echo "tfkit $version release check --help does not mention '$phrase'; the pin predates the changelog-based release check" >&2
    missing=1
  fi
done
if ! grep -qi 'changelog' <<<"$release_help"; then
  echo "tfkit $version release check --help does not mention the changelog; the pin predates the changelog-based release check" >&2
  missing=1
fi
if [ "$missing" -ne 0 ] && [ -n "${release_help:-}" ]; then
  echo "tfkit release check --help said:" >&2
  printf '%s\n' "$release_help" >&2
fi
exit "$missing"
