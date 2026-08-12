#!/usr/bin/env bash
# WSL2 substrate setup — asserts its preconditions instead of assuming them.
# Run inside the WSL2 Ubuntu 24.04 distro, as the user who will run the agent
# (NOT root). See docs/implementation-plan.md §8 Tier 1.
set -euo pipefail

fail() { echo "SETUP-FAIL: $*" >&2; exit 1; }
note() { echo "setup: $*"; }

# --- Preconditions -----------------------------------------------------------
[ "$(id -u)" -ne 0 ] || fail "run as the service user, not root (sudo is used selectively)"

# systemd must be PID 1 for libvirtd/openvswitch units.
[ "$(ps -p 1 -o comm=)" = "systemd" ] \
  || fail "systemd is not PID 1 — add [boot]\\nsystemd=true to /etc/wsl.conf, then 'wsl --shutdown' and retry"

# Nested virtualization must expose /dev/kvm.
[ -e /dev/kvm ] \
  || fail "/dev/kvm missing — ensure nestedVirtualization=true in %UserProfile%\\.wslconfig, then 'wsl --shutdown'"

# --- Packages ---------------------------------------------------------------
note "installing packages"
sudo apt-get update -q
sudo DEBIAN_FRONTEND=noninteractive apt-get install -qy \
  qemu-system-x86 qemu-utils \
  libvirt-daemon-system libvirt-clients \
  openvswitch-switch \
  cloud-image-utils genisoimage \
  curl jq

# --- Groups (a new login is required for these to apply) ---------------------
sudo usermod -aG kvm,libvirt "$USER"
note "user added to kvm,libvirt — group changes need a fresh login (or 'sg')"

# OVSDB socket access for the non-root service user: libvirt group membership
# grants NOTHING on OVS. Grant via group ownership on the run directory.
sudo groupadd -f vmc
sudo usermod -aG vmc "$USER"
OVS_RUN=/var/run/openvswitch
if [ -d "$OVS_RUN" ]; then
  sudo chgrp -R vmc "$OVS_RUN"
  sudo chmod g+rwx "$OVS_RUN"
  note "OVSDB socket group access granted (verify it survives 'systemctl restart openvswitch-switch' — spike checks this)"
fi

# --- Services ----------------------------------------------------------------
sudo systemctl enable --now libvirtd
sudo systemctl enable --now openvswitch-switch

# --- Datapath capability detection (never assume) ----------------------------
if sudo modprobe openvswitch 2>/dev/null; then
  note "OVS kernel datapath available (module loaded)"
  echo kernel | sudo tee /var/lib/vmc-datapath >/dev/null
else
  note "no openvswitch module — will use userspace netdev datapath (experimental) after TUN/TAP smoke test"
  [ -e /dev/net/tun ] || fail "no /dev/net/tun — userspace datapath impossible; spike is a NO-GO for OVS"
  echo netdev | sudo tee /var/lib/vmc-datapath >/dev/null
fi

# --- Storage root (WSL-native filesystem, never /mnt/c) ----------------------
sudo mkdir -p /var/lib/vmc
sudo chown "$USER":vmc /var/lib/vmc
sudo chmod 2775 /var/lib/vmc   # setgid: agent + qemu group share

case "$(pwd)" in
  /mnt/c/*) fail "repo checkout is under /mnt/c — clone to the WSL filesystem (~/) before running the demo" ;;
esac

note "OK — now run scripts/spike/spike.sh"
