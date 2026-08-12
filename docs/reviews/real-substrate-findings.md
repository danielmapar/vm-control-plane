# Real-substrate findings — bugs the fake tier could never surface

The libvirt/qcow2 drivers built and unit-tested clean, but running them
against a real Ubuntu guest under KVM (the Vagrant substrate VM) surfaced
four bugs that only real hardware could show. Each is a genuine
hypervisor-layer gotcha — and one was predicted verbatim by the plan.

| # | Symptom on real hardware | Root cause | Fix |
|---|---|---|---|
| 1 | `EnsureMgmtNetwork` returned "Network not found" instead of creating it | `golibvirt.IsNotFound` matches the DOMAIN-not-found error code only; a network-not-found is a different libvirt code | Treat any lookup failure inside the supervised call as "define it"; recover a define-race by re-lookup |
| 2 | `error creating bridge interface vmcbr-vmc-mgmt-i: Numerical result out of range` | Linux interface names are capped at 15 chars (IFNAMSIZ-1); `vmcbr-` + a truncated network name overran it | Derive the bridge name as `vmcbr-` + 6 hex of a hash (12 chars, collision-resistant) |
| 3 | Idempotent-replay `Ensure` failed on `qemu-img info: exit status 1` | **The QEMU image-locking hazard the plan predicted (D10):** a running QEMU holds the overlay's lock, so a plain `qemu-img info` cannot acquire it | Use `qemu-img info -U` for the read-only ensure-probe — safe because info never mutates; the plan's "never `-U` on a live image" rule is about mutations, not inspection |
| 4 | Idempotent-replay `Ensure` failed on `no-replace publish: file exists` (seed.iso) | `WriteSeed` always tried to publish; on redelivery the deterministic seed already existed and the (correct) no-replace publish refused to clobber | Make `WriteSeed` idempotent: an existing content-deterministic seed is the correct one — return it |
| 5 | Guest booted to a login prompt but ignored the NoCloud seed (hostname stayed `ubuntu`, no DHCP lease) | The Ubuntu **minimal** cloud image does not reliably consume a NoCloud cdrom seed (confirmed by reproducing with the canonical `cloud-localds` tool, which also failed) | Use the standard `noble-server-cloudimg-amd64.img` — full cloud-init with NoCloud — as the demo target and the seed-image default |

## Why this matters for the interview

Bug #3 is the headline: the plan's storage ADR (D10) said *"qemu-img must
never touch an image a running QEMU holds"* and scoped snapshots to stopped
VMs for exactly this reason — but the ensure-**probe** is read-only, and the
real run proved that even a read-only `qemu-img info` needs `-U` against a
live image. That is the difference between reasoning about a hazard and
meeting it: the design anticipated the lock, and the implementation had to
handle it one layer deeper than the design spelled out.

All four are the kind of bug a fake driver can never produce — they live in
the seams between the Go code and real libvirt/qemu/Linux. Finding them is
the whole reason the plan insisted on a real-substrate tier rather than
declaring victory on fakes.
