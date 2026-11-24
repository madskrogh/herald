#!/usr/bin/env bash
# Runs the scenario against two builds of Herald at the same time, and
# compares the candidate's timing statistics with the base's
# (docs/testing.md, "Timing margins"). The candidate's run is also its
# behaviour comparison and its invariants, as in `make scenario-compare`.
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
#             runs (default 90: the first phase, which has no fault and
#             is where the timing statistics come from. A profile can't
#             span the dispatcher's restart, which ends its connection)
#
# It writes:
#
#   OUT/report.md                 the comparison; when it fails, with the
#                                 profile diff
#   OUT/<build>/test.log          each run's test output
#   OUT/<build>/statistics.json   each run's statistics
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
ip=${HOST_ENDPOINT_IP:-127.0.0.1}
profile_duration=${PROFILE_DURATION:-90}

# port prints the value of variable $2 in checkout $1's .env.
port() {
  local p
  p=$(sed -n "s/^$2=//p" "$1/.env" 2>/dev/null)
  test -n "$p" || { echo "scenario-timing: $1/.env has no $2: run 'make up' there" >&2; exit 1; }
  echo "$p"
}

# run_scenario runs the scenario against checkout $1's stack, with the
# test binary's further arguments, in the background, writing to OUT/$2.
run_scenario() {
  local dir=$1 build=$2
  shift 2
  mkdir -p "$out/$build"
  (
    cd "$here/internal/scenario"
    env \
      HERALD_E2E_API_URL="http://localhost:$(port "$dir" HERALD_API_HTTP_PORT)" \
      HERALD_E2E_DATABASE_URL="postgres://herald:herald@localhost:$(port "$dir" HERALD_POSTGRES_PORT)/herald?sslmode=disable" \
      HERALD_E2E_ENDPOINT_IP="$ip" \
      HERALD_E2E_OTLP_URL="http://localhost:$(port "$dir" HERALD_OTLP_HTTP_PORT)" \
      HERALD_E2E_CLICKHOUSE_URL="http://herald:herald@localhost:$(port "$dir" HERALD_CLICKHOUSE_HTTP_PORT)" \
      HERALD_E2E_API_PPROF_URL="http://localhost:$(port "$dir" HERALD_API_PPROF_PORT)" \
      HERALD_E2E_DISPATCHER_PPROF_URL="http://localhost:$(port "$dir" HERALD_DISPATCHER_PPROF_PORT)" \
      HERALD_SCENARIO_COMPOSE_FILE="$dir/compose.yaml" \
      "$out/scenario.test" -test.run '^TestScenario$' -test.count 1 -test.v -test.timeout 20m \
      -out "$out/$build" "$@" >"$out/$build/test.log" 2>&1
  ) &
}

# export_spans saves every span in checkout $1's ClickHouse to
# OUT/$2/spans.parquet, for SQL over both runs.
export_spans() {
  curl --fail --silent --show-error -o "$out/$2/spans.parquet" \
    "http://herald:herald@localhost:$(port "$1" HERALD_CLICKHOUSE_HTTP_PORT)/" \
    --data-binary 'SELECT * FROM otel.otel_traces FORMAT Parquet' ||
    echo "scenario-timing: couldn't export $2's spans" >&2
}

# Both runs use one test binary, built first, so neither waits on the
# compiler while the other runs.
go test -race -c -o "$out/scenario.test" ./internal/scenario/

echo "scenario-timing: running the scenario on both builds into $out"
run_scenario "$base" base -base
base_pid=$!
run_scenario "$here" candidate
candidate_pid=$!
CANDIDATE_API="localhost:$(port "$here" HERALD_API_PPROF_PORT)" \
  CANDIDATE_DISPATCHER="localhost:$(port "$here" HERALD_DISPATCHER_PPROF_PORT)" \
  BASE_API="localhost:$(port "$base" HERALD_API_PPROF_PORT)" \
  BASE_DISPATCHER="localhost:$(port "$base" HERALD_DISPATCHER_PPROF_PORT)" \
  DURATION="$profile_duration" OUT="$out/profiles" \
  "$here/scripts/profile.sh" >"$out/profiles.log" 2>&1 &
profile_pid=$!

failed=0
wait "$base_pid" || { echo "scenario-timing: the base's run failed: $out/base/test.log" >&2; failed=1; }
wait "$candidate_pid" || { echo "scenario-timing: the candidate's run failed: $out/candidate/test.log" >&2; failed=1; }
profiled=1
wait "$profile_pid" || { echo "scenario-timing: profiling failed: $out/profiles.log" >&2; profiled=0; }
export_spans "$base" base
export_spans "$here" candidate

timing=0
go run ./cmd/scenario-timing -base "$out/base" -candidate "$out/candidate" \
  -base-label "${BASE_LABEL:-base}" -candidate-label "${CANDIDATE_LABEL:-candidate}" \
  >"$out/report.md" || timing=1
{
  echo
  echo "Each build's spans are in base/spans.parquet and candidate/spans.parquet beside this report, for SQL over both runs:"
  echo
  echo '```'
  echo "clickhouse local -q \"SELECT SpanName, SpanAttributes['herald.outcome'] AS outcome, count() FROM file('candidate/spans.parquet') GROUP BY ALL\""
  echo '```'
  if ((timing)); then
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
  echo "scenario-timing: the timing comparison failed (above)" >&2
  failed=1
fi
exit "$failed"
