#!/usr/bin/env bash
# The shipped fix, for when you want to compare or move on.
set -euo pipefail
source "$LAB_ROOT/lib/log.sh"
source "$LAB_ROOT/lib/guard.sh"
source "$LAB_ROOT/lib/wait.sh"
guard::require_lab_context

# The CPU HPA has to go first. KEDA creates and owns its own HPA for the same
# Deployment, and two autoscalers pointing at one workload fight: each computes
# a replica count from its own metric and the larger one wins every cycle.
kubectl -n ml delete hpa model-worker --ignore-not-found >/dev/null

kubectl apply -f "$SCENARIO_DIR/solution/scaledobject.yaml" >/dev/null

wait::until 90 "KEDA to take ownership of model-worker" \
  kubectl -n ml get hpa keda-hpa-model-worker
log::ok "ScaledObject active; KEDA now manages model-worker"
