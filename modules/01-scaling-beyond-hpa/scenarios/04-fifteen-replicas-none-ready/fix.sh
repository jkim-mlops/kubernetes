#!/usr/bin/env bash
# The shipped fix, for when you want to compare or move on.
set -euo pipefail
source "$LAB_ROOT/lib/log.sh"
source "$LAB_ROOT/lib/guard.sh"
source "$LAB_ROOT/lib/wait.sh"
guard::require_lab_context

# Size the container for what it actually holds. Nothing about the autoscaler,
# the queue or the model changes -- the workload was always this big, and the
# manifest was always wrong about it.
kubectl -n ml patch deployment model-worker \
  --patch-file "$SCENARIO_DIR/solution/worker-resources.yaml" >/dev/null

log::ok "model-worker is now sized for the model it loads (Guaranteed QoS)"
