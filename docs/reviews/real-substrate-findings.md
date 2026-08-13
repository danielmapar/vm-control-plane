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

## End-to-end capstone: it works — and the long chase that got there

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

### The resolution: the capstone completes, and the "wedge" was a demo bug

The full single-run capstone **works** — monolithic and distributed:

```
== create a real VM ==   operation ... CREATE vm/real-1 DONE ; real-1 RUNNING node-a
== wait for the mgmt IP (matched by the domain's MAC) ==   guest IP: 192.168.221.74
== SSH into the guest ==   REAL-KVM GUEST REACHED: real-1 / 6.8.0-136-generic
== delete ==   operation ... DELETE vm/real-1 DONE
== demo complete: a real KVM guest was booted, reached over SSH, and deleted
   through the control plane ==
```

The symptom that looked like an unfixable nested-virt "wedge" for a long time —
`guest never answered SSH` — was a **bug in the demo's own lease matching**, not
the hypervisor. The demo matched the guest's DHCP lease by its cloud-init
hostname (`$6 == "real-1"`), but **until cloud-init sets the hostname the lease
shows `-`**, so the match fell back to a *stale* lease from a prior guest and
probed a dead address. The guest was booting fine at its real IP the whole time;
the probe was knocking on the wrong door. This also confounded the elimination
experiments above (16 vCPUs, CPU pinning, tmpfs, poll rate): each concluded
"still wedged" while actually probing a stale lease. The fix is what the
integration test always did — **match the lease by the domain's own MAC**
(`virsh dumpxml` → `52:54:00:…`) — plus clearing the dnsmasq lease file on
cleanup so stale leases can never reappear.

What was **genuinely** VirtualBox's fault is the intermittent **guru
meditation** — a real host-level fault (confirmed via `VBoxManage`, and
reproducible on demand with a brutal `dd oflag=direct` loop, so nested VT-x is
truly I/O-sensitive). It correlated with *accumulated* crashes degrading the
substrate; on a freshly-booted, healthy substrate the demo runs clean. The
**distributed** deployment further de-risks it by keeping the DB/control-plane
load off the nested VM entirely.

**The honest conclusion for the interview:** the system works end-to-end on
real KVM — one command boots a real guest through the whole control plane,
SSHes into it, and deletes it. Three ways to show it, simplest first:
- **Tier-0** (`make dev`, native, no hypervisor) — the control-plane behaviour.
- **Monolithic real-KVM** (`scripts/demo/01-real-kvm.sh`, in the substrate VM) —
  the full capstone in one command.
- **Distributed** (`scripts/demo/02-distributed.ps1`) — control-plane + Postgres
  on one host driving the hypervisor agent on another over a tunnel; it even
  **survives a hypervisor-host crash**.

An `--emulated` (QEMU TCG) path also exists: it needs no nested virtualization
at all (no guru risk), but cloud-init's SSH host-key generation under software
emulation is impractically slow, so it is a fallback, not the demo.
