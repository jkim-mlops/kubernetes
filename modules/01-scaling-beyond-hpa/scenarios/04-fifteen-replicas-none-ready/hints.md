## Hint 1

Read the `READY` column as two separate numbers:

    task k -- -n ml get deploy model-worker

`0/15`. Fifteen is how many pods the autoscaler asked for and got. Zero is how
many of them can serve a request. Every green signal you have — the
`ScaledObject`, the replica count, the absence of any scaling error — is
reporting on the first number.

Now look at the pods themselves:

    task k -- -n ml get pods -l app=model-worker

## Hint 2

The `RESTARTS` column is not zero and is climbing. A pod that is restarting has
already told you why, in its previous life:

    task k -- -n ml describe pod -l app=model-worker | grep -B2 -A8 'Last State'

`Reason: OOMKilled`, `Exit Code: 137`. That is not Kubernetes evicting a pod
under pressure, and it is not a scheduling decision. It is the Linux kernel
enforcing a cgroup limit on one container, and it will happen every time that
container asks for more than the limit allows.

## Hint 3

So how much does it actually need? Ask, rather than guess. A worker survives for
about twenty seconds before it dies, which is long enough to catch:

    task k -- -n ml top pod -l app=model-worker

Compare that with what the manifest promises:

    task k -- -n ml get deploy model-worker \
      -o jsonpath='{.spec.template.spec.containers[0].resources}{"\n"}'

Then find the line in the Deployment that changed last week, and note that it
was not in the `resources` block. `MEM_PER_MODEL_MB` is the size of the weights
this pod loads into memory and keeps there.

## Hint 4

Two numbers govern memory and they are read by two different components:

- `requests` is what the **scheduler** uses to decide whether a pod fits on a
  node. Nothing enforces it at runtime.
- `limits` is what the **kernel** enforces, via the cgroup. The scheduler never
  looks at it when packing.

Set them both, from the measurement rather than from the gateway's manifest
(which was copied here, and holds no model). While you are there, look up what
the relationship between the two does to the pod's QoS class:

    task k -- -n ml get pods -l app=model-worker \
      -o custom-columns='POD:.metadata.name,QOS:.status.qosClass'
