#!/usr/bin/env bash
# Profiles two builds of Herald at the same time, and prints how the
# candidate's profiles differ from the base's.
#
# Run through `make profile`, which points it at this checkout's stack (the
# candidate) and the main checkout's (the base). Send traffic to both while
# it runs: a diff means something only when both builds did the same work.
#
# Settings, from the environment:
#
#   CANDIDATE_API, CANDIDATE_DISPATCHER, BASE_API, BASE_DISPATCHER
#             host:port of each service's pprof endpoints (required)
#   BASE_LABEL, CANDIDATE_LABEL
#             what each build is, such as its commit, shown at the top of the
#             diffs (default: none)
#   DURATION  seconds to profile for (default 30)
#   OUT       directory to write to (default profiles/<UTC time>)
#
# For each build and service it writes, under OUT/<build>/<service>/:
#
#   cpu.pb.gz        CPU, over DURATION
#   allocs.pb.gz     allocations made during DURATION
#   heap.pb.gz       heap in use at the end, after a GC
#   goroutine.pb.gz  goroutines at the end
#   trace.out        the execution trace over DURATION, for `go tool trace`
#   net.pb, sync.pb, syscall.pb, sched.pb
#                    time blocked on the network, on synchronisation, in
#                    syscalls and waiting to be scheduled, from the trace
#
# and every diff, as text, to OUT/diff.txt, which it also prints.
set -euo pipefail

: "${CANDIDATE_API:?}" "${CANDIDATE_DISPATCHER:?}" "${BASE_API:?}" "${BASE_DISPATCHER:?}"
duration=${DURATION:-30}
if [[ ! "$duration" =~ ^[1-9][0-9]*$ ]]; then
  echo "profile: DURATION is $duration; want a whole number of seconds" >&2
  exit 1
fi
out=${OUT:-profiles/$(date -u +%Y%m%dT%H%M%SZ)}

builds=(base candidate)
services=(herald-api herald-dispatcher)
trace_profiles=(net sync syscall sched)

# addr prints the pprof address of service in build.
addr() {
  case "$1/$2" in
    base/herald-api) echo "$BASE_API" ;;
    base/herald-dispatcher) echo "$BASE_DISPATCHER" ;;
    candidate/herald-api) echo "$CANDIDATE_API" ;;
    candidate/herald-dispatcher) echo "$CANDIDATE_DISPATCHER" ;;
  esac
}

# fetch saves http://<address>/debug/pprof/<path> to file, and names the URL
# when it fails.
fetch() {
  curl --fail --silent --show-error --max-time $((duration + 30)) -o "$2" "http://$1/debug/pprof/$3" ||
    { echo "profile: GET http://$1/debug/pprof/$3 failed" >&2; return 1; }
}

# capture profiles one service of one build into dir: the profiles that span
# the window at the same time, then the snapshots at its end.
capture() {
  local address=$1 dir=$2
  local pids=() failed=0
  fetch "$address" "$dir/cpu.pb.gz" "profile?seconds=$duration" &
  pids+=($!)
  fetch "$address" "$dir/trace.out" "trace?seconds=$duration" &
  pids+=($!)
  fetch "$address" "$dir/allocs.pb.gz" "allocs?seconds=$duration" &
  pids+=($!)
  for pid in "${pids[@]}"; do
    wait "$pid" || failed=1
  done
  if ((failed)); then return 1; fi
  fetch "$address" "$dir/heap.pb.gz" "heap?gc=1"
  fetch "$address" "$dir/goroutine.pb.gz" "goroutine"
  for p in "${trace_profiles[@]}"; do
    go tool trace -pprof="$p" "$dir/trace.out" >"$dir/$p.pb"
  done
}

echo "profile: profiling both builds for ${duration}s into $out"
pids=()
for b in "${builds[@]}"; do
  for s in "${services[@]}"; do
    mkdir -p "$out/$b/$s"
    capture "$(addr "$b" "$s")" "$out/$b/$s" &
    pids+=($!)
  done
done
# Every capture finishes, or times out, before the first failure stops the
# run, so none is left running.
failed=0
for pid in "${pids[@]}"; do
  wait "$pid" || failed=1
done
if ((failed)); then
  echo "profile: a capture failed (above). Do both builds serve pprof at those addresses?" >&2
  exit 1
fi

# diff_profile prints the candidate's profile less the base's, top entries
# first. A diff that fails says so and doesn't stop the others.
diff_profile() {
  local s=$1 file=$2 title=$3
  shift 3
  echo "=== $s: $title (candidate minus base)"
  go tool pprof -top -nodecount=20 "$@" -diff_base "$out/base/$s/$file" "$out/candidate/$s/$file" 2>&1 ||
    echo "profile: this diff failed (above)"
  echo
}

# total prints the line of a profile's header that says how much it holds.
total() {
  go tool pprof -top -nodecount=1 "$@" 2>/dev/null | grep -m1 -E 'Total|total' || echo "(empty)"
}

{
  echo "base:      ${BASE_LABEL:-(unlabelled)}"
  echo "candidate: ${CANDIDATE_LABEL:-(unlabelled)}"
  echo
  for s in "${services[@]}"; do
    echo "=== $s: CPU samples (a thin profile is noise)"
    echo "base:      $(total "$out/base/$s/cpu.pb.gz")"
    echo "candidate: $(total "$out/candidate/$s/cpu.pb.gz")"
    echo
    diff_profile "$s" cpu.pb.gz "CPU"
    diff_profile "$s" allocs.pb.gz "bytes allocated" -sample_index=alloc_space
    diff_profile "$s" heap.pb.gz "heap in use" -sample_index=inuse_space
    diff_profile "$s" goroutine.pb.gz "goroutines"
    for p in "${trace_profiles[@]}"; do
      diff_profile "$s" "$p.pb" "time blocked: $p"
    done
  done
} >"$out/diff.txt"

cat "$out/diff.txt"
echo "profile: wrote $out; open a trace with 'go tool trace $out/<build>/<service>/trace.out'"
