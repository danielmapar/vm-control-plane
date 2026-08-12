# Substrate spike report — Tier 1 (Ubuntu VM under VirtualBox)

**Status: PENDING** — blocked on the one-time substrate-VM provisioning
(`scripts/spike/vbox-create.ps1` + Ubuntu install; VirtualBox 7.2.8 and
Hyper-V-off are already verified on the dev machine). Per plan §9, **M3+
PRs do not merge until this report is committed with results**, and the
schedule is re-estimated from it.

## How to produce this report

```
# On Windows: provision the VM (nested VT-x on), install Ubuntu 24.04 + OpenSSH
scripts/spike/vbox-create.ps1 -IsoPath C:/isos/ubuntu-24.04-live-server-amd64.iso

# Inside the VM, as the service user, from a clone on the VM's filesystem:
./scripts/spike/host-setup.sh     # run twice — re-login applies groups
./scripts/spike/spike.sh | tee spike-results.txt
```

## Checks and results

| Check | Expectation | Result |
|---|---|---|
| kvm-device-rw / kvm-accel | /dev/kvm usable; `domain type='kvm'` (hard fail on TCG) | _pending_ |
| libvirt-connect | `qemu:///system` reachable under the service UID | _pending_ |
| domain lifecycle | define/start/destroy/undefine round-trip | _pending_ |
| qemu-img base/overlay/info | explicit `-F` + explicit size honored (2 GiB overlay) | _pending_ |
| ovs-bridge (+datapath detection) | kernel module expected (real Ubuntu kernel); netdev fallback only after TUN/TAP check | _pending_ |
| ovs-external-ids | ownership stamping works under the service UID | _pending_ |
| ovs-restart-perms | OVSDB socket access **survives daemon restart** | _pending_ |
| mgmt NAT network | define/start/destroy/undefine of `vmc-spike-net` | _pending_ |
| golibvirt-probe | exact-library lifecycle + blackholed-call poisoning + re-observe | _pending_ |

## Decisions this report drives

- **GO:** M3a proceeds; schedule re-estimated from measured friction.
- **NO-GO (OVS):** invoke plan §9 failure branch — cut OVS + snapshots, Linux-bridge isolation for demo 4, document the OVS design.
- **NO-GO (KVM, i.e. nested VT-x failed):** verify Hyper-V/VBS is off and --nested-hw-virt was applied; otherwise select the concrete fallback venue (rented Linux host) or degraded-scope matrix; decided here, not deferred.
