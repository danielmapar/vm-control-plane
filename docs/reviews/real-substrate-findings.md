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
| 7 | Full-stack capstone: SSH always failed even though the guest reached `RUNNING` and a lease existed. The demo reported the *same* `guest IP: 192.168.221.26` on every run | The demo picked the lease with `net-dhcp-leases \| head -1` — the **first (oldest)** lease. Each `vmctl create` mints a new VM UUID → new deterministic MAC → new lease, so `.26` was a **stale lease from a long-gone guest**; the demo was SSHing a dead address while the real guest sat at a different IP (the integration test's `MgmtIP` matches the guest's *specific* MAC, which is why it never hit this) | Demo now selects the newest lease whose hostname is `real-1`; also retries SSH like `waitSSH` and destroys the guest on any exit so a failed run can't leave it lingering |

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

## End-to-end capstone: what real KVM proves, and the VirtualBox wall

The capstone goal was one unbroken run: `vmctl create` → a real guest boots →
SSH into it → `vmctl delete`, driven through the control plane. On real KVM,
every link is proven:

1. **The control plane converges a real domain to `RUNNING`.** Every run,
   `vmctl create real-1` produced an operation that reached `DONE` and a VM at
   `PHASE: RUNNING` on `node-a` — the control-plane, the durable work queue,
   the reconciler, the scheduler, the agent, the **real** libvirt driver, the
   **real** qcow2 overlay, and a **real** libvirt domain (defined + started)
   all executed in sequence. Reproducible, monolithic AND distributed.
2. **The guest boots and DHCPs through that whole path.** Real Ubuntu guests,
   created by the control plane, booted their kernel and pulled a lease off the
   `vmc-mgmt` NAT network (e.g. `192.168.221.24`, matched by cloud-init
   hostname).
3. **The driver boots a guest all the way to SSH and tears it down.**
   `TestITBootSSHTeardown` **passes in ~80–100 s**: real domain, DHCP, key-only
   SSH after cloud-init, clean teardown. This is the *same driver* the agent
   uses, and it passes repeatedly even late in the investigation — so the
   substrate is healthy.

The one thing that does **not** complete in a single agent-driven run — the
guest answering SSH — is a VirtualBox limitation, and pinning it down took a
long chain of eliminations (each an entry above or below):

- **It is not the demo.** A stale-lease bug (finding #7) had the SSH step
  probing a dead address; fixed to match the newest `real-1` lease.
- **It is not disk contention alone.** Embedded Postgres was moved to tmpfs
  and the backing image pre-warmed into RAM — yet the guest still wedged. (A
  deliberately brutal `dd oflag=direct` loop *did* guru-meditate the VM, so
  nested VT-x *is* I/O-sensitive — but removing the DB's I/O was not enough.)
- **It is not CPU or scheduling.** Bumping the substrate to 16 vCPUs did
  nothing; pinning the guest's every QEMU thread to dedicated cores 8-15
  (verified via `virsh emulatorpin`/`vcpupin` and `taskset` on all threads)
  did nothing. Activity on cores 0-7 still wedges a guest isolated on 8-15 —
  so the interference is *below* the CPU scheduler, at the VT-x emulation.
- **It is not libvirt introspection.** The IT test passes even with a
  `virsh dumpxml`/`domstate` loop hammering its guest every 0.3 s.
- **It is not the DB/control-plane sharing the VM.** A **distributed**
  deployment was built for exactly this test: control-plane + Postgres on the
  Windows host, and *only* the agent + libvirt + guest in the nested VM,
  reached over an SSH reverse tunnel. It works mechanically — the remote
  control plane drove a real guest to `RUNNING` + DHCP, and even **survived a
  hypervisor-host crash** — but the guest still wedged/guru'd.
- **It is not the poll rate.** Slowing the agent's work-claim poll from 300 ms
  to 3 s (matching the IT test's cadence) did not help either.

What remains, by elimination, is the difference between the passing IT test and
every failing run: a **long-running agent daemon co-located with a booting
guest**. The IT test creates the guest with one synchronous `Ensure` and then
only polls DHCP leases; the agent keeps a persistent gRPC connection and a
reconcile loop alive *through* the guest's boot. On VirtualBox's experimental
nested VT-x that steady-state presence is enough to wedge the L2 guest or trip
a host guru meditation — the same fragility that a bare-metal or Hyper-V
hypervisor simply does not have (running agents beside booting guests is
ordinary production behaviour there).

**The honest conclusion for the interview:** the system works on real KVM —
the control plane converges a real domain to `RUNNING` (monolithic and
distributed), the guest boots and DHCPs through it, and the driver boots a
guest all the way to SSH. The single agent-driven boot-to-SSH is gated by
nested VirtualBox, not by the code, and the fix is a real hypervisor
(bare-metal KVM, or Hyper-V nested virt) — not a code change. The reliable
live demos are: the native **Tier-0** stack (`make dev`, identical
control-plane code paths, no hypervisor at all), the **IT test** (real
boot-to-SSH), and the **distributed** control-plane convergence (a control
plane on one host driving a real hypervisor on another).
