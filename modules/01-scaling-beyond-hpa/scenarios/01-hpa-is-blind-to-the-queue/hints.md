## Hint 1

Look at the HPA and at the queue at the same time:

    task k -- -n ml get hpa model-worker
    task k -- -n ml exec deploy/redis -- redis-cli LLEN infer:queue

One of those two numbers is screaming. The other is calm. The HPA is not broken
and it is not lying — read its `TARGETS` column carefully and ask what that
percentage is actually measuring.

## Hint 2

Work out the throughput by hand. `model-worker` has `LATENCY_MS=800` and
`CONCURRENCY=1`, so one pod finishes 1.25 requests per second. Traffic is 12 rps.
How many pods does that need, and how many are running?

Now the real question: the worker spends 800 ms per request asleep, waiting on a
simulated GPU call. What does that do to its CPU utilisation, and therefore to
the only signal the HPA can see?

## Hint 3

You need to scale on the depth of the Redis list, but that number does not live
inside any pod, so `metrics.k8s.io` will never carry it. Kubernetes has an API
for exactly this — `external.metrics.k8s.io` — and an HPA can consume it with a
`type: External` metric.

What it does not have is anything that puts a Redis list length *into* that API.
That is the missing component. Something is already installed in this cluster
that will do it for you:

    task k -- -n keda get pods
