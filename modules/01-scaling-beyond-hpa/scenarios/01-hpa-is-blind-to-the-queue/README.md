# HPA is blind to the queue

**The page.** Inference latency has gone through the roof. Users are getting
timeouts. This started when marketing turned on a campaign and traffic went from
a trickle to about 12 requests per second.

**What you know.** The path is:

    loadgen  ->  inference-gateway  ->  redis list "infer:queue"  ->  model-worker

`model-worker` already has a `HorizontalPodAutoscaler`. It was tested. It works —
you watched it scale during a load test months ago.

**Your objective.** The inference path must serve 12 rps with a p99 under 4
seconds and zero timeouts. `task verify` measures exactly that.

Somewhere to start:

    task k -- -n ml get pods
    task k -- -n ml get hpa
    task k -- -n ml top pod
    task k -- -n ml exec deploy/redis -- redis-cli LLEN infer:queue
    task k -- -n ml logs deploy/loadgen --tail=20
