# Solution — HPA is blind to the queue

## What was actually happening

Two numbers tell the whole story:

    $ kubectl -n ml get hpa model-worker
    NAME           REFERENCE                 TARGETS        MINPODS   MAXPODS   REPLICAS
    model-worker   Deployment/model-worker   cpu: 19%/70%   1         15        1

    $ kubectl -n ml exec deploy/redis -- redis-cli LLEN infer:queue
    (integer) 512

The HPA is working perfectly. It is measuring CPU utilisation, CPU utilisation is
19%, that is well under the 70% target, so one replica is the correct answer to
the question it was asked.

It is just the wrong question.

`model-worker` spends 800 ms of every request asleep, waiting on a simulated GPU
call. Sleeping costs no CPU. So the busier this workload gets, the more requests
pile up behind it — and its CPU utilisation *does not move*. A queue consumer's
CPU tells you how expensive each item is, never how many are waiting.

The arithmetic: one pod at `LATENCY_MS=800` with `CONCURRENCY=1` serves 1.25 rps.
Offered load is 12 rps. One pod was running. The queue grew by roughly 10.75
items every second, and once the backlog passed ~25 items every new request was
waiting longer than the gateway's 20-second `REQUEST_TIMEOUT`. Users saw
timeouts; the autoscaler saw a healthy, idle pod.

**This is the general failure.** CPU is a proxy for load only when work is
CPU-bound. For a queue consumer, an I/O-bound service, or anything that spends
its time waiting on a GPU or a downstream API, the proxy breaks — and it breaks
silently, which is worse than breaking loudly.

## Why you cannot fix this with HPA alone

The instinct is "point the HPA at the queue depth instead." You cannot, directly.

An HPA reads metrics from three APIs:

| API | Serves | Who provides it |
|---|---|---|
| `metrics.k8s.io` | CPU and memory **of pods** | metrics-server |
| `custom.metrics.k8s.io` | any metric **attached to a Kubernetes object** | you must install an adapter |
| `external.metrics.k8s.io` | any metric from **outside the cluster** | you must install an adapter |

Queue depth is a property of Redis, not of a pod, so `metrics.k8s.io` can never
carry it. `external.metrics.k8s.io` is the right home for it — but Kubernetes
ships no implementation of that API. It is an interface with no provider.

Writing your own is a real project: an aggregated API server, a TLS-serving
deployment, an `APIService` registration, and a Redis client — several hundred
lines of Go you would then own forever, per metric source.

## The fix

KEDA is that adapter, already written, with about seventy scalers in the box.

    kubectl -n ml delete hpa model-worker
    kubectl apply -f solution/scaledobject.yaml

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: model-worker
  namespace: ml
spec:
  scaleTargetRef:
    name: model-worker
  minReplicaCount: 1
  maxReplicaCount: 15
  triggers:
    - type: redis
      metadata:
        address: redis.ml.svc.cluster.local:6379
        listName: infer:queue
        listLength: "1"       # target 1 queued item per replica
```

Delete the CPU HPA first. KEDA creates its own HPA for the same Deployment, and
two autoscalers on one workload fight — each computes a replica count from its
own metric, and on every sync the larger number wins. You get oscillation that
looks like a KEDA bug and is not.

`listLength: 1` is the tuning knob, and it is a *target ratio*, not a threshold.
KEDA asks for `ceil(queue_depth / listLength)` replicas, so in steady state the
queue settles at roughly `listLength x replicas`. A backlog of 10 asks for 10
pods — about what 12 rps needs at 1.25 rps per pod.

What you will actually see after the fix is **15 pods and an empty queue**, not
10 pods and a queue of 10. That is not KEDA overshooting. The backlog that piled
up while the service was broken briefly asked for `maxReplicaCount`, and HPA's
default five-minute scale-down stabilisation window then holds the count at its
recent peak. Scaling out is eager; scaling in is deliberately reluctant, because
scaling in too fast is how you cause the outage you were trying to avoid.

The trade-off is still the one to carry away: **every item you leave queued is
latency a user feels.** Raise `listLength` and you run fewer pods and miss your
p99; lower it and you buy latency with replicas. It is the same dial as a CPU
target, pointed at a signal that actually moves.

## The reveal

KEDA is not a replacement for HPA. Look at what it actually built:

    $ kubectl -n ml get hpa
    NAME                    REFERENCE                 TARGETS
    keda-hpa-model-worker   Deployment/model-worker   2/2 (avg)

    $ kubectl get apiservice v1beta1.external.metrics.k8s.io
    NAME                             SERVICE                                    AVAILABLE
    v1beta1.external.metrics.k8s.io  keda/keda-operator-metrics-apiserver        True

It wrote an ordinary `HorizontalPodAutoscaler` with a `type: External` metric,
and it registered itself as the `external.metrics.k8s.io` API server so that HPA
has something to read. The scaling loop is the same one you already knew. KEDA
supplied the missing half of the interface.

That is worth holding onto, because it tells you where to look when KEDA
"doesn't scale": check the HPA it generated (`kubectl describe hpa keda-hpa-...`)
and check that the metrics API server is `Available`. A `ScaledObject` that looks
healthy while `keda-operator-metrics-apiserver` is down will scale nothing and
report nothing.

## What to take to a real cluster

- CPU-target HPAs are correct for CPU-bound services and quietly wrong for
  everything that waits. Inference servers, queue consumers, and API gateways
  are usually in the second group.
- Scale on the signal your users feel. For queue-backed work that is depth or
  age, not utilisation.
- The same reasoning applies to SQS, Kafka consumer lag, Prometheus queries and
  cron windows — they are all just different KEDA triggers on the same machinery.
- On EKS this composes downward: KEDA decides how many pods, and Karpenter or
  the Cluster Autoscaler then decides how many nodes those pods need.

## Next

`02-scale-to-zero-cold-start` — `minReplicaCount: 1` is still pinning a GPU you
are paying for around the clock. Dropping it to zero is one character, and it
introduces a new problem.
