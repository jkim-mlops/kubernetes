## Hint 1

Do not read any YAML yet. Watch the replica count for one full cycle — about
three minutes — with the wave generator running:

    task k -- -n ml scale deployment loadgen --replicas=1
    task k -- -n ml get deploy model-worker -w

Write down what the number does. There is a moment in every cycle where it does
something you did not ask for, and the timing of that moment relative to the
next wave is the entire scenario.

## Hint 2

Two intervals are competing, and neither is a mistake on its own:

    task k -- -n ml get scaledobject model-worker \
      -o jsonpath='{.spec.cooldownPeriod}{"\n"}'
    task k -- -n ml get deploy loadgen \
      -o jsonpath='{.spec.template.spec.containers[0].env}' | tr ',' '\n' | grep -A1 BURST

Scenario 02 chose one of these to stop paying for idle GPUs, and it was the
right choice for a trickle that stops for the night. Compare it against how long
this traffic actually goes quiet for.

## Hint 3

You have probably reached for the Kubernetes anti-flapping mechanism by now: an
HPA `behavior.scaleDown.stabilizationWindowSeconds`, which KEDA will happily
pass through for you under `advanced.horizontalPodAutoscalerConfig`.

Set it and measure again. It will not help on its own, and understanding why is
the point of this scenario. Watch these two numbers together across a lull:

    task k -- -n ml get deploy model-worker -o jsonpath='{.status.replicas}{"\n"}'
    task k -- -n ml get hpa keda-hpa-model-worker -o jsonpath='{.status.desiredReplicas}{"\n"}'

There is a moment where they disagree — where the Deployment is at zero and its
own HPA still wants fifteen. Whatever performed that scale-down was not the HPA,
and is not bound by anything you configure on it. An HPA cannot express zero.

## Hint 4

So the scale-down happens in two stages with two owners, and you have to satisfy
both:

    N -> 0   is KEDA's, and obeys cooldownPeriod alone
    N -> 1   is the HPA's, and obeys the stabilization window

Fix only the first and the workers stop hitting zero — but the HPA will still
walk them down to one across the lull, and the next wave rebuilds from one pod
instead of from none. Barely an improvement.

Both intervals have to outlast the quiet period. Measure how long the queue is
actually empty for — it is longer than the traffic is off, because the backlog
takes time to drain — and size both against that number rather than against the
burst period.

One warning: the stabilization window already has a default, and it is larger
than the value most people would type in. Check what it is before you "improve"
it.
