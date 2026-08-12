# ADR-0005: Networking — host singletons, VLAN topology, deterministic identity, precise isolation claims

**Status:** Accepted · **Date:** 2026-08-12

## Context

Tenant isolation must be *provable*, guest addressing must make the SSH demo work without building an SDN, and the port lifecycle must respect a truth libvirt imposes: tap devices do not exist until QEMU starts, so pre-creating OVS ports is backwards.

## Decision

- **Topology:** one OVS integration bridge (`vmc-int`) per physical host; logical networks are **unique VLAN tags** on it. Host singletons (`vmc-int`, the `vmc-mgmt` NAT network, the image cache) are ensured once per host by the **host-lock holder** (ADR-0004) and stamped with ownership; a same-name object without our stamp is a conflict, never adopted.
- **Node vs host identity:** scheduler nodes are logical partitions advertised by the **single host daemon** (ADR-0004); the substrate host is physical. Logical nodes share host singletons; VM-scoped artifacts remain per-node+epoch. Bridge creation and its ownership stamp are **one atomic OVSDB transaction** (no created-but-unstamped window), and the `vmc-mgmt` libvirt network carries its deterministic UUID and ownership metadata in the define-time XML.
- **Ports are attached by libvirt at domain start** (`<interface type='bridge'>` + `<virtualport type='openvswitch'/>` + `<vlan><tag/></vlan>`). The network driver manages bridges and *verifies* ports after start.
- **Deterministic identity:** MAC and OVS `interfaceid` derive from VM UUID + NIC index (libvirt randomizes otherwise); `external_ids` carry node/VM/epoch. The crash window between port creation and stamping is closed by a **narrow adoption proof**: stamp retroactively only when the port hangs off our owned live domain with the exact expected bridge/tap/MAC/interfaceid/VLAN; anything else is a conflict.
- **Management plane:** `vmc-mgmt` is **NAT with guest-to-guest isolation** — `<port isolated='yes'/>` blocks guest peers but *not* host access or egress (libvirt formatnetwork/formatdomain semantics; stated accurately, not oversold). Its dnsmasq provides DHCP + host-reachable SSH with zero addressing code. Tenant NICs get static IPs from DB-backed IPAM via cloud-init, no default route.
- **Datapath by capability detection:** kernel module first (the WSL2 6.6 kernel config ships `CONFIG_OPENVSWITCH=m`); experimental userspace `netdev` only after a TUN/TAP smoke test. Same detection in CI.

## Isolation test battery (positive *and* negative)

Same-VLAN ping succeeds; cross-VLAN on the **same subnet** fails (a separate-subnets "test" would pass with tagging broken); host→guest SSH succeeds; mgmt guest-to-guest fails; wrong-tag drift is repaired; `ofport` valid and `Interface.error` empty.

## Consequences

- VLAN isolation is demonstrable on one bridge with OVSDB inspection — an honest, verifiable claim.
- Cross-host tenant traffic is out of scope with a documented growth path (VLAN trunk or VXLAN tunnel ports).

## Alternatives considered

- Per-network bridges: makes VLAN tags redundant and the isolation demo vacuous; rejected.
- Agent-managed persistent taps: a second lifecycle to get wrong; rejected in favor of libvirt-attached ports.
