# Fifteen replicas, none ready

**The change.** The data science team shipped a bigger model last week. Same
image, same deployment, same everything else — the weights got larger.

**The page.** Inference is down. Not slow: down. Every request is timing out.

**What you know.** The autoscaler is doing exactly what the last three scenarios
taught it to do, and by every signal you have learned to trust it is healthy:

    $ kubectl -n ml get scaledobject model-worker
    NAME           READY   ACTIVE   FALLBACK   PAUSED
    model-worker   True    True     False      False

    $ kubectl -n ml get deploy model-worker
    NAME           READY    UP-TO-DATE   AVAILABLE
    model-worker   0/15     15           0

There are fifteen replicas. The queue is deep, KEDA has scaled against it
correctly, and the `ScaledObject` reports no problem at all.

**Your objective.** Serve the offered load with zero errors and a p99 under 4
seconds — and have a fleet that is actually running. `task verify` measures the
traffic and checks that workers are genuinely ready, not merely counted.

Somewhere to start:

    task k -- -n ml get pods -l app=model-worker
    task k -- -n ml describe pod -l app=model-worker | grep -A6 'Last State'
    task k -- -n ml top pod
    task k -- -n ml get pods -l app=model-worker \
      -o custom-columns='POD:.metadata.name,QOS:.status.qosClass,RESTARTS:.status.containerStatuses[0].restartCount'

Read the two numbers in that `READY` column separately. They are not the same
question, and only one of them is about whether your service works.
