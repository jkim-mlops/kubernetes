# Thrashing on bursty load

**The change.** Nothing. You shipped scenario 02's fix — `pollingInterval: 5`,
`cooldownPeriod: 60`, a gateway that will wait out a cold start — and it worked.
The morning trickle is served, and the workers go away when the work does.

**The page.** Traffic is no longer a trickle. A batch upstream now submits work in
waves: busy for about a minute and a half, quiet for about a minute and a half,
all day long. Nobody is getting errors. They are getting *slow*, and they are
getting slow **on every single wave** — not once in the morning, but every time
the work comes back.

**What you know.** The cold start you characterised in scenario 02 is 20 seconds
of model load on top of scheduling. You accepted paying that once a day. You are
now paying it roughly every three minutes.

**Your objective.** Once the system is warm and traffic is in its steady rhythm,
a wave must be served with a p99 under 4 seconds and no errors. `task verify`
lets the first wave and the first lull go by unmeasured — the very first cold
start of the day is expected, and is not what this is about — and then measures
one full wave.

Somewhere to start:

    task k -- -n ml get deploy model-worker -w
    task k -- -n ml get scaledobject model-worker -o yaml | grep -A3 cooldown
    task k -- -n ml exec deploy/redis -- redis-cli LLEN infer:queue
    task k -- -n ml get hpa keda-hpa-model-worker

Turn the wave generator on and watch for four or five minutes before you change
anything. The shape of what happens matters more than any single number:

    task k -- -n ml scale deployment loadgen --replicas=1
