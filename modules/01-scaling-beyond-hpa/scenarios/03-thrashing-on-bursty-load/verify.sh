#!/usr/bin/env bash
# Objective check: once the system is in its steady rhythm, can it serve a wave?
#
# The first wave of the day starts from nothing and pays a cold start no matter
# how well this is tuned, so measuring it would fail every configuration equally
# and teach nothing. The run therefore sends one full wave and one full lull
# unmeasured, and starts counting exactly as the second wave begins.
set -euo pipefail
source "$LAB_ROOT/lib/log.sh"
source "$LAB_ROOT/lib/guard.sh"
source "$LAB_ROOT/lib/wait.sh"
guard::require_lab_context

RPS=6
BURST_PERIOD=90s
WARMUP=180s            # wave 1 + lull 1, unmeasured
DURATION=90s           # wave 2, measured
MAX_P99_MS=4000

SAMPLES="$(mktemp)"
sampler_pid=""
cleanup() {
  [[ -n "$sampler_pid" ]] && kill "$sampler_pid" 2>/dev/null || true
  rm -f "$SAMPLES"
  kubectl -n ml delete job measure --ignore-not-found >/dev/null 2>&1 || true
}
trap cleanup EXIT

kubectl -n ml scale deployment loadgen --replicas=0 >/dev/null
kubectl -n ml delete job measure --ignore-not-found >/dev/null
kubectl -n ml exec deploy/redis -- redis-cli DEL infer:queue >/dev/null 2>&1 || true

# Both configurations start from the same cold system, so the only thing under
# test is what happens between the waves.
kubectl -n ml scale deployment model-worker --replicas=0 >/dev/null
if ! wait::until 120 "model-worker to be cold (0 replicas)" \
  bash -c 'r="$(kubectl -n ml get deploy model-worker -o jsonpath="{.status.replicas}")"; [[ -z "$r" || "$r" == "0" ]]'; then
  log::verdict_fail "could not reach a cold start state"
  exit 1
fi

log::info "waves: ${RPS} rps for ${BURST_PERIOD}, then silence, repeating"
log::info "measuring the second wave only (${WARMUP} unmeasured, then ${DURATION})"

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
            - name: PROFILE
              value: burst
            - name: BURST_PERIOD
              value: "$BURST_PERIOD"
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

# Record the replica count throughout, so the run reports the shape and not just
# the outcome. Whether the workers survived the lull is the whole mechanism.
# .status.replicas is omitted entirely when it is zero, so an unguarded
# jsonpath records a blank line for the one value this scenario cares about.
( while true; do
    n="$(kubectl -n ml get deploy model-worker -o jsonpath='{.status.replicas}' 2>/dev/null)"
    printf '%s\n' "${n:-0}" >> "$SAMPLES"
    sleep 5
  done ) &
sampler_pid=$!

if ! kubectl -n ml wait --for=condition=Complete job/measure --timeout=480s >/dev/null 2>&1; then
  kubectl -n ml logs job/measure --tail=20 2>/dev/null | sed 's/^/    /' || true
  log::verdict_fail "the measurement run did not finish"
  exit 1
fi

kill "$sampler_pid" 2>/dev/null || true
sampler_pid=""

result="$(kubectl -n ml logs job/measure | grep '^RESULT ' | tail -1)" || true
[[ -n "$result" ]] || { log::verdict_fail "no RESULT line from the load generator"; exit 1; }

field() { sed -n "s/.*[[:space:]]$1=\([^ ]*\).*/\1/p" <<<"$result"; }
requests="$(field requests)"; errors="$(field errors)"
p50="$(field p50_ms)"; p99="$(field p99_ms)"; max="$(field max_ms)"

# What the replica count did while nobody was looking. The run deliberately
# starts from zero, so only zeros seen AFTER the workers first appear are
# evidence of a teardown.
low="$(awk '$1>0{seen=1; next} seen&&$1==0{n++} END{print n+0}' "$SAMPLES")"
peak="$(sort -n "$SAMPLES" | tail -1)"; peak="${peak:-0}"

log::info "$(printf 'requests=%s  errors=%s  p50=%sms  p99=%sms  max=%sms' \
  "$requests" "$errors" "$p50" "$p99" "$max")"
log::info "$(printf 'workers: peak=%s   samples at zero after start-up=%s' "$peak" "$low")"
if (( low > 0 )); then
  log::dim "the workers were torn down mid-run and rebuilt for the next wave"
else
  log::dim "the workers survived the lull"
fi

fail=0
if (( p99 > MAX_P99_MS )); then
  log::fail "p99 ${p99}ms exceeds the ${MAX_P99_MS}ms objective — this wave paid for a rebuild"
  fail=1
else
  log::ok "p99 ${p99}ms is within the ${MAX_P99_MS}ms objective"
fi

if (( errors > 0 )); then
  log::fail "$errors failed requests — should be zero"
  fail=1
else
  log::ok "no failed requests"
fi

if (( fail )); then
  log::verdict_fail "every wave is paying a cold start"
  exit 1
fi
log::verdict_pass "a steady-state wave is served without rebuilding the workers"
