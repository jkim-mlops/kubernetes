#!/usr/bin/env bash
# Loads and installs the fake GPU device plugin, then waits until every GPU node
# actually advertises the resource. A DaemonSet that is Running but failed to
# register with the kubelet looks perfectly healthy and schedules nothing, so
# "the pods are up" is not a sufficient check.
set -euo pipefail
source "$LAB_ROOT/lib/log.sh"
source "$LAB_ROOT/lib/guard.sh"
source "$LAB_ROOT/lib/wait.sh"
guard::require_lab_context

IMAGE="k8slab/fake-gpu-device-plugin:dev"
GPUS_PER_NODE=2
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ -z "$(docker images -q "$IMAGE" 2>/dev/null)" ]]; then
  log::warn "$IMAGE not built — run 'task install'; skipping GPU plugin"
  exit 0
fi

kind load docker-image "$IMAGE" --name "${LAB_CLUSTER:?LAB_CLUSTER not set}" >/dev/null
kubectl apply -f "$HERE/daemonset.yaml" >/dev/null
kubectl -n kube-system rollout status daemonset/fake-gpu-device-plugin --timeout=120s >/dev/null

gpu_total() {
  kubectl get nodes -o jsonpath='{range .items[*]}{.status.capacity.nvidia\.com/gpu}{"\n"}{end}' |
    awk '{ s += $1 } END { print s+0 }'
}

nodes="$(kubectl get nodes -l k8slab.io/gpu=true -o name | wc -l | tr -d ' ')"
expected=$(( nodes * GPUS_PER_NODE ))

# Registration is per-node and asynchronous, so wait for the full expected
# total rather than for the first node to report.
all_advertised() { [[ "$(gpu_total)" -ge "$expected" ]]; }
wait::until 120 "all $nodes GPU nodes to advertise nvidia.com/gpu (expecting $expected)" all_advertised

log::ok "cluster advertises $(gpu_total) nvidia.com/gpu across $nodes nodes"
