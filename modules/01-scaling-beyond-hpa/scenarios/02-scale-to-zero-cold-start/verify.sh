#!/usr/bin/env bash
# Objective check: can a cold system serve the morning trickle, and does it go
# back to sleep afterwards?
#
# Note what is deliberately missing compared with scenario 01: there is no
# warm-up window. Scenario 01 gave the autoscaler time to converge before it
# started counting, because convergence time was not what it was testing. Here
# the cold start IS the thing under test, so the very first request counts.
set -euo pipefail
source "$LAB_ROOT/lib/log.sh"
source "$LAB_ROOT/lib/guard.sh"
source "$LAB_ROOT/lib/wait.sh"
guard::require_lab_context

RPS=1
DURATION=60s
MAX_MAX_MS=45000        # worst single request, cold start included
SCALEDOWN_WAIT=240      # how long we allow for the return to zero

cleanup() { kubectl -n ml delete job measure --ignore-not-found >/dev/null 2>&1 || true; }
trap cleanup EXIT

kubectl -n ml scale deployment loadgen --replicas=0 >/dev/null
kubectl -n ml delete job measure --ignore-not-found >/dev/null

# Start genuinely cold. KEDA owns the replica count, and with an empty queue it
# wants zero, so scaling to zero here is not cheating the test -- it is skipping
# the cooldown wait to get to the state the test is actually about. Whether
# cooldown works is asserted separately, at the end.
kubectl -n ml exec deploy/redis -- redis-cli DEL infer:queue >/dev/null 2>&1 || true
kubectl -n ml scale deployment model-worker --replicas=0 >/dev/null
if ! wait::until 120 "model-worker to be cold (0 replicas)" \
  bash -c 'r="$(kubectl -n ml get deploy model-worker -o jsonpath="{.status.replicas}")"; [[ -z "$r" || "$r" == "0" ]]'; then
  log::verdict_fail "could not reach a cold start state"
  exit 1
fi
log::info "cold: 0 workers, empty queue"
log::info "measuring: ${RPS} rps for ${DURATION}, starting from zero replicas"

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
              value: "0"
            - name: DURATION
              value: "$DURATION"
            # Well above any gateway timeout, so every failure recorded here is
            # the gateway's own 504 rather than the client losing patience.
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
p99="$(field p99_ms)"; max="$(field max_ms)"

# Traffic has only just stopped, so cooldown cannot have elapsed yet: whatever
# is up now is what the cold start actually produced.
scaled_to="$(kubectl -n ml get deploy model-worker -o jsonpath='{.status.readyReplicas}')" || true
scaled_to="${scaled_to:-0}"

log::info "$(printf 'requests=%s  errors=%s  timeouts=%s  p99=%sms  max=%sms' \
  "$requests" "$errors" "$timeouts" "$p99" "$max")"
log::info "$(printf 'workers ready at end of run=%s' "$scaled_to")"

fail=0
if (( errors > 0 )); then
  log::fail "$errors of $requests requests failed ($timeouts timeouts) — a cold start is not an outage"
  fail=1
else
  log::ok "all $requests requests served from a cold start"
fi

if (( max > MAX_MAX_MS )); then
  log::fail "slowest request ${max}ms exceeds the ${MAX_MAX_MS}ms objective"
  fail=1
else
  log::ok "slowest request ${max}ms is within the ${MAX_MAX_MS}ms objective"
fi

if (( scaled_to < 1 )); then
  log::fail "no workers were running at the end of the run — nothing scaled up"
  fail=1
else
  log::ok "scaled up to $scaled_to workers"
fi

# The other half of the bargain. Skipped when the run already failed, so the
# regression suite does not spend four minutes confirming a known failure.
if (( fail )); then
  log::dim "skipping the scale-to-zero check while the run above is failing"
else
  log::info "waiting up to ${SCALEDOWN_WAIT}s for the workers to go back to zero"
  if wait::until "$SCALEDOWN_WAIT" "model-worker to return to zero" \
    bash -c 'r="$(kubectl -n ml get deploy model-worker -o jsonpath="{.status.replicas}")"; [[ -z "$r" || "$r" == "0" ]]'; then
    log::ok "returned to zero replicas — the GPU is not being paid for while idle"
  else
    log::fail "still running workers after ${SCALEDOWN_WAIT}s idle — you are paying for an empty queue"
    fail=1
  fi
fi

if (( fail )); then
  log::verdict_fail "the inference path cannot absorb a cold start"
  exit 1
fi
log::verdict_pass "cold start absorbed: zero errors, scaled up, and back to zero"
