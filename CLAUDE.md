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
simulated by `platform/fake-gpu-device-plugin`, a real device plugin registering over the
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
A branch with no modules — `main` — still gets a cluster of its own, `k8slab-base`: the harness and
the platform components are runnable on their own.

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

Queue access is `go-redis`, metrics are `prometheus/client_golang`. mlsim was stdlib-only at first,
justified by the image building without network access — which was never true of anything else here
(`task install` brew-installs, the build pulls base images, `task up` pulls `kindest/node`, module 01
helm-installs KEDA). What that rule really bought was 269 lines that taught nothing about Kubernetes
and could fail in ways indistinguishable from a scenario failing.

Real metric types are the payoff, not just the deletion: `mlsim_request_duration_seconds` is a
histogram, so a scenario can scale on a latency quantile, and the default registry brings `go_*` and
`process_*` along for the right-sizing work.

**Two traps this code has already paid for**, both about the gateway's blocking wait on a reply key:

- The wait must **not** be tied to `r.Context()`. Aborting when the caller hangs up is better
  practice generally, but it makes the numbers `verify.sh` judges depend on when a load generator
  disconnected — and `verify.sh` deletes the `measure` job between phases, which is exactly that.
- Only `redis.Nil` may be read as "timed out". Mapping any other empty result onto the same return
  makes the gateway answer 504 for a request that was merely cancelled, and inflates
  `mlsim_timeouts_total` — a scenario judged on a timeout count cannot afford invented timeouts.
- The gateway holds a connection for the whole life of a request, because BLPOP occupies it until a
  worker answers. `PoolSize` must exceed peak in-flight requests or requests queue for a connection
  on top of queueing for a worker, inventing latency that corrupts every measurement.

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
- **Not yet hit, but waiting**: the loadgen's `http.Client` uses the default transport, whose
  `MaxIdleConnsPerHost` is 2. At 12 rps with ~10 requests in flight most connections are closed
  rather than pooled — immaterial now, but a scenario that raises `RPS` into the hundreds would be
  measuring TCP handshakes and billing them to the gateway. Set it explicitly before that happens.

## Current state

- `main` — setup only: harness, `mlsim`, platform components.
- `module/01-scaling-beyond-hpa` — scenario 01 "HPA is blind to the queue". Validated end to end:
  12 rps against one replica drives the queue past 500 and pegs p99 at the 20s gateway timeout;
  after the KEDA `ScaledObject`, p99 is 899ms with zero errors and the queue drains to 0.

### Open, in order

1. Design scenario 02, get approval, build, commit.

Planned module 1 arc after that: scale-to-zero and its cold start; scaling thrash under bursty
load; right-sizing requests after an OOMKill; queueing batch work that does not fit; and a Kafka
scenario where **consumer lag is not queue depth** — the lag semantics are the lesson, which is why
the queue underneath the earlier scenarios stays Redis. Then modules 02 extending-kubernetes,
03 service-mesh (Linkerd then Istio ambient), 04 platform-ops.

### Settled, do not relitigate

- **mlsim uses libraries** — see "The workload". The offline-build argument for hand-rolling does
  not survive contact with the rest of the repo.
- **The GPU plugin stays a real gRPC device plugin**, and module 02 reads it as the worked example
  of the device-plugin interface. A PATCH of node status would be a fraction of the code and would
  teach a falsehood: it survives no kubelet restart and never exercises `Allocate`.
- **KEDA installs in the module, not `task up`.** Platform is infrastructure; a capability that is
  the answer to a scenario belongs to the module. The lesson is the `ScaledObject`, not `helm install`.
- **The role stays an env var (`MODE`), not a subcommand.** Considered and deferred, not
  overlooked: `args: ["gateway"]` would be the more idiomatic shape — visible in the pod spec,
  validated at startup — but every other dial in mlsim is env, and scenarios inject faults by
  editing env. Revisit only if it actually bites.
- **Redis stays the queue; no localstack.** Faking SQS would buy familiarity at the cost of faking
  IRSA / Pod Identity, which is the genuinely hard part on real EKS — false comfort is worse than
  an honest prop.
