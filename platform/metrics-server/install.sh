#!/usr/bin/env bash
# metrics-server — without it there is no `kubectl top`, and an HPA with a CPU
# target reports <unknown> forever. Module 1 needs to show a *real* CPU number
# sitting well below target while the queue backs up, so this is mandatory.
set -euo pipefail
source "$LAB_ROOT/lib/log.sh"
source "$LAB_ROOT/lib/guard.sh"
source "$LAB_ROOT/lib/wait.sh"
guard::require_lab_context

VERSION="v0.7.2"

if kubectl -n kube-system get deployment metrics-server >/dev/null 2>&1; then
  log::dim "metrics-server already installed"
else
  kubectl apply -f "https://github.com/kubernetes-sigs/metrics-server/releases/download/${VERSION}/components.yaml" >/dev/null

  # kind nodes serve the kubelet API with a self-signed certificate that is not
  # in the cluster CA, so metrics-server cannot verify it. This flag is correct
  # for a local lab and wrong for anything real.
  kubectl -n kube-system patch deployment metrics-server --type=json \
    -p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]' >/dev/null
fi

wait::rollout kube-system metrics-server 180 >/dev/null
wait::until 120 "metrics-server to publish node metrics" kubectl top nodes
log::ok "metrics-server ready"
