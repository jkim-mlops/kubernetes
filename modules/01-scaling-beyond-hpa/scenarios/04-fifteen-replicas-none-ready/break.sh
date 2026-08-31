#!/usr/bin/env bash
# Restores the system as scenarios 01-03 left it and applies the one change
# that came from outside: a bigger model. Then turns the traffic on and lets
# the symptom develop, so the reader arrives at a system already on fire.
set -euo pipefail
source "$LAB_ROOT/lib/log.sh"
source "$LAB_ROOT/lib/guard.sh"
source "$LAB_ROOT/lib/wait.sh"
guard::require_lab_context

kubectl -n ml delete hpa model-worker --ignore-not-found >/dev/null 2>&1 || true
kubectl -n ml apply -f "$SCENARIO_DIR/manifests" >/dev/null

kubectl -n ml scale deployment loadgen --replicas=0 >/dev/null
kubectl -n ml exec deploy/redis -- redis-cli DEL infer:queue >/dev/null 2>&1 || true
wait::rollout ml inference-gateway 180 >/dev/null

kubectl -n ml scale deployment model-worker --replicas=0 >/dev/null
wait::until 120 "model-worker to reach zero replicas" \
  bash -c 'r="$(kubectl -n ml get deploy model-worker -o jsonpath="{.status.replicas}")"; [[ -z "$r" || "$r" == "0" ]]'

kubectl -n ml scale deployment loadgen --replicas=1 >/dev/null
log::info "traffic is running — letting the symptom develop (90s)"
sleep 90
