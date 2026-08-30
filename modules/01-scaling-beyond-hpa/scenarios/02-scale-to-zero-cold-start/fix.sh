#!/usr/bin/env bash
# The shipped fix, for when you want to compare or move on.
set -euo pipefail
source "$LAB_ROOT/lib/log.sh"
source "$LAB_ROOT/lib/guard.sh"
source "$LAB_ROOT/lib/wait.sh"
guard::require_lab_context

# Half the fix is in the autoscaler: notice sooner, and stop paying sooner.
kubectl apply -f "$SCENARIO_DIR/solution/scaledobject.yaml" >/dev/null

# The other half is not in the autoscaler at all. No polling interval can make
# a 20-second model load fit inside a 20-second request timeout, so the gateway
# has to be willing to wait out a cold start.
kubectl -n ml set env deployment/inference-gateway REQUEST_TIMEOUT=90s >/dev/null
wait::rollout ml inference-gateway 180 >/dev/null

log::ok "polling interval, cooldown and request timeout now budget for a cold start"
