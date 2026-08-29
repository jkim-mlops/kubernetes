# Scaling beyond HPA

You already know `HorizontalPodAutoscaler` with a CPU target. This module is about
everything that model breaks on.

Each scenario puts you in front of a system that is visibly failing while every
dashboard you know how to read says it is fine. The fix is never "raise the
replica count" — it is a different signal, a different controller, or a different
scheduling model.

| Scenario | The thing HPA cannot do |
|---|---|
| `01-hpa-is-blind-to-the-queue` | Scale on a metric that does not live inside the pod |

Planned, in order — each builds on the last: scale-to-zero and its cold start,
scaling thrash under bursty load, right-sizing requests after an OOMKill, and
queueing batch work that does not fit.
