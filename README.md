# Kubernetes Capability Lab

A local lab for learning advanced Kubernetes by **feeling the pain first**.

Every scenario puts you in front of a system that is failing in a way the
built-in primitives genuinely cannot fix. You debug it with `kubectl`, and the
capability that solves it — KEDA, a service mesh, an operator, Kueue — arrives as
the answer to a problem you have already met, not as a feature tour.

Everything runs in `kind` on your laptop. No cloud, no cost, no GPU required.

## Quickstart

```sh
brew install go-task     # the only prerequisite
task install             # kind, helm, kubectl, go + build the lab images
task up                  # 3-node cluster + module 1
task start -- 01/01      # break something and start debugging
```

Then:

```sh
task verify              # objective pass/fail — did you actually fix it?
task hint                # progressive hints, one at a time
task solve               # the full write-up
task fix                 # apply the shipped fix and move on
task down                # delete the cluster
```

## Your real clusters are safe

The lab exists to break things, so it never touches your normal kubectl setup:

- it uses its own kubeconfig at `.lab/kubeconfig`, and never reads or writes
  `~/.kube/config`
- every mutating script refuses to run unless the active context is a `kind`
  lab cluster

`task doctor` prints both contexts side by side so you can see the separation.

## Modules

| Module | What it teaches | |
|---|---|---|
| `01-scaling-beyond-hpa` | KEDA, scale-to-zero, VPA, Kueue — everything a CPU-target HPA cannot do | in progress |
| `02-extending-kubernetes` | Device plugins, a Kubebuilder operator, admission policy | planned |
| `03-service-mesh` | Retries, timeouts, circuit breaking, mTLS, canaries — Linkerd then Istio | planned |
| `04-platform-ops` | Observability, GitOps drift, noisy neighbours, secrets | planned |

`task list` shows the scenarios in each.

## The workload

One Go binary, `apps/mlsim`, plays three roles selected by `MODE`: an inference
`gateway`, a queue-consuming model `worker`, and a `loadgen`. Latency, error
rate, model load time and memory footprint are all environment variables, which
is why scenarios can inject new failures by editing a manifest rather than
writing new code.

GPUs are simulated by `platform/fake-gpu-device-plugin`, a real device plugin
that advertises `nvidia.com/gpu` on nodes that have no GPU. Extended-resource
scheduling behaves exactly as it does on a real GPU node, because that behaviour
lives in the scheduler and the kubelet rather than in the hardware.

## Layout

```
Taskfile.yml   front door
bin/lab        scenario-loop CLI
clusters/      kind topology
platform/      components installed into every lab cluster
apps/mlsim/    the workload
modules/<module>/scenarios/<scenario>/
    README.md      the symptom, no spoilers
    manifests/     the base state
    break.sh       injects the fault
    verify.sh      objective pass/fail
    hints.md       progressive hints
    SOLUTION.md    diagnosis, fix, and why the primitive could not do it
    fix.sh         the escape hatch
```
