# Networking, one layer at a time

**Goal.** Trace a guest packet and repair the failing hop without removing host protection. Keep the chapter-1 default NAT path as the known-good control.

**Read first.**  [libvirt network XML](https://libvirt.org/formatnetwork.html), Ubuntu’s host networking guide for the installed release, then  [OVS/libvirt integration](https://docs.openvswitch.org/en/latest/howto/libvirt/) for the final sublab.

**Quick model.** NAT, isolation and an external bridge differ in who supplies addressing, forwarding and access control. Keep default NAT as the control while changing one hop.

![Guest traffic crosses its tap, the host bridge and host NAT. dnsmasq supplies DHCP and DNS; a second host has a separate default network.](assets/image6.png)

*Figure 2. Trace the packet before changing a bridge or firewall rule. The first lab deliberately uses host-local NAT.*

## 3.1 Trace NAT

1. Draw linux-01’s current path: guest NIC → virtio → TAP → libvirt bridge/NAT/firewall → host uplink, and the reverse path. Record sudo virsh net-dumpxml default, domiflist linux-01, host ip -brief link/address, ip route and guest ip route/resolvectl status. Test guest-to-host, IP-to-Internet and DNS resolution separately.

2. Expected: default network supplies DHCP/DNS and NAT when enabled; it does not make the guest directly reachable from the outside LAN.

**Check:** The NAT packet path, addressing and DNS behavior match host and guest observations.

## 3.2 Add isolation

1. Define a separate **isolated** libvirt test network on a non-overlapping private subnet from a reviewed network XML example. Attach a disposable test guest, not linux-01, and show that same-network traffic works while uplink access is absent. Record which component supplied addresses. Restore the test guest to NAT and verify the known-good path.

**Check:** The isolated guest reaches its test network without uplink access, then returns to NAT.

## 3.3 Build a bridge

1. On a second, non-management lab NIC or fully recoverable nested host, connect test guests to an existing Linux bridge. First configure the host bridge with Ubuntu’s current network tooling, then attach guests. Libvirt does not create or supply DHCP/DNS for an externally managed bridge.

2. Do not move the sole SSH uplink without local console and a tested rollback. Repeat with one VLAN-separated pair and verify both allowed and denied reachability.

**Check:** Bridge and VLAN tests show both allowed and denied paths without losing host management.

## 3.4 Compare OVS

1. Recreate the bridge experiment with OVS only after the Linux bridge path works. Assign explicit owners for bridge setup, VLAN tagging, DHCP/DNS and filtering. Compare TAP/bridge/OVS observations. Investigate macvtap’s host-to-guest limitation and why a Wi-Fi uplink may rule out a transparent bridge.

**Check:** The OVS comparison identifies each component that owns addressing, tags and filtering.

## 3.5 Diagnose a packet

1. Break DNS on a disposable guest while retaining IP connectivity. Observe that an IP ping or application connection succeeds while name resolution fails. Then create one bounded test-only firewall or MTU fault and locate the hop with guest/host captures or counters. Restore the saved network configuration after each drill.

**Check:** Each induced fault is located at one hop and the original network is restored.

**Pass and cleanup.** Save a packet-path drawing and reproducible NAT, isolation, bridge, VLAN and OVS configurations, or mark hardware-limited work outstanding. Name who owns addressing/filtering on each path. Restore linux-01 NAT/SSH; never disable the host firewall globally.
