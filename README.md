# vm-control-plane

A small, production-shaped VM control plane in Go: gRPC API → PostgreSQL desired
state → reconciler → scheduler → hypervisor agent → libvirt/KVM, with Open
vSwitch networking and qcow2 storage. It follows the common shape of
libvirt-based compute platforms (the OpenStack Nova / KubeVirt lineage): a
state-owning control plane, a per-host agent wrapping the hypervisor, and
level-triggered reconciliation between them.

```
vmctl create vm web-1 --image ubuntu-24.04 --cpu 2 --memory 2GiB \
      --network production-a --volume workspace:10GiB
```

The point of the project is the part most demos skip: the failure
interleavings. Duplicate work claims, agents acting before their
acknowledgments commit, stale agent processes, deletes racing slow provisions,
old placements deleting new placements' disks — each is a named protocol with a
named failure-injection test. Start with
[docs/design.md](docs/design.md) (a 10-step walkthrough of one request),
then [docs/implementation-plan.md](docs/implementation-plan.md) for the state
model, PR-by-PR build order, and testing strategy. Decision records live in
[docs/adr/](docs/adr/).

## Status

Tier 0 (fake drivers) is COMPLETE and runs on Windows/macOS/Linux: full
lifecycle, scheduling across logical nodes, execution grants and fencing,
drift detection with one-path repair, node-loss policy, durable claims,
semantic idempotency, and operations that always terminate — black-box
tested with real processes and real kill -9. The real-substrate drivers
(libvirt/OVS/qcow2 executors) land against the verified Linux substrate (an Ubuntu VM under VirtualBox with nested VT-x) per
the plan's harness-before-feature gate (§9); their portable cores
(domain XML, seed ISO, qcow2 command logic) are already merged and tested.
The PR history is meant to be read as a course — see the plan's §9.

## Quickstart (Tier 0 — fake drivers, no hypervisor required)

> Requires only Go 1.26+ ("no preinstalled services": the first run
> downloads a pinned embedded PostgreSQL once; run from a non-elevated
> shell).

```
make dev     # embedded Postgres + control-plane + host agent (2 logical nodes)
bin/vmctl create vm demo-1 --cpu 1 --memory 512MiB
bin/vmctl op wait <operation-id>
bin/vmctl list vms          # REVISION column: applied/desired — watch it converge
bin/vmctl stop vm demo-1    # the only mutable spec field (v0.1)
bin/vmctl delete vm demo-1  # tombstone → reconciled teardown → finalize
```

Crash-safety is demoable, not aspirational: run the black-box suite —

```
go test ./internal/e2e/ -v    # incl. controller kill -9 mid-transition and
                              # crash-mid-delete, recovered by restart alone
```

Real-KVM tiers (an Ubuntu VM under VirtualBox / CI) are described in the plan, §8.

## AI collaboration

This project was built as a documented AI-pair-programming exercise. The
implementation plan and ADRs record every design decision and its rationale;
an AI reviewer examines every PR, and each PR description records which of its
findings were accepted, which were rejected, and why. The triage record — not
any review score — is the evidence of engineering judgment.

## License

MIT — see [LICENSE](LICENSE).
