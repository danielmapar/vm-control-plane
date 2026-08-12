#!/usr/bin/env bash
# WSL2 substrate setup — asserts its preconditions instead of assuming them.
# Run inside the WSL2 Ubuntu 24.04 distro as the user who will run the agent
# (NOT root; sudo is used selectively).
#
# Group membership does not apply to the current login: after the first run
# reports "re-login required", start a new WSL shell and run this script
# again — the second run verifies effective identity and finishes.
set -euo pipefail

fail() { echo "SETUP-FAIL: $*" >&2; exit 1; }
note() { echo "setup: $*"; }

ME="$(id -un)"
[ "$(id -u)" -ne 0 ] || fail "run as the service user, not root"

# systemd must be PID 1 for libvirtd/openvswitch units.
[ "$(ps -p 1 -o comm=)" = "systemd" ] \
  || fail "systemd is not PID 1 — add [boot]\\nsystemd=true to /etc/wsl.conf, then 'wsl --shutdown' and retry"

# Nested virtualization must expose /dev/kvm.
[ -e /dev/kvm ] \
  || fail "/dev/kvm missing — ensure nestedVirtualization=true in %UserProfile%\\.wslconfig, then 'wsl --shutdown'"

# Go toolchain (the probe and the agent run under this identity).
command -v go >/dev/null || fail "Go not installed in WSL — 'sudo snap install go --classic' or apt; need 1.26+"

note "installing packages"
sudo apt-get update -q
sudo DEBIAN_FRONTEND=noninteractive apt-get install -qy \
  qemu-system-x86 qemu-utils \
  libvirt-daemon-system libvirt-clients \
  openvswitch-switch \
  cloud-image-utils genisoimage \
  curl jq openssh-client

# --- Groups ------------------------------------------------------------------
sudo groupadd -f vmc
sudo usermod -aG kvm,libvirt,vmc "$ME"

# --- Services ----------------------------------------------------------------
sudo systemctl enable --now libvirtd
sudo systemctl enable --now openvswitch-switch

# --- Persistent OVSDB socket access for the service user ---------------------
# libvirt group membership grants NOTHING on OVS, and run-dir permissions
# reset on daemon restart — a drop-in re-applies them after every start.
sudo mkdir -p /etc/systemd/system/ovsdb-server.service.d
sudo tee /etc/systemd/system/ovsdb-server.service.d/vmc-socket-perms.conf >/dev/null <<'EOF'
[Service]
ExecStartPost=/bin/sh -c 'chgrp -R vmc /var/run/openvswitch && chmod -R g+rwx /var/run/openvswitch'
EOF
sudo systemctl daemon-reload
sudo systemctl restart ovsdb-server openvswitch-switch

# --- Datapath capability detection (never assume) ----------------------------
if sudo modprobe openvswitch 2>/dev/null; then
  note "OVS kernel datapath available (module loaded)"
  echo kernel | sudo tee /var/lib/vmc-datapath >/dev/null
else
  note "no openvswitch module — userspace netdev datapath (experimental) after TUN/TAP check"
  [ -e /dev/net/tun ] || fail "no /dev/net/tun — userspace datapath impossible; spike is a NO-GO for OVS"
  echo netdev | sudo tee /var/lib/vmc-datapath >/dev/null
fi

# --- Storage root (WSL-native filesystem, never /mnt/c) ----------------------
sudo mkdir -p /var/lib/vmc
sudo chown "$ME":vmc /var/lib/vmc
sudo chmod 2775 /var/lib/vmc   # setgid: agent + qemu group share

case "$(pwd)" in
  /mnt/c/*) fail "repo checkout is under /mnt/c — clone to the WSL filesystem (~/) before running the demo" ;;
esac

# --- Effective-identity verification (second run finishes here) --------------
if id -nG "$ME" | tr ' ' '\n' | grep -qx kvm && \
   id -nG | tr ' ' '\n' | grep -qx kvm; then
  # Current shell already has the groups: verify the substrate touchpoints.
  [ -r /dev/kvm ] && [ -w /dev/kvm ] || fail "/dev/kvm not accessible even with kvm group — check udev"
  virsh -c qemu:///system version >/dev/null || fail "libvirt system socket not accessible under $ME"
  ovs-vsctl show >/dev/null || fail "OVSDB not accessible under $ME (socket perms drop-in failed?)"
  note "OK — identity verified; run scripts/spike/spike.sh"
else
  note "groups updated but NOT active in this shell — open a NEW WSL shell and run this script once more"
fi
