# Solution — Scale to zero and the cold start

## What was actually happening

Nothing was misconfigured. Three independently reasonable numbers were composed
for the first time, and their sum was larger than any of them:

| Term | Value | Where it came from |
|---|---|---|
| KEDA notices the work | up to **30s** | `pollingInterval` default — nobody chose it |
| Pod scheduled and started | ~**3s** | image already cached on the node |
| Model loaded before it can serve | **20s** | `MODEL_LOAD_SECONDS`, a property of the workload |
| **Cold-start budget** | **up to ~53s** | |
| Gateway gives up | **20s** | `REQUEST_TIMEOUT`, chosen in scenario 01 |

The gateway abandoned the request before KEDA had even looked at the queue. Not
before the worker was ready — before the *autoscaler had an opinion*. Every
request in the first minute after idle failed, then the workers finished loading
and the system behaved perfectly, which is why it always looked fine by the time
anyone logged in.

The 20-second timeout was not wrong when it was chosen. In scenario 01 there was
always a worker already running, so the only thing a request ever waited for was
queue depth, and 20 seconds was a generous bound on an 800 ms inference. Dropping
`minReplicaCount` to zero silently added a new term to a sum nobody re-checked.

**This is the general shape.** Scale-to-zero does not remove cost, it converts a
*capacity* problem into a *latency* problem. The first request after idle pays
for every second of everything that has to happen before a worker exists.

## Why the primitives cannot fix this for you

There is no Kubernetes mechanism that makes a workload ready before it is ready.
A `startupProbe` changes when the kubelet decides a slow starter is *unhealthy*;
it does not make the model load faster. `HorizontalPodAutoscaler` cannot reach
zero at all, so it has nothing to say here. Even KEDA cannot help with the
largest of the three terms — 20 of the 53 seconds are the workload reading its
own weights, and no autoscaler can shorten that.

What you can do is exactly three things, and it is worth seeing that the list is
short and complete:

1. **Notice sooner** — shrink the polling gap.
2. **Pay it less often** — keep workers alive longer after the work stops.
3. **Be willing to wait** — let the request survive the cold start.

The third one is only available because the queue is durable. The job is already
in Redis the moment the gateway enqueues it, so nothing is lost while there are
no workers; the only thing that fails is the *synchronous contract* the gateway
offers its caller. Had this been an in-memory handoff or a pub/sub topic, the
work would be gone and no timeout could have saved it. **The queue is what makes
scale-to-zero survivable at all.**

## The fix

    kubectl apply -f solution/scaledobject.yaml
    kubectl -n ml set env deployment/inference-gateway REQUEST_TIMEOUT=90s

```yaml
spec:
  minReplicaCount: 0
  pollingInterval: 5     # was defaulting to 30
  cooldownPeriod: 60     # was defaulting to 300
```

Note that half the fix is not in the autoscaler. That is the part worth
remembering: **the cold-start budget spans components that no single manifest
owns.** The polling interval is KEDA's, the model load belongs to the workload,
and the timeout belongs to the caller — and only their sum is meaningful.

`cooldownPeriod` is the honest expression of the trade. At 300s you pay for five
idle minutes after every burst but rarely pay a cold start; at 60s you pay for
one, and pay the cold start more often. There is no setting that avoids both,
and pretending otherwise is how people end up with `minReplicaCount: 1` and a
GPU bill they meant to eliminate.

## The reveal

Scenario 01's manifest carried this comment:

    # pollingInterval and cooldownPeriod belong here too, but KEDA ignores them
    # while minReplicaCount is above zero -- it warns you as much on apply.

KEDA told you these fields were inert, and it was right, and it stopped being
right the moment one character changed. That is the most transferable thing in
this scenario: **a setting that is inert under your current configuration is not
a setting that is safe.** It is a landmine with the safety on, and the safety is
some other field you may change months later for unrelated reasons.

The same shape appears all over a real cluster — a `PodDisruptionBudget` that
never binds until you actually drain a node, a `topologySpreadConstraint` that
does nothing until you have more than one zone, a resource `limit` that never
bites until the node fills up.

## What to take to a real cluster

- Scale-to-zero converts cost into latency. Before enabling it, add up the
  cold-start budget and compare it with what your callers will tolerate.
- The budget spans teams: the autoscaler, the workload's startup time, and the
  client's timeout. It is nobody's field and everybody's problem.
- On EKS the budget is usually worse than this lab, because a scale-from-zero
  often needs a *node* too. Karpenter provisioning plus an image pull of a
  multi-gigabyte CUDA image can add minutes. Pre-pulled images and warm node
  pools exist for exactly this reason.
- Durable queues are what make aggressive scale-to-zero safe. If the request
  path has nowhere to park work, scaling to zero drops traffic rather than
  delaying it.
- `cooldownPeriod` is a cost dial with a latency price. Set it deliberately,
  from how often bursts actually arrive.

## Next

Bursty traffic. This scenario tuned the system to react quickly and to give up
its workers quickly — which is exactly the configuration that thrashes when load
arrives in waves instead of a trickle.
