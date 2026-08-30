# Solution — Thrashing on bursty load

## What was actually happening

The workers were being destroyed in the gap between waves and rebuilt for the
next one, paying the 20-second model load every time.

    cooldownPeriod: 60      # from scenario 02
    gap between waves: ~90s

The lull is longer than the cooldown, so every lull is long enough to trigger a
teardown, and every wave then starts from zero. Scenario 02's answer was correct
for a trickle that stops for the night. Applied to traffic that stops for ninety
seconds at a time, the same number means "throw away the workers roughly every
three minutes."

Nothing here is a misconfiguration. **An autoscaler's time constants encode an
assumption about the shape of your traffic**, and shape is not something you can
read off an average. Six requests per second averaged over the day and six
requests per second in waves are the same number and completely different
problems.

## Two transitions, two owners, two settings

The instinct is right: this is flapping, and Kubernetes has a mechanism for it.
An HPA takes a `behavior.scaleDown.stabilizationWindowSeconds`, which holds the
replica count at its recent peak instead of chasing every dip, and KEDA passes
your `behavior` block straight to the HPA it manages.

Set only that, and nothing improves. Set only `cooldownPeriod`, and nothing
improves either. **You need both, because the scale-down happens in two stages
owned by two different controllers.**

| Transition | Owner | Governed by |
|---|---|---|
| 1 → N, N → 1 | HorizontalPodAutoscaler | `behavior.scaleDown`, stabilization window |
| 0 → 1 | KEDA | trigger activation |
| **N → 0** | **KEDA** | **`cooldownPeriod`, and nothing else** |

KEDA's documentation states the split plainly:

> `advanced.horizontalPodAutoscalerConfig.behavior` — "directly affect scaling of
> 1<->N replicas, which is internally being handled by HPA."

> "the KEDA `cooldownPeriod` only applies when scaling to 0; scaling from 1 to N
> replicas is handled by the Kubernetes Horizontal Pod Autoscaler."

You can watch the split happen. Sampling the Deployment and its HPA every eight
seconds through one full cycle, with the original `cooldownPeriod: 60`:

```
    t   repl ready queue  hpa
  104s    15    15     0   15     queue empty, lull begins
  176s    15    15     0   15     HPA holding at 15 (default 300s window)
  184s     0     0     0   15     <-- KEDA patches to 0. The HPA still wants 15.
  240s     1     0    35    0     next wave arrives, rebuilding from nothing
  320s    15    15   138   15     fully warm again, ~80s into the wave
```

At `t=184` the Deployment is at **0 replicas while its own HPA still desires
15**. An HPA cannot express zero — `minReplicas` is at least 1 — so the
transition doing the damage is structurally outside its reach. KEDA patches the
replica count directly, from whatever it happens to be, walking past the
stabilization window the HPA was carefully applying.

This is current KEDA behaviour, not a misconfiguration, and there is an open
issue asking for it to change:
[kedacore/keda#7204, "Honour HPA Scale-Down Policy When Scaling Down to Zero
Replicas"](https://github.com/kedacore/keda/issues/7204), proposing that KEDA
"should first gradually scale down the replicas from N to 1 in steps of 2, and
only then scale from 1 to 0."

### Why fixing only the cooldown is not enough

Raise `cooldownPeriod` to 300 on its own and the measured wave still comes back
at **p99 28263ms**. The workers no longer go to zero — and the HPA scales them
15 → 1 across the lull instead, so the wave rebuilds from one pod and pays the
model load anyway. Zero was never the point; *rebuilding* was.

The stabilization window has to outlast the lull **plus the time the queue takes
to drain** — about 135 seconds here, not the 90 seconds the traffic is quiet
for. Kubernetes defaults it to 300s, which covers this comfortably. The trap is
that setting it to a number that sounds careful, like 120, is worse than never
touching it at all.

## The fix

    kubectl apply -f solution/scaledobject.yaml

```yaml
spec:
  cooldownPeriod: 300          # was 60 — stops KEDA jumping N -> 0
  advanced:
    horizontalPodAutoscalerConfig:
      behavior:
        scaleDown:
          stabilizationWindowSeconds: 300   # stops the HPA walking N -> 1
```

Measured across a steady-state wave: **p50 807ms, p99 901ms, zero errors**, with
the workers never rebuilt. Compare the same wave before: p50 18903ms, p99
39252ms, 64 failed requests, and 55 seconds of the run with no workers at all.

The rule worth carrying: **each time constant must exceed the gap it is meant to
sit through.** For `cooldownPeriod` that is the gap you will tolerate keeping
workers for; for the stabilization window it is the same gap plus the drain.
Both must stay below the gaps you genuinely want to scale to zero for, so the
overnight case from scenario 02 still works.

If no such value exists — if your quiet periods are never long enough to be
worth a cold start — that is the finding, not a tuning failure. **Scale-to-zero
has a break-even point.** When the idle gap is shorter than the cold start
needed to recover from it, zero costs more than it saves, and the honest
configuration is a warm floor.

`idleReplicaCount` is the other dial worth knowing, and it controls depth rather
than frequency. Only `0` is supported and it must be below `minReplicaCount`;
with `minReplicaCount: 5, idleReplicaCount: 0` the workload sits at zero when
idle and jumps straight to five on activation, instead of starting one pod and
waiting for the HPA to ramp. It does not avoid a cold start — it shortens the
recovery once one is being paid.

## What to take to a real cluster

- Autoscaler time constants are assumptions about traffic shape. Write down the
  shape — burst length, gap length, cold-start cost — before choosing them.
- Averages hide shape. The same rps in waves and in a trickle need different
  configuration.
- When two controllers share an object, map the transitions to their owners
  before tuning, and check whether a scale-down passes through more than one of
  them. `kubectl get hpa -o yaml` shows you what KEDA actually built.
- A shorter timeout is not a safer timeout. Lowering a stabilization window from
  its default to a number that merely sounds careful is how this scenario's fix
  fails on the second attempt.
- On EKS the thrash is worse than it looks here, because pod churn drives node
  churn: Karpenter consolidates the emptied nodes away during the lull, and the
  next wave pays node provisioning *and* an image pull on top of the model load.
  `consolidateAfter` is the same class of dial as `cooldownPeriod`, one layer
  down, and the two want to agree with each other.
- A cold start is a cost per *transition*, not per day. Multiply it by how often
  your configuration chooses to pay it.

## Next

Right-sizing. Every worker in this module has been running with a memory request
nobody measured, on the assumption that model weights are small. They are not,
and the pod that gets OOMKilled will not be the one you expect.
