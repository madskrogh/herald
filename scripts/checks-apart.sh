#!/usr/bin/env bash
# Fails when the change since BASE changes a check file together with any
# other file, and names both sets: whoever changes behaviour must not also
# change what judges it (docs/testing.md, "Check changes"). A change that
# needs both, such as a feature that needs a new invariant, is two PRs: the
# check first, then the change.
#
# Usage: scripts/checks-apart.sh <base>
#
# The change is what HEAD changed since its merge-base with BASE, so a
# commit on BASE after the branch started isn't part of it. CI runs the
# base's copy of it on every PR with BASE=HEAD^1, the main the PR's merge
# commit was made from, so a PR is judged by the list before it.
#
# This is the one list of check files.
set -euo pipefail

base=${1:?usage: scripts/checks-apart.sh <base>}

# is_check_file says whether path $1 is a check file: one that holds a
# check, a margin, an invariant or a span attribute a check reads, or that
# decides whether they run.
is_check_file() {
  # In a case pattern, * matches / too: internal/e2e/*.sql is every .sql
  # file under internal/e2e, at any depth.
  case $1 in
    # The baselines, which a PR re-records with the change that moves
    # them (docs/testing.md, "The traffic file and the baseline").
    internal/scenario/testdata/baseline.json | \
      internal/scenario/testdata/counts.json | \
      internal/scenario/testdata/traffic.json) return 1 ;;
    # The scenario's checks, comparisons, margins and their tests.
    internal/scenario/*) return 0 ;;
    # The timing comparison and its report.
    cmd/scenario-timing/*) return 0 ;;
    # The queries the checks read spans and the final state with, and the
    # code sqlc generates from them. A renamed span attribute changes
    # them. The end-to-end tests' Go code isn't here: a feature adds its
    # end-to-end tests with its code.
    internal/e2e/*.sql | internal/e2e/e2edb/*) return 0 ;;
    # What runs the checks and the profiles, and this job.
    scripts/scenario-timing.sh | scripts/profile.sh | \
      scripts/checks-apart.sh | scripts/checks_apart_test.go) return 0 ;;
    # The margins, and what the checks are.
    docs/testing.md) return 0 ;;
    # The workflow, so a PR can't switch this job off.
    .github/workflows/ci.yml) return 0 ;;
  esac
  return 1
}

since=$(git merge-base "$base" HEAD 2>/dev/null) || {
  echo "checks-apart: no merge-base of HEAD and $base" >&2
  exit 2
}

checks=()
others=()
# Without rename detection, a file moved counts at its old path and its new.
# Paths are NUL-terminated and unquoted, so that each is matched as it is.
while IFS= read -r -d '' path; do
  if is_check_file "$path"; then
    checks+=("$path")
  else
    others+=("$path")
  fi
done < <(git -c core.quotePath=false diff -z --no-renames --name-only "$since" HEAD)

if ((${#checks[@]} == 0 || ${#others[@]} == 0)); then
  exit 0
fi

echo "checks-apart: this change changes check files together with other files."
echo "Change the checks in a PR of their own, then the rest (docs/testing.md, \"Check changes\")."
echo
echo "check files:"
printf '  %s\n' "${checks[@]}"
echo
echo "other files:"
printf '  %s\n' "${others[@]}"
exit 1
