# CLAUDE.md

## What this repo is for

A local Kubernetes lab that teaches advanced concepts by **feeling the pain first**: each scenario
presents a system failing in a way the built-in primitives genuinely cannot fix, so the capability
that solves it (KEDA, a mesh, an operator, Kueue) arrives as the answer to a problem the reader has
already met.

**The point is the owner's understanding, not a comprehensive lab.** Content written ahead of them
turns discovery into reading a blog post. Optimise for what they learn, not for how complete the
repo looks.

Owner background: solid on basic networking and HPA, works on EKS day to day. Scenarios should
teach things that transfer to a real managed cluster.

## How to work here

These were agreed explicitly. Departing from them has already caused rework.

- **One unit at a time: design → they approve → build → commit.** Never build more than one
  scenario ahead.
- A design proposal is **a few sentences**: the pain, the symptom they would see, the capability
  that resolves it. Then wait.
- **Commit as you go**, one commit per unit, with a message explaining the reasoning. Do not
  accumulate a large uncommitted or unreviewed blob.
- **Branch per module** (`module/NN-name`). `main` carries setup only — harness, workload, platform.
- **Tooling installs go in `task install`**, never ad-hoc `brew install` during a session. Someone
  cloning this on a Mac should get running with `brew install go-task && task install && task up`.
- `Taskfile.yml` (go-task) is the front door. Not a Makefile.
- **Prove it before claiming it.** A scenario is not done until `verify` has been observed failing
  before the fix and passing after. Show the run output.

## Environment

Apple Silicon, Docker Desktop, kind, Homebrew. There is **no NVIDIA GPU** — `nvidia.com/gpu` is
simulated by `platform/fake-gpu-device-plugin`, which is a real device plugin registering over the
kubelet's gRPC socket. Extended-resource scheduling behaves identically to real hardware because
that behaviour lives in the scheduler and kubelet, not the silicon.

## Safety — non-negotiable

The owner's normal kubectl context points at a **real EKS cluster**. This lab exists to break
things. Two independent protections, and neither may be weakened:

1. `KUBECONFIG` is pinned to `.lab/kubeconfig` (set by the Taskfile `env:` block and by
   `lib/guard.sh`). `~/.kube/config` is never read or written.
2. `lib/guard.sh` is sourced by **every mutating script** and calls `guard::require_lab_context`,
   which refuses to run unless the active context starts with `kind-k8slab-`.

One kind cluster per module, named `k8slab-<NN>` (from the module directory's numeric prefix).

## Layout

```
Taskfile.yml                  front door
bin/lab                       scenario-loop CLI (bash); the Taskfile delegates to it
lib/{log,guard,wait}.sh       shared helpers
clusters/base.yaml            kind topology: 1 control-plane + 2 workers
platform/<name>/install.sh    installed into every lab cluster by `lab up`
apps/mlsim/                   the workload
modules/<module>/
  README.md  install.sh       module-level platform (e.g. Redis, KEDA)
  scenarios/<scenario>/
```

Node labels are applied by `bin/lab` with `kubectl label` after cluster creation, not via
`kubeadmConfigPatches` — kubeadm's `kubeletExtraArgs` schema changed in v1beta4 and this keeps
working across Kubernetes versions.

## Scenario anatomy

Seven files, always the same, so the loop becomes muscle memory:

| File | Role |
|---|---|
| `README.md` | the symptom and business context — **no spoilers** |
| `manifests/` | the base state |
| `break.sh` | injects the fault |
| `verify.sh` | **objective pass/fail** — what makes "did I actually fix it?" answerable |
| `hints.md` | progressive hints, one `## Hint N` section each |
| `SOLUTION.md` | diagnosis, the fix, and *why the primitive could not do it* |
| `fix.sh` | the escape hatch |

Scripts receive `LAB_ROOT` and `SCENARIO_DIR` in the environment.

## The workload

`apps/mlsim` is one Go binary and one image, playing a role chosen by `MODE`:
`gateway` | `worker` | `loadgen`. Behaviour is env-driven — `LATENCY_MS`, `ERROR_RATE`,
`MODEL_LOAD_SECONDS`, `MEM_PER_MODEL_MB`, `CONCURRENCY`, `HANG_RATE` — so a scenario injects a new
failure by editing a manifest rather than adding code.

Stdlib only, including a hand-written RESP client and Prometheus text endpoint, so the image builds
without network access.

The load generator's final line is the contract every `verify.sh` parses:

```
RESULT requests=1200 errors=0 timeouts=0 p50_ms=812 p95_ms=1210 p99_ms=1490 max_ms=... rps=... duration_s=...
```

`WARMUP` sends traffic without recording it — an autoscaler may take time to converge, it just may
not fail to converge.

## Commands

```
task install                    idempotent: brew deps, docker check, build images
task doctor                     versions + proves the lab is isolated from your real clusters
task up -- <module>             cluster + platform + module
task list
task start -- <module>/<scenario>    numeric prefixes work: 01/01
task verify | hint | solve | fix | reset
task k -- get pods -A           kubectl scoped to the lab
task test [-- <module>]         regression: every scenario must fail before its fix and pass after
task down | task clean
```

## Gotchas already paid for

- **go-task YAML**: an unquoted `cmds` string containing `": "` parses as a mapping and fails with
  `invalid keys in command`. Single-quote the whole scalar.
- **bash `pipefail`**: `x="$(cmd | head -1)"` aborts silently under `set -e` when `cmd` fails.
  Append `|| true` to the assignment.
- **Device plugin**: `pluginapi.KubeletSocket` is already an absolute path. Do not
  `filepath.Join` it with `DevicePluginPath`.
- **Waiting on the GPU plugin**: wait for *every* GPU node to advertise the resource, not the
  first. A DaemonSet that is Running but failed to register looks healthy and schedules nothing.
- **`verify.sh` should clear the queue before measuring**, so the result depends on whether the
  system can serve its offered load rather than on how long the reader spent debugging.
- **KEDA** warns that `pollingInterval` and `cooldownPeriod` are inert while `minReplicaCount > 0`.

## Current state

- `main` — setup only: harness, `mlsim`, platform components.
- `module/01-scaling-beyond-hpa` — scenario 01 "HPA is blind to the queue". Validated end to end:
  12 rps against one replica drives the queue past 500 and pegs p99 at the 20s gateway timeout;
  after the KEDA `ScaledObject`, p99 is 899ms with zero errors and the queue drains to 0.

### Open, in order

1. **Bug**: `lab up` hard-requires a module directory, so `main` alone is not runnable
   (`task up` → `No such module`). Make the module optional: cluster + platform, module only if one
   exists.
2. **Setup review** — the owner has not yet ruled on four decisions:
   - `mlsim` being stdlib-only (~200 lines of hand-rolled Redis/metrics code) vs. `go-redis` +
     `prometheus/client_golang`
   - the GPU plugin being a real gRPC device plugin (~200 lines) vs. a few lines that PATCH
     `nvidia.com/gpu` onto node status
   - KEDA installing at `task up` vs. being installed as part of solving scenario 01
   - Redis as the queue vs. something closer to their EKS stack (SQS via localstack, or Kafka)
3. Design scenario 02, get approval, build, commit.

Planned module 1 arc after that: scale-to-zero and its cold start; scaling thrash under bursty
load; right-sizing requests after an OOMKill; queueing batch work that does not fit. Then modules
02 extending-kubernetes, 03 service-mesh (Linkerd then Istio ambient), 04 platform-ops.
