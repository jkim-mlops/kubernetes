# Solution — Fifteen replicas, none ready

## What was actually happening

The model got bigger. The manifest did not.

    MEM_PER_MODEL_MB: 400        # the weights, held for the process lifetime
    limits: memory: 256Mi        # what the kernel will allow

Every worker started, spent twenty seconds loading its model, allocated 400MB,
and was killed by the kernel the instant it crossed 256Mi — before it had ever
become ready, and therefore before it had ever consumed a single job. Kubernetes
restarted it, and it did the same thing again.

Measured before the fix:

    requests=719  errors=719  timeouts=719  p50=90032ms  p99=90103ms
    workers: 0 ready of 15   restarts=68   OOMKilled=15

Fifteen replicas. Sixty-eight restarts. Zero service.

**The autoscaler was working perfectly the entire time.** It read a deep queue,
asked for fifteen replicas, and got fifteen replicas. Everything it is
responsible for succeeded. `kubectl get scaledobject` had nothing to report,
because nothing it measures had gone wrong.

## Why every signal you trusted stayed green

Three scenarios in this module trained you to trust queue depth and replica
count. Both were correct here and both were useless, because they answer a
question you had stopped asking: *are the pods that exist actually working?*

`0/15` is the whole story and it is easy to skim, because the eye goes to the
number that changed — fifteen — and not to the one that did not. **A replica
count is a count, not a capacity.** `AVAILABLE` and `READY` are the columns that
mean something; `UP-TO-DATE` and the replica count are about the controller
having done as it was told.

There is a nastier version of this waiting in any real cluster: had the model
been slightly smaller, the pod would have loaded it, become ready, popped a job
with `BRPOP`, and *then* been OOMKilled mid-inference. The job would be gone —
Redis lists have no acknowledgement, so nothing redelivers it — and the caller
would time out while queue depth read perfectly healthy. The failure would
appear as a small percentage of lost requests with no error anywhere to explain
them. Here you were lucky: the pod died before it could take work with it.

## requests and limits are two contracts, not one setting

This is the part worth carrying everywhere.

| | Read by | Enforced when | Effect if wrong |
|---|---|---|---|
| `requests` | the **scheduler** | at placement | pods packed onto nodes that cannot really hold them |
| `limits` | the **kernel** (cgroup) | continuously | container OOMKilled the moment it exceeds |

They are read by different components at different times, and neither one checks
the other. Nothing in Kubernetes will tell you that a container requesting 64Mi
routinely uses 437Mi. The scheduler is satisfied — 64Mi fits easily — and the
kernel is satisfied right up until the allocation that crosses the line.

Horizontal autoscaling is what turns this from a latent error into an outage,
because it multiplies a per-pod sizing mistake by the replica count, and the
replica count is driven by load. **The better your autoscaling works, the faster
a wrong request becomes a fleet-wide failure.**

## The fix

Measure first:

    $ kubectl -n ml top pod -l app=model-worker
    NAME                CPU(cores)   MEMORY(bytes)
    model-worker-...    18m          437Mi

400MB of weights plus the Go runtime lands around 437Mi. So 256Mi was never
survivable, and 512Mi would leave nothing for a bad day.

```yaml
resources:
  requests:
    memory: 768Mi     # tell the scheduler the truth
    cpu: 100m
  limits:
    memory: 768Mi     # requests == limits => Guaranteed QoS
```

    requests=365  errors=0  timeouts=0  p50=815ms  p99=901ms
    workers: 15 ready of 15   restarts=0   OOMKilled=0

Setting `requests` equal to `limits` puts the pod in the **Guaranteed** QoS
class. For a workload whose footprint is a known model size rather than a guess,
that is the honest setting: it tells the scheduler exactly what the pod costs,
and it puts the pod last in line when a node comes under memory pressure.
Kubernetes evicts `BestEffort` first, then `Burstable` pods that are furthest
above their requests, and `Guaranteed` last. A pod that under-requests is not
just risking its own limit — it is volunteering to be evicted first.

The gateway can stay `Burstable`. It holds no model and its usage genuinely is
bursty, so a request below its peak is a reasonable bet there.

## The check nobody does until it bites

Honest requests make your capacity visible, and it may be smaller than you
assumed. Fifteen replicas at 768Mi is 11.5 GiB of *reservation* that the
scheduler must find somewhere:

    kubectl describe node <node> | sed -n '/Allocated resources/,/Events/p'

`maxReplicaCount` is a number you wrote down. It is not a promise the cluster
can keep. When it exceeds what the nodes can hold, KEDA will keep asking, the
pods will sit `Pending`, and the queue will not drain — and `Pending` looks
exactly as green as `Running` in a replica count. Check the arithmetic before
trusting the ceiling.

## What to take to a real cluster

- `READY` and `AVAILABLE` are the columns that describe your service. Everything
  else describes your controllers.
- Set requests from measurement. `kubectl top`, or a Prometheus quantile over a
  week, beats a number copied from an adjacent manifest every time.
- `requests` is scheduling, `limits` is enforcement, QoS is their relationship.
  Under-requesting makes you the first pod evicted; exceeding a limit makes you
  OOMKilled regardless of how idle the node is.
- Autoscaling multiplies sizing errors. Re-check resources whenever the thing in
  the container changes size — and the people who change model weights are
  usually not the people who own the Deployment.
- On EKS, `Guaranteed` QoS for GPU workers is close to mandatory: the pods are
  expensive, the nodes are scarce, and being evicted first is the worst possible
  property for the most expensive pod on the node.

## Next

That is module 01. The remaining arc — batch work that does not fit, and Kafka
consumer lag, where lag is not queue depth — is where scaling stops being about
replicas and starts being about admission.
