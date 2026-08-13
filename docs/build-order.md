# The road to the demo

This system was built as a stack of 30 small pull requests. Read them in order
and you watch it come together, from an empty repo to a real virtual machine
booting under KVM and answering SSH.

Every PR is already in `main`. They are closed, but kept along with their
`pr/NN-*` branches, so the build order stays readable. An AI reviewer looked at
each change; the `review:` PRs fold the accepted findings back in, and the files
under [reviews/](reviews/) record which findings were taken, which were
rejected, and why.

Start at the top and read down.

## 1. Foundation: API, store, CLI

An empty repo becomes a working control plane you can already drive from the
command line, backed by fake drivers.

- [01. build: repo scaffolding, CI lanes, lint, toolchain bootstrap](https://github.com/sigtunnel/vm-control-plane/pull/1)
- [02. spike: WSL2 substrate go/no-go scripts and go-libvirt probe](https://github.com/sigtunnel/vm-control-plane/pull/2)
- [03. proto: vmc.v1 core — VirtualMachine, Operation, VMService, OperationService](https://github.com/sigtunnel/vm-control-plane/pull/3)
- [04. store: embedded-Postgres harness, migrations, VM repository with CAS](https://github.com/sigtunnel/vm-control-plane/pull/4)
- [05. store: operations that always terminate; idempotency envelopes for every verb](https://github.com/sigtunnel/vm-control-plane/pull/5)
- [06. api: VMService + OperationService over the store](https://github.com/sigtunnel/vm-control-plane/pull/6)
- [07. cli: vmctl — create/get/list/delete/op over the public API](https://github.com/sigtunnel/vm-control-plane/pull/7)
- [08. review: apply codex triage for PRs 2-3 (recorded in docs/reviews)](https://github.com/sigtunnel/vm-control-plane/pull/8)

## 2. The concurrency engine (Tier 0 complete)

The hard part: durable work claims, scheduling with placement fencing, execution
grants, a level-triggered reconciler, drift repair, and deletes that survive a
crash. By the end, the full fake-driver lifecycle works and is black-box tested
with real `kill -9`.

- [09. reconciler: durable token-based claim queue with lease-aware commits](https://github.com/sigtunnel/vm-control-plane/pull/9)
- [10. faults: failpoint registry with a drift-proof manifest](https://github.com/sigtunnel/vm-control-plane/pull/10)
- [11. nodes: logical partitions, host binding, session generations, quota sums](https://github.com/sigtunnel/vm-control-plane/pull/11)
- [12. scheduler: filter/score proposals; placements as a schema-fenced ledger](https://github.com/sigtunnel/vm-control-plane/pull/12)
- [13. grants: admission-checked execution grants — the exposure proof](https://github.com/sigtunnel/vm-control-plane/pull/13)
- [14. agent: intent protocol, execution daemon, fake compute driver](https://github.com/sigtunnel/vm-control-plane/pull/14)
- [15. reconciler: the level-triggered loop — scan, transition, converge](https://github.com/sigtunnel/vm-control-plane/pull/15)
- [16. e2e: Tier-0 supervisor and the black-box vertical slice](https://github.com/sigtunnel/vm-control-plane/pull/16)
- [17. review: apply codex triage for PRs 4-8 (recorded in docs/reviews)](https://github.com/sigtunnel/vm-control-plane/pull/17)
- [18. power: UpdateVmPower — the only mutable spec field, end to end](https://github.com/sigtunnel/vm-control-plane/pull/18)
- [19. drift + node loss: resync evidence, one-path repair, the reschedule rule](https://github.com/sigtunnel/vm-control-plane/pull/19)
- [20. deletion hardening: schema-enforced invariants; Tier 0 complete](https://github.com/sigtunnel/vm-control-plane/pull/20)

## 3. Portable driver cores

The parts a real hypervisor needs, written as pure logic and golden-tested on any
OS before they touch real hardware.

- [21. domainxml: the hand-built domain definition — identity as code](https://github.com/sigtunnel/vm-control-plane/pull/21)
- [22. seed: pure-Go cloud-init NoCloud ISO — identity injection, verified](https://github.com/sigtunnel/vm-control-plane/pull/22)
- [23. qcow2: the volume driver's portable core — sharp edges as tests](https://github.com/sigtunnel/vm-control-plane/pull/23)
- [24. docs: quickstart is live — Tier 0 complete](https://github.com/sigtunnel/vm-control-plane/pull/24)

## 4. Real substrate, real KVM

A Linux VM with nested KVM, the real libvirt/qcow2 drivers behind it, and the
end-to-end demo: create a VM through the control plane, watch a real guest boot,
SSH into it, and delete it.

- [25. substrate: Tier 1 moves from WSL2 to an Ubuntu VM under VirtualBox](https://github.com/sigtunnel/vm-control-plane/pull/25)
- [26. review: apply codex batch triage for PRs 9-25 (concurrency correctness)](https://github.com/sigtunnel/vm-control-plane/pull/26)
- [27. substrate: Vagrant VM + real libvirt/qcow2 drivers (linux-tagged)](https://github.com/sigtunnel/vm-control-plane/pull/27)
- [28. spike: GO — 16/16 real-substrate checks pass in the VirtualBox VM](https://github.com/sigtunnel/vm-control-plane/pull/28)
- [29. real-driver: fixes found by running against real KVM (boot-to-SSH passes)](https://github.com/sigtunnel/vm-control-plane/pull/29)
- [30. capstone: real-KVM end-to-end demo + the bugs running it surfaced](https://github.com/sigtunnel/vm-control-plane/pull/30)

## Ready

The demo runs in one command. Inside the substrate VM,
`scripts/demo/01-real-kvm.sh` boots a real KVM guest through the whole control
plane, reaches it over SSH, and deletes it. The capstone was finished on `main`
after PR 30; the story of the real-hardware bugs it took to get there is in
[reviews/real-substrate-findings.md](reviews/real-substrate-findings.md).
