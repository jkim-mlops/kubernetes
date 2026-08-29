#!/usr/bin/env bash
# Objective check: can the inference path actually serve its offered load?
#
# Background traffic is paused and a single measured run takes its place, so
# the numbers are reproducible. The run allows a warm-up window first: an
# autoscaler is allowed to take time to converge, it is just not allowed to
# never converge.
set -euo pipefail
source "$LAB_ROOT/lib/log.sh"
source "$LAB_ROOT/lib/guard.sh"
source "$LAB_ROOT/lib/wait.sh"
guard::require_lab_context

RPS=12
WARMUP=90s
DURATION=60s
MAX_P99_MS=4000
MAX_QUEUE=50

cleanup() {
  kubectl -n ml scale deployment loadgen --replicas=1 >/dev/null 2>&1 || true
}
trap cleanup EXIT

kubectl -n ml scale deployment loadgen --replicas=0 >/dev/null
kubectl -n ml delete job measure --ignore-not-found >/dev/null

# Start from an empty queue. The question is whether the system can serve its
# offered load, not whether it can dig out of however large a backlog happened
# to accumulate while you were debugging — that would make the result depend on
# how long you took.
backlog="$(kubectl -n ml exec deploy/redis -- redis-cli LLEN infer:queue 2>/dev/null | tr -d '\r')" || backlog=0
kubectl -n ml exec deploy/redis -- redis-cli DEL infer:queue >/dev/null 2>&1 || true

log::info "cleared a backlog of ${backlog} before measuring"
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
              value: 30s
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
YAML

if ! kubectl -n ml wait --for=condition=Complete job/measure --timeout=300s >/dev/null 2>&1; then
  kubectl -n ml logs job/measure --tail=20 2>/dev/null | sed 's/^/    /' || true
  log::verdict_fail "the measurement run did not finish"
  exit 1
fi

result="$(kubectl -n ml logs job/measure | grep '^RESULT ' | tail -1)" || true
[[ -n "$result" ]] || { log::verdict_fail "no RESULT line from the load generator"; exit 1; }

field() { sed -n "s/.*[[:space:]]$1=\([^ ]*\).*/\1/p" <<<"$result"; }
p99="$(field p99_ms)"; p50="$(field p50_ms)"
errors="$(field errors)"; timeouts="$(field timeouts)"; requests="$(field requests)"

queue="$(kubectl -n ml exec deploy/redis -- redis-cli LLEN infer:queue 2>/dev/null | tr -d '\r')" || queue=0
replicas="$(kubectl -n ml get deployment model-worker -o jsonpath='{.status.readyReplicas}')"

log::info "$(printf 'requests=%s  p50=%sms  p99=%sms  errors=%s  timeouts=%s' \
  "$requests" "$p50" "$p99" "$errors" "$timeouts")"
log::info "$(printf 'queue depth=%s  ready worker replicas=%s' "$queue" "${replicas:-0}")"

fail=0
if (( p99 > MAX_P99_MS )); then
  log::fail "p99 latency ${p99}ms exceeds the ${MAX_P99_MS}ms objective"
  fail=1
else
  log::ok "p99 latency ${p99}ms is within the ${MAX_P99_MS}ms objective"
fi

if (( errors > 0 )); then
  log::fail "$errors failed requests ($timeouts of them timeouts) — should be zero"
  fail=1
else
  log::ok "no failed requests"
fi

if (( queue > MAX_QUEUE )); then
  log::fail "queue depth $queue is above $MAX_QUEUE — work is arriving faster than it is served"
  fail=1
else
  log::ok "queue depth $queue is under control"
fi

if (( fail )); then
  log::verdict_fail "the inference path cannot serve ${RPS} rps"
  exit 1
fi
log::verdict_pass "the inference path serves ${RPS} rps within its latency objective"
