#!/usr/bin/env bash
# Prints where the end-to-end tests and the scenario find a checkout's
# stack, as variable assignments, one per line, for `env`. Without them,
# both skip, as in `make test`. Both `make e2e` and the scenario targets
# read them here, so a stack is found the same way by each.
#
# Usage: scripts/stack-env.sh <checkout>
#
# The checkout's .env, written by `make up`, has its ports. HOST_ENDPOINT_IP,
# from the environment, is the host address the fake Endpoints listen on,
# the one host.docker.internal reaches from a container (default
# 127.0.0.1). It fails, naming the variable, if .env lacks a port.
set -euo pipefail

dir=$(cd "${1:?usage: stack-env.sh <checkout>}" && pwd)

# port prints the value of variable $1 in the checkout's .env.
port() {
  local p
  p=$(sed -n "s/^$1=//p" "$dir/.env" 2>/dev/null)
  test -n "$p" || { echo "stack-env: $dir/.env has no $1: run 'make up' there" >&2; exit 1; }
  echo "$p"
}

api=$(port HERALD_API_HTTP_PORT)
postgres=$(port HERALD_POSTGRES_PORT)
otlp=$(port HERALD_OTLP_HTTP_PORT)
clickhouse=$(port HERALD_CLICKHOUSE_HTTP_PORT)
api_pprof=$(port HERALD_API_PPROF_PORT)
dispatcher_pprof=$(port HERALD_DISPATCHER_PPROF_PORT)

echo "HERALD_E2E_API_URL=http://localhost:$api"
echo "HERALD_E2E_DATABASE_URL=postgres://herald:herald@localhost:$postgres/herald?sslmode=disable"
echo "HERALD_E2E_ENDPOINT_IP=${HOST_ENDPOINT_IP:-127.0.0.1}"
echo "HERALD_E2E_OTLP_URL=http://localhost:$otlp"
echo "HERALD_E2E_CLICKHOUSE_URL=http://herald:herald@localhost:$clickhouse"
echo "HERALD_E2E_API_PPROF_URL=http://localhost:$api_pprof"
echo "HERALD_E2E_DISPATCHER_PPROF_URL=http://localhost:$dispatcher_pprof"
# The scenario injects its faults through the checkout's compose file.
echo "HERALD_SCENARIO_COMPOSE_FILE=$dir/compose.yaml"
