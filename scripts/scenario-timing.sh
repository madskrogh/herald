#!/usr/bin/env bash
# Runs the scenario against two builds of Herald at the same time, and
# compares the candidate's timing statistics with the base's
# (docs/testing.md, "Timing margins"). The candidate's run is also its
# behaviour comparison and its invariants, as in `make scenario-compare`.
# One report holds all three.
#
# Run through `make scenario-timing`, which points it at this checkout's
# stack (the candidate) and BASE's. Both stacks must be up (make up), and
# nothing else may use them meanwhile. Both runs are this checkout's
# scenario, from this checkout's traffic file; only the build under it
# differs.
#
# Settings, from the environment:
#
#   BASE      the checkout whose stack is the base build (required)
#   BASE_LABEL, CANDIDATE_LABEL
#             what each build is, such as its commit (default: base,
#             candidate)
#   HOST_ENDPOINT_IP
#             the host address the fake Endpoints listen on, the one
#             host.docker.internal reaches (default 127.0.0.1)
#   OUT       directory to write to (default scenario-timing/<UTC time>)
#   PROFILE_DURATION
#             seconds to profile both builds for, from the start of the
#             runs (default 60). A profile can't span the dispatcher's
#             restart, which ends its connection, so it must end within
#             the traffic file's first phases. With the committed traffic
#             file, the first phase, which has no fault and is where the
#             timing statistics come from, takes about 95 s, at least the
#             retry schedule's 72 s at the local stack's retry scale. A
#             profile that fails is reported, and doesn't fail the run.
#
# It writes:
#
#   OUT/report.md                 the behaviour comparison, the
#                                 invariants and the timing comparison,
#                                 written whatever the runs found; when
#                                 the timing fails, with the profile diff
#   OUT/<build>/test.log          each run's test output
#   OUT/<build>/statistics.json   each run's statistics
#   OUT/candidate/result.json     what the candidate's run found, for the
#                                 report
#   OUT/candidate/report.md       the candidate's report without the
#                                 timing, as make scenario-compare writes it
#   OUT/<build>/spans.parquet     every span in each build's ClickHouse
#   OUT/profiles/                 scripts/profile.sh's profiles and diff
#
# It fails if the candidate's run fails (a baseline difference or a broken
# invariant), if the base's run fails, or if a statistic is beyond its
# margin.
set -euo pipefail

: "${BASE:?}"
out=${OUT:-scenario-timing/$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$out"
out=$(cd "$out" && pwd)
base=$(cd "$BASE" && pwd)
here=$(pwd)
profile_duration=${PROFILE_DURATION:-60}

# Where each stack is, read before anything starts, so that a stack that
# isn't up stops the script here, naming what's missing.
base_env=$(./scripts/stack-env.sh "$base")
candidate_env=$(./scripts/stack-env.sh "$here")

# setting prints the value of variable $2 in $1, a stack's settings.
setting() {
  sed -n "s/^$2=//p" <<<"$1"
}

# pprof prints the host:port of a stack's pprof, from its URL in variable
# $2 of the stack's settings $1.
pprof() {
  setting "$1" "$2" | sed 's|^http://||'
}

# run_scenario runs the scenario against the stack whose settings are $1,
# with the test binary's further arguments, in the background, writing to
# OUT/$2.
run_scenario() {
  local settings=$1 build=$2
  shift 2
  mkdir -p "$out/$build"
  # The settings are one assignment per line, with no spaces in them.
  # shellcheck disable=SC2086
  (cd "$here/internal/scenario" && env $settings \
    "$out/scenario.test" -test.run '^TestScenario$' -test.count 1 -test.v -test.timeout 20m \
    -out "$out/$build" "$@" >"$out/$build/test.log" 2>&1) &
}

# export_spans saves every span in the ClickHouse of the stack whose
# settings are $1 to OUT/$2/spans.parquet, for SQL over both runs.
export_spans() {
  curl --fail --silent --show-error -o "$out/$2/spans.parquet" \
    "$(setting "$1" HERALD_E2E_CLICKHOUSE_URL)/" \
    --data-binary 'SELECT * FROM otel.otel_traces FORMAT Parquet' ||
    echo "scenario-timing: couldn't export $2's spans" >&2
}

# Both runs use one test binary, built first, so neither waits on the
# compiler while the other runs.
go test -race -c -o "$out/scenario.test" ./internal/scenario/
go build -o "$out/scenario-timing" ./cmd/scenario-timing

echo "scenario-timing: running the scenario on both builds into $out"
run_scenario "$base_env" base -base
base_pid=$!
run_scenario "$candidate_env" candidate
candidate_pid=$!
CANDIDATE_API=$(pprof "$candidate_env" HERALD_E2E_API_PPROF_URL) \
  CANDIDATE_DISPATCHER=$(pprof "$candidate_env" HERALD_E2E_DISPATCHER_PPROF_URL) \
  BASE_API=$(pprof "$base_env" HERALD_E2E_API_PPROF_URL) \
  BASE_DISPATCHER=$(pprof "$base_env" HERALD_E2E_DISPATCHER_PPROF_URL) \
  DURATION="$profile_duration" OUT="$out/profiles" \
  "$here/scripts/profile.sh" >"$out/profiles.log" 2>&1 &
profile_pid=$!

failed=0
wait "$base_pid" || { echo "scenario-timing: the base's run failed: $out/base/test.log" >&2; failed=1; }
wait "$candidate_pid" || { echo "scenario-timing: the candidate's run failed: $out/candidate/test.log" >&2; failed=1; }
profiled=1
wait "$profile_pid" || { echo "scenario-timing: profiling failed: $out/profiles.log" >&2; profiled=0; }
export_spans "$base_env" base
export_spans "$candidate_env" candidate

# scenario-timing writes the report whatever the runs found. It exits 1
# when a statistic is beyond its margin, and 2 when it can't compare, as
# when a run wrote no statistics; the report then says why.
timing=0
"$out/scenario-timing" -base "$out/base" -candidate "$out/candidate" \
  -base-label "${BASE_LABEL:-base}" -candidate-label "${CANDIDATE_LABEL:-candidate}" \
  >"$out/report.md" 2>"$out/timing.log" || timing=$?
{
  echo
  echo "Every span in each build's ClickHouse is in base/spans.parquet and candidate/spans.parquet beside this report, for SQL over both runs:"
  echo
  echo '```'
  echo "clickhouse local -q \"SELECT SpanName, SpanAttributes['herald.outcome'] AS outcome, count() FROM file('candidate/spans.parquet') GROUP BY ALL\""
  echo '```'
  if ((timing == 1)); then
    echo
    echo "## Profiles"
    echo
    if ((profiled)); then
      echo "How the candidate's profiles differ from the base's, over the first ${profile_duration}s of both runs (scripts/profile.sh). The profiles and traces are in profiles/."
      echo
      echo '```'
      cat "$out/profiles/diff.txt"
      echo '```'
    else
      echo "Profiling failed: profiles.log says why."
    fi
  fi
} >>"$out/report.md"
cat "$out/report.md"

if ((timing)); then
  cat "$out/timing.log" >&2
  failed=1
fi
exit "$failed"
