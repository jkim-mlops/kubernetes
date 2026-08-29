# shellcheck shell=bash
# SAFETY GUARD.
#
# This lab exists to break things on purpose. The user's normal kubectl context
# points at a real EKS cluster, so two independent protections apply:
#
#   1. KUBECONFIG is pinned to $LAB_ROOT/.lab/kubeconfig. ~/.kube/config is
#      never read or written, and `kubectl config use-context` inside the lab
#      cannot change the user's own current context.
#   2. Every mutating operation calls guard::require_lab_context, which refuses
#      to run unless the active context is a kind lab cluster.

LAB_ROOT="${LAB_ROOT:?LAB_ROOT must be set before sourcing guard.sh}"
LAB_KUBECONFIG="${LAB_KUBECONFIG:-$LAB_ROOT/.lab/kubeconfig}"
LAB_CLUSTER_PREFIX="k8slab-"
LAB_CONTEXT_PREFIX="kind-${LAB_CLUSTER_PREFIX}"

mkdir -p "$(dirname "$LAB_KUBECONFIG")"
export KUBECONFIG="$LAB_KUBECONFIG"

guard::current_context() {
  kubectl config current-context 2>/dev/null || true
}

guard::require_lab_context() {
  local ctx
  ctx="$(guard::current_context)"

  if [[ -z "$ctx" ]]; then
    log::die "No lab cluster is running (no context in $LAB_KUBECONFIG).
       Run 'task up' first."
  fi

  if [[ "$ctx" != "$LAB_CONTEXT_PREFIX"* ]]; then
    log::die "REFUSING TO RUN — this is not a lab cluster.
       Current context: $ctx
       Expected a context starting with: $LAB_CONTEXT_PREFIX
       KUBECONFIG in use: $KUBECONFIG"
  fi
}

# Cluster name for a module directory: 01-scaling-beyond-hpa -> k8slab-01.
# With no module (setup-only branches) the lab still gets a cluster of its own:
# k8slab-base. Either way the name keeps the kind- prefix the guard demands.
guard::cluster_for_module() {
  local module="${1:-base}"
  printf '%s%s\n' "$LAB_CLUSTER_PREFIX" "${module%%-*}"
}
