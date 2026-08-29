# shellcheck shell=bash
# Polling helpers. Scenario scripts use these instead of bare `sleep` so that
# timeouts produce a readable message rather than a mystery failure.

# wait::until <timeout-seconds> <description> <command...>
# Polls the command every 2s until it succeeds. Returns 1 on timeout.
wait::until() {
  local timeout="$1" desc="$2"; shift 2
  local deadline=$(( SECONDS + timeout ))
  while (( SECONDS < deadline )); do
    if "$@" >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  log::fail "timed out after ${timeout}s waiting for: $desc"
  return 1
}

# wait::rollout <namespace> <deployment> [timeout-seconds]
wait::rollout() {
  local ns="$1" name="$2" timeout="${3:-180}"
  kubectl -n "$ns" rollout status "deployment/$name" --timeout="${timeout}s"
}

# wait::pods_ready <namespace> <label-selector> [timeout-seconds]
wait::pods_ready() {
  local ns="$1" selector="$2" timeout="${3:-180}"
  kubectl -n "$ns" wait --for=condition=Ready pod \
    -l "$selector" --timeout="${timeout}s"
}
