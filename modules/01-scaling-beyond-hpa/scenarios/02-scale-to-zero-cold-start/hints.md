## Hint 1

Watch a cold start happen instead of reasoning about it. In one terminal:

    task k -- -n ml get deploy model-worker -w

In another, put a single request in and time it:

    task k -- -n ml exec deploy/redis -- redis-cli LPUSH infer:queue probe-1

Now watch the clock. How long does the queue sit at 1 before the replica count
moves at all? Nothing is broken during that gap — write down how long it is,
because it is the first of three numbers you need.

## Hint 2

Compare that gap with what the gateway is prepared to tolerate:

    task k -- -n ml get deploy inference-gateway \
      -o jsonpath='{.spec.template.spec.containers[0].env}' | tr ',' '\n' | grep -A1 REQUEST_TIMEOUT

One of these two numbers is a KEDA default nobody chose. The other is a
deliberate decision from scenario 01 that was correct at the time. They have
never been compared with each other, because until `minReplicaCount` hit zero
there was always a worker already running and the comparison never mattered.

## Hint 3

There is a third number, and it is the largest. A pod that is `Running` is not a
pod that is working:

    task k -- -n ml get pods -l app=model-worker
    task k -- -n ml logs -l app=model-worker --tail=5

Look at `READY` for a worker that has just started, and at what the log says it
is doing. `MODEL_LOAD_SECONDS` is in the manifest.

Add the three up. That total is your cold-start budget, and something in the
request path has to be willing to wait for it — or you have to stop paying it so
often. Both of those are settings you can change; neither of them is
`listLength`.

## Hint 4

`kubectl explain scaledobject.spec` lists the fields. Two of them govern the
ends of this problem: one decides how quickly KEDA notices work, the other
decides how long a worker survives after the work stops.

Scenario 01's manifest told you these two were being ignored, and why. Read that
comment again — the condition it named has just changed.
