#!/usr/bin/env bash
# Objective check: is the offered load actually served, by a fleet that is
# actually running?
#
# Replica count is deliberately not the test. Fifteen replicas is what the
# broken system reports. The test is readiness plus served traffic.
set -euo pipefail
source "$LAB_ROOT/lib/log.sh"
source "$LAB_ROOT/lib/guard.sh"
source "$LAB_ROOT/lib/wait.sh"
guard::require_lab_context

RPS=6
WARMUP=60s
DURATION=60s
MAX_P99_MS=4000

cleanup() {
  kubectl -n ml delete job measure --ignore-not-found >/dev/null 2>&1 || true
  kubectl -n ml scale deployment loadgen --replicas=1 >/dev/null 2>&1 || true
}
trap cleanup EXIT

kubectl -n ml scale deployment loadgen --replicas=0 >/dev/null
kubectl -n ml delete job measure --ignore-not-found >/dev/null

backlog="$(kubectl -n ml exec deploy/redis -- redis-cli LLEN infer:queue 2>/dev/null | tr -d '\r')" || backlog=0
kubectl -n ml exec deploy/redis -- redis-cli DEL infer:queue >/dev/null 2>&1 || true
log::info "cleared a backlog of ${backlog:-0} before measuring"
log::info "measuring: ${RPS} rps, ${WARMUP} warm-up then ${DURATION} measured"

kubectl -n ml apply -f - >/dev/null <<YAML
apiVersion: batch/v1
kind: Job
metadata:
  name: measure
  namespace: ml
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: mlsim
          image: k8slab/mlsim:dev
          imagePullPolicy: IfNotPresent
          env:
            - name: MODE
              value: loadgen
            - name: TARGET_URL
              value: http://inference-gateway.ml.svc.cluster.local:8080/infer
            - name: RPS
              value: "$RPS"
            - name: WARMUP
              value: "$WARMUP"
            - name: DURATION
              value: "$DURATION"
            - name: REQUEST_TIMEOUT
              value: 120s
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
YAML

if ! kubectl -n ml wait --for=condition=Complete job/measure --timeout=420s >/dev/null 2>&1; then
  kubectl -n ml logs job/measure --tail=20 2>/dev/null | sed 's/^/    /' || true
  log::verdict_fail "the measurement run did not finish"
  exit 1
fi

result="$(kubectl -n ml logs job/measure | grep '^RESULT ' | tail -1)" || true
[[ -n "$result" ]] || { log::verdict_fail "no RESULT line from the load generator"; exit 1; }

field() { sed -n "s/.*[[:space:]]$1=\([^ ]*\).*/\1/p" <<<"$result"; }
requests="$(field requests)"; errors="$(field errors)"; timeouts="$(field timeouts)"
p50="$(field p50_ms)"; p99="$(field p99_ms)"

# Both halves of the READY column, and how hard the fleet has been restarting.
desired="$(kubectl -n ml get deploy model-worker -o jsonpath='{.status.replicas}')" || true
ready="$(kubectl -n ml get deploy model-worker -o jsonpath='{.status.readyReplicas}')" || true
desired="${desired:-0}"; ready="${ready:-0}"
restarts="$(kubectl -n ml get pods -l app=model-worker \
  -o jsonpath='{range .items[*]}{.status.containerStatuses[0].restartCount}{"\n"}{end}' 2>/dev/null \
  | awk '{s+=$1} END{print s+0}')" || restarts=0
oom="$(kubectl -n ml get pods -l app=model-worker \
  -o jsonpath='{range .items[*]}{.status.containerStatuses[0].lastState.terminated.reason}{"\n"}{end}' 2>/dev/null \
  | grep -c OOMKilled)" || oom=0

log::info "$(printf 'requests=%s  errors=%s  timeouts=%s  p50=%sms  p99=%sms' \
  "$requests" "$errors" "$timeouts" "$p50" "$p99")"
log::info "$(printf 'workers: %s ready of %s   restarts=%s   OOMKilled=%s' \
  "$ready" "$desired" "$restarts" "$oom")"

fail=0
if (( ready < 1 )); then
  log::fail "no worker is ready — $desired replicas is a count, not a capacity"
  fail=1
else
  log::ok "$ready of $desired workers are actually ready"
fi

if (( errors > 0 )); then
  log::fail "$errors of $requests requests failed ($timeouts timeouts)"
  fail=1
else
  log::ok "all $requests requests served"
fi

if (( p99 > MAX_P99_MS )); then
  log::fail "p99 ${p99}ms exceeds the ${MAX_P99_MS}ms objective"
  fail=1
else
  log::ok "p99 ${p99}ms is within the ${MAX_P99_MS}ms objective"
fi

if (( fail )); then
  log::verdict_fail "the fleet cannot serve its load"
  exit 1
fi
log::verdict_pass "load served by a fleet that is running rather than restarting"
