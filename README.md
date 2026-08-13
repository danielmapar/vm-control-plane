# vm-control-plane

A small, production-shaped VM control plane in Go. It has the same shape as
real libvirt-based compute platforms (the OpenStack Nova / KubeVirt lineage): a
state-owning **control plane** (gRPC API + PostgreSQL desired state +
reconciler + scheduler), a per-host **agent** that wraps the hypervisor
(libvirt/KVM, Open vSwitch, qcow2), and level-triggered reconciliation between
them. You drive it with one small CLI, `vmctl`.

## What the demo covers

- **Full VM lifecycle** over an async, operation-based API: create → boot →
  stop / start → delete. Every mutation returns an operation you can wait on.
- **Desired-state reconciliation** — you declare what you want and the control
  plane converges to it and repairs drift. `list vms` shows an
  `applied/desired` revision you can watch catch up.
- **Real KVM** — the same commands boot a real Ubuntu guest under KVM, reach it
  over SSH, and tear it down. Or run against fake drivers on any OS, with no
  hypervisor at all.
- **Correctness under failure** — durable work claims, execution grants and
  placement fencing, crash-safe deletes, operations that always terminate —
  exercised by a black-box test suite that uses real `kill -9`.

## Quick start

Two ways to run it, simplest first.

### 1. No hypervisor, any OS (fake drivers)

Needs only Go 1.26+. The first run downloads a pinned embedded PostgreSQL, so
run it from a normal (non-admin) shell.

```
make dev
```

This starts embedded Postgres + the control-plane + a host agent (two logical
nodes) and prints the address to point the CLI at:

```
export VMCTL_SERVER=127.0.0.1:<port>     # PowerShell: $env:VMCTL_SERVER='127.0.0.1:<port>'
```

Now use the operations below (`bin/vmctl ...`).

### 2. Real KVM, one command

Inside the substrate VM — an Ubuntu VM under VirtualBox with nested KVM; bring
it up with `vagrant up` in [deploy/vagrant](deploy/vagrant) — as the
unprivileged service user:

```
cd ~/vm-control-plane
./scripts/demo/01-real-kvm.sh
```

It boots a real Ubuntu guest through the whole control plane, SSHes into it,
and deletes it:

```
== create a real VM ==       real-1  RUNNING  node-a
guest IP: 192.168.221.74
REAL-KVM GUEST REACHED: real-1 / 6.8.0-136-generic
== demo complete ==
```

A distributed variant — control plane + Postgres on the host, agent in the VM
over an SSH tunnel — is [scripts/demo/02-distributed.ps1](scripts/demo/02-distributed.ps1).

## Operations

`bin/vmctl` talks to the control plane at `$VMCTL_SERVER` (default
`127.0.0.1:7070`; override per command with `--server host:port`). Creates,
power changes, and deletes are **asynchronous** — they return an operation id
you can wait on.

```
# create a VM (async; prints an operation id)
bin/vmctl create vm web-1 --cpu 2 --memory 2GiB --image ubuntu-24.04 --disk 10GiB
#   also: --network <tenant-net>   --idempotency-key <uuid> (replay a prior attempt)

# wait for / inspect an operation
bin/vmctl op wait <operation-id> --timeout 4m
bin/vmctl op get  <operation-id>

# see state — REVISION is applied/desired; watch it converge
bin/vmctl list vms
bin/vmctl get  vm web-1

# power (stop/start is the mutable part of the spec in v0.1)
bin/vmctl stop  vm web-1
bin/vmctl start vm web-1

# delete (tombstone → reconciled teardown → finalize)
bin/vmctl delete vm web-1
```

Sizes accept `MiB` / `GiB` (e.g. `2GiB`) or raw bytes.

## Crash safety

Recovery is demoable, not aspirational — the black-box suite kills the
controller mid-transition and mid-delete and recovers by restart alone:

```
go test ./internal/e2e/ -v
```

## Docs

- **Manual (Google Doc):** https://docs.google.com/document/d/1NpBb0K4TJ9w_39VXpmlwwpBxBypILRHIcVq4iQ5F_xc/edit
- **Design walkthrough** — one request in 10 steps: [docs/design.md](docs/design.md)
- **Implementation plan** — state model, PR-by-PR build order, testing: [docs/implementation-plan.md](docs/implementation-plan.md)
- **Decision records:** [docs/adr/](docs/adr/)
- **Real-hardware findings:** [docs/reviews/real-substrate-findings.md](docs/reviews/real-substrate-findings.md)

## License

MIT — see [LICENSE](LICENSE).
