# Real-substrate findings — bugs the fake tier could never surface

The libvirt/qcow2 drivers built and unit-tested clean, but running them
against a real Ubuntu guest under KVM (the Vagrant substrate VM) surfaced
seven bugs that only real hardware could show. Each is a genuine
hypervisor-layer gotcha — and one was predicted verbatim by the plan.

| # | Symptom on real hardware | Root cause | Fix |
|---|---|---|---|
| 1 | `EnsureMgmtNetwork` returned "Network not found" instead of creating it | `golibvirt.IsNotFound` matches the DOMAIN-not-found error code only; a network-not-found is a different libvirt code | Treat any lookup failure inside the supervised call as "define it"; recover a define-race by re-lookup |
| 2 | `error creating bridge interface vmcbr-vmc-mgmt-i: Numerical result out of range` | Linux interface names are capped at 15 chars (IFNAMSIZ-1); `vmcbr-` + a truncated network name overran it | Derive the bridge name as `vmcbr-` + 6 hex of a hash (12 chars, collision-resistant) |
| 3 | Idempotent-replay `Ensure` failed on `qemu-img info: exit status 1` | **The QEMU image-locking hazard the plan predicted (D10):** a running QEMU holds the overlay's lock, so a plain `qemu-img info` cannot acquire it | Use `qemu-img info -U` for the read-only ensure-probe — safe because info never mutates; the plan's "never `-U` on a live image" rule is about mutations, not inspection |
| 4 | Idempotent-replay `Ensure` failed on `no-replace publish: file exists` (seed.iso) | `WriteSeed` always tried to publish; on redelivery the deterministic seed already existed and the (correct) no-replace publish refused to clobber | Make `WriteSeed` idempotent: an existing content-deterministic seed is the correct one — return it |
| 5 | Guest booted to a login prompt but ignored the NoCloud seed (hostname stayed `ubuntu`, no DHCP lease) | The Ubuntu **minimal** cloud image does not reliably consume a NoCloud cdrom seed (confirmed by reproducing with the canonical `cloud-localds` tool, which also failed) | Use the standard `noble-server-cloudimg-amd64.img` — full cloud-init with NoCloud — as the demo target and the seed-image default |
| 6 | Full-stack capstone: the agent's `EnsureMgmtNetwork` failed with `Network is already in use by interface vmcbr-…` and the operation hung `PENDING` forever | The libvirt **integration test** brings up its own `vmc-mgmt-it` network but never tore it down; left active it holds `192.168.221.0/24` — the *same* subnet the demo's `vmc-mgmt` uses — so libvirt refuses to start `vmc-mgmt`. A crashed agent then took embedded Postgres down with it, so the control-plane spun on `connection refused` and nothing advanced the op | The IT harness now tears its network down in `t.Cleanup` (LIFO, after domain teardown); the demo starts from a clean network slate |
| 7 | Full-stack capstone: the guest booted and got its lease (`guest IP: 192.168.221.26` — through the whole control plane), but SSH failed on `connect … port 22: connection refused` and the demo aborted | A DHCP lease only means the guest *kernel* is up; `sshd` and the injected key land later in cloud-init. The demo did a **single** SSH attempt and gave up — where the integration test's `waitSSH` retries for two minutes | Demo retries SSH until cloud-init finishes (matching `waitSSH`), and destroys the guest on exit so a failed run can't leave it lingering |

## Why this matters for the interview

Bug #3 is the headline: the plan's storage ADR (D10) said *"qemu-img must
never touch an image a running QEMU holds"* and scoped snapshots to stopped
VMs for exactly this reason — but the ensure-**probe** is read-only, and the
real run proved that even a read-only `qemu-img info` needs `-U` against a
live image. That is the difference between reasoning about a hazard and
meeting it: the design anticipated the lock, and the implementation had to
handle it one layer deeper than the design spelled out.

All seven are the kind of bug a fake driver can never produce — they live in
the seams between the Go code and real libvirt/qemu/Linux. Finding them is
the whole reason the plan insisted on a real-substrate tier rather than
declaring victory on fakes.

## End-to-end capstone: proven in two halves, plus a VirtualBox limitation

The capstone goal was one unbroken script: `vmctl create` → a real guest
boots → SSH into it → `vmctl delete`, all driven through the full control
plane. Every link was demonstrated on real KVM:

1. **The control plane converges a real domain to `RUNNING`.** Across every
   capstone run, `vmctl create real-1` produced an operation that reached
   `DONE` and a VM that reached `PHASE: RUNNING` on `node-a` — meaning the
   control-plane, the durable work queue, the reconciler, the scheduler, the
   agent, the **real** libvirt driver, the **real** qcow2 overlay, and a
   **real** libvirt domain (defined + started) all executed in sequence.
2. **The guest boots and gets a lease through that whole path.** One run
   reached `guest IP: 192.168.221.26` — a real Ubuntu guest, created by the
   control plane, booted its kernel and pulled a DHCP lease off our
   `vmc-mgmt` NAT network.
3. **The driver boots a guest all the way to SSH and tears it down.** The
   focused integration test `TestITBootSSHTeardown` **passes in ~80 s**: real
   domain up, DHCP lease, key-only SSH after cloud-init, clean teardown with
   storage removed. This is the same driver the agent uses.

What could **not** be captured in a single unbroken run is a VirtualBox
limitation, not a control-plane one. Under the capstone's *sustained*
full-stack load, VirtualBox's (experimental) nested VT-x repeatedly hit a
**guru meditation** — a host-level hypervisor fault — while the guest was
running. The mechanism is visible in the traces: a guest left running long
enough (e.g. when a failed SSH step orphaned it) lingered into a nested-virt
fault window and took the outer VM down; the isolated IT test survives
precisely because it boots, SSHes, and reaps the guest in ~80 s. The demo now
mitigates this (retry SSH, then destroy the guest promptly on any exit), but
repeated faults eventually left the substrate VM's own kernel throwing
`rcu_preempt` stalls on boot — the nested hypervisor degraded underneath it.

The lesson is the honest one for the interview: **the system works against
real KVM — proven by the domain reaching `RUNNING` through the full stack and
by the driver booting a guest to SSH — and the remaining gap is that nesting
KVM inside VirtualBox is not a stable substrate for a sustained guest boot.**
Production KVM runs on bare metal for exactly this reason; the reliable live
demo is the native Tier-0 stack (`make dev`, fake drivers, identical
control-plane code paths), with the real-KVM tier standing as the
hardware-level proof.
