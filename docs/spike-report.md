# Substrate spike report — Tier 1 (Ubuntu VM under VirtualBox)

**Status: GO** — 16/16 checks passed on 2026-08-12 inside the Vagrant-provisioned
substrate VM (`deploy/vagrant`). Every real-substrate assumption is verified;
M3+ real-driver work is unblocked.

## Environment (verified)

| Fact | Value |
|---|---|
| Host | Windows 11, VirtualBox 7.2.8, Hyper-V OFF |
| Guest | Ubuntu 24.04, kernel 6.8.0-53-generic, 8 vCPU / 16 GiB, nested VT-x on |
| /dev/kvm | present and accelerating (KVM, not TCG) |
| libvirt | 10.0.0 (`qemu:///system` reachable under the service identity) |
| qemu-img | 8.2.2 |
| Open vSwitch | 3.3.4, kernel datapath (module present — no userspace fallback needed) |
| Go | 1.26.5 linux/amd64 |
| Service identity | `vagrant` in groups `kvm`, `libvirt`, `vmc` |

## Results (16/16 PASS)

| Check | Proves |
|---|---|
| kvm-device-rw, kvm-accel | /dev/kvm usable; `domain type='kvm'` (hard fail on TCG avoided) |
| libvirt-connect | system socket reachable under the service UID |
| domain-define/start/destroy/undefine | full domain lifecycle round-trip |
| qemu-img-base/overlay/size | explicit -F + explicit size honored (2 GiB overlay) |
| ovs-bridge, ovs-owner-readback | bridge creation + external_ids ownership stamp in one transaction |
| ovs-restart-perms | OVSDB socket access survives `systemctl restart openvswitch-switch` (the drop-in works) |
| mgmt-net-define, mgmt-net-start | NAT management network define + start |
| golibvirt-probe | the connection-supervisor design (D8) verified against the exact library: a DomainDefineXML mutation lands while its response is withheld (the ambiguous-outcome window), the poisoned transport unblocks the call, and a fresh connection re-observes |

## Decision

GO. The real-driver PRs proceed against this verified substrate. The OVS
kernel datapath is available (better than the WSL2 plan would have offered),
so no experimental userspace-netdev fallback is needed here. Full run logs
(per-check command, output, exit status) are retained under the spike's
/tmp/vmc-spike-<run>/ log directory on the substrate VM.

## Reproduce

```
cd deploy/vagrant && vagrant up          # provisions the substrate VM
vagrant ssh                              # you are the `vagrant` service user
cd ~/vm-control-plane
./scripts/spike/spike.sh                 # this battery
```
