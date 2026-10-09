#!/usr/bin/env bash
# Small test deployment: Render stops API and worker together when it sleeps.
set -euo pipefail
export SPACE_HTTP_ADDR=":${PORT:-10000}"
export SPACE_WORKER_CONCURRENCY="${SPACE_WORKER_CONCURRENCY:-1}"
export SPACE_METRICS_ADDR="${SPACE_METRICS_ADDR:-127.0.0.1:9091}"
bin_dir="${SPACE_BIN_DIR:-/}"
api_pid=""
worker_pid=""
shutdown() {
  trap - TERM INT
  for pid in "$api_pid" "$worker_pid"; do
    if [[ -n "$pid" ]]; then kill -TERM "$pid" 2>/dev/null || true; fi
  done
  for pid in "$api_pid" "$worker_pid"; do
    if [[ -n "$pid" ]]; then wait "$pid" 2>/dev/null || true; fi
  done
}
trap 'shutdown; exit 143' TERM INT
"${bin_dir%/}/api" &
api_pid=$!
"${bin_dir%/}/worker" &
worker_pid=$!
# Restart the whole container if either child fails; never leave a healthy API
# silently serving upload confirmations while its processing worker is dead.
set +e
wait -n "$api_pid" "$worker_pid"
status=$?
shutdown
if [[ "$status" == 0 ]]; then status=1; fi
exit "$status"
