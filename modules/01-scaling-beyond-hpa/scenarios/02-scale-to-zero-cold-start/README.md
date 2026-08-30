# Scale to zero and the cold start

**The change.** Scenario 01 left you with a `ScaledObject` and `minReplicaCount: 1`.
That one replica is a GPU worker you are paying for around the clock to serve a
queue that is empty most of the night. So it went to zero. One character.

**The page.** The overnight bill dropped. Then the morning shift arrived, and the
first minute of traffic was a wall of gateway timeouts. By the time anyone opened
a terminal it had fixed itself, and everything looked perfect. It has now
happened three mornings running, and only ever after a quiet period.

**What you know.** Nothing else changed. The `ScaledObject` is the one you wrote,
the gateway is untouched, and the workers are the same image. Traffic is a
trickle at this hour — about 1 request per second, nowhere near the 12 rps this
thing was sized for.

**Your objective.** Starting from zero replicas, the inference path must serve a
low-rate morning trickle with **zero failed requests**, and it must go back to
zero afterwards — the point was to stop paying for idle GPUs. `task verify`
measures all three: no errors, it scaled up, it scaled back down.

Somewhere to start:

    task k -- -n ml get scaledobject model-worker
    task k -- -n ml get deploy model-worker -w
    task k -- -n ml exec deploy/redis -- redis-cli LLEN infer:queue
    task k -- -n ml logs deploy/inference-gateway --tail=20

A useful thing to do while you watch: send a single request into a cold system
and time it.

    task k -- -n ml run probe --rm -it --restart=Never --image=k8slab/mlsim:dev \
      --command -- curl -s -o /dev/null -w 'status=%{http_code} total=%{time_total}s\n' \
      --max-time 120 http://inference-gateway.ml.svc.cluster.local:8080/infer
