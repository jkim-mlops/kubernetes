#!/usr/bin/env bash
# The shipped fix, for when you want to compare or move on.
set -euo pipefail
source "$LAB_ROOT/lib/log.sh"
source "$LAB_ROOT/lib/guard.sh"
source "$LAB_ROOT/lib/wait.sh"
guard::require_lab_context

# The only changes are time constants, and there are two of them because the
# scale-down passes through two controllers: KEDA's cooldownPeriod for N->0 and
# the HPA's stabilization window for N->1. Fixing either alone leaves the wave
# rebuilding its workers.
kubectl apply -f "$SCENARIO_DIR/solution/scaledobject.yaml" >/dev/null

wait::until 60 "KEDA to accept the updated ScaledObject" \
  kubectl -n ml get scaledobject model-worker
log::ok "both scale-down stages now outlast the gap between waves"
