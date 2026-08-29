#!/usr/bin/env bash
# Module 1 platform: the Redis work queue and KEDA.
#
# KEDA is installed up front rather than as part of a scenario's fix so that
# the debugging loop stays fast. What the scenarios actually teach is the
# ScaledObject — the piece you write — not `helm install`.
set -euo pipefail
source "$LAB_ROOT/lib/log.sh"
source "$LAB_ROOT/lib/guard.sh"
source "$LAB_ROOT/lib/wait.sh"
guard::require_lab_context

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

log::info "redis"
kubectl apply -f "$HERE/base/redis.yaml" >/dev/null
wait::rollout ml redis 120 >/dev/null

if kubectl get namespace keda >/dev/null 2>&1 && helm status keda -n keda >/dev/null 2>&1; then
  log::dim "keda already installed"
else
  log::info "keda (this takes a minute)"
  helm repo add kedacore https://kedacore.github.io/charts >/dev/null 2>&1 || true
  helm repo update kedacore >/dev/null 2>&1 || true
  helm upgrade --install keda kedacore/keda \
    --namespace keda --create-namespace \
    --wait --timeout 5m >/dev/null
fi

# The metrics adapter is the piece that serves external.metrics.k8s.io. If it
# is not Available, ScaledObjects appear healthy but no scaling ever happens.
wait::until 180 "KEDA metrics adapter" \
  kubectl -n keda rollout status deployment/keda-operator-metrics-apiserver --timeout=10s
log::ok "keda ready"

