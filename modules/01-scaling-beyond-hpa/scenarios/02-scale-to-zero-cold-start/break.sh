#!/usr/bin/env bash
# Puts the system into the state the night leaves behind: no traffic, an empty
# queue, and zero workers.
#
# There is no sabotage in this scenario. Everything here is the configuration a
# reasonable person arrives at after scenario 01, plus a model that takes time
# to load. The failure is what those defaults do when composed.
set -euo pipefail
source "$LAB_ROOT/lib/log.sh"
source "$LAB_ROOT/lib/guard.sh"
source "$LAB_ROOT/lib/wait.sh"
guard::require_lab_context

# Scenario 01 may have left its CPU HPA behind. Two autoscalers on one
# Deployment fight, and that is last scenario's lesson, not this one.
kubectl -n ml delete hpa model-worker --ignore-not-found >/dev/null 2>&1 || true

kubectl -n ml apply -f "$SCENARIO_DIR/manifests" >/dev/null

# No ambient traffic: the whole scenario is about what happens when it returns.
kubectl -n ml scale deployment loadgen --replicas=0 >/dev/null
kubectl -n ml exec deploy/redis -- redis-cli DEL infer:queue >/dev/null 2>&1 || true

wait::rollout ml inference-gateway 180 >/dev/null

# Wind the workers down to zero so the reader starts where the morning starts.
kubectl -n ml scale deployment model-worker --replicas=0 >/dev/null
wait::until 120 "model-worker to reach zero replicas" \
  bash -c '[[ "$(kubectl -n ml get deploy model-worker -o jsonpath="{.status.replicas}")" == "" || "$(kubectl -n ml get deploy model-worker -o jsonpath="{.status.replicas}")" == "0" ]]'

log::ok "system is idle: 0 workers, empty queue, no traffic"
