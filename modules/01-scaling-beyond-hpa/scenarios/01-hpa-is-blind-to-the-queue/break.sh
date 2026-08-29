#!/usr/bin/env bash
# Puts the inference path into its "as designed" state: a CPU-target HPA in
# front of a workload whose CPU never moves. Nothing here is sabotage — this is
# what a reasonable person ships after learning HPA.
set -euo pipefail
source "$LAB_ROOT/lib/log.sh"
source "$LAB_ROOT/lib/guard.sh"
source "$LAB_ROOT/lib/wait.sh"
guard::require_lab_context

# Remove anything left over from a previous run of the fix.
kubectl -n ml delete scaledobject model-worker --ignore-not-found >/dev/null 2>&1 || true
kubectl -n ml apply -f "$SCENARIO_DIR/manifests" >/dev/null
kubectl -n ml scale deployment model-worker --replicas=1 >/dev/null

# Start from an empty queue so the backlog you see is one you watched build.
kubectl -n ml exec deploy/redis -- redis-cli DEL infer:queue >/dev/null 2>&1 || true

wait::rollout ml inference-gateway 180 >/dev/null
wait::rollout ml model-worker 180 >/dev/null
kubectl -n ml scale deployment loadgen --replicas=1 >/dev/null

log::info "traffic is running — letting the symptom develop (45s)"
sleep 45
