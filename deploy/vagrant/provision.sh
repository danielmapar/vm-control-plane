#!/usr/bin/env bash
# Root provisioner: installs the KVM/OVS/qcow2 substrate and Go toolchain,
# sets up the `vagrant` service identity, and detects the OVS datapath.
# Idempotent — safe to re-run with `vagrant provision`.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

echo "provision: /dev/kvm check"
if [ ! -e /dev/kvm ]; then
  echo "PROVISION-FAIL: /dev/kvm absent — nested VT-x is not reaching the guest." >&2
  echo "  Ensure Hyper-V/VBS is OFF on the host and the VM has --nested-hw-virt on." >&2
  exit 1
fi

echo "provision: packages"
apt-get update -q
apt-get install -qy \
  qemu-system-x86 qemu-utils \
  libvirt-daemon-system libvirt-clients \
  openvswitch-switch \
  cloud-image-utils genisoimage \
  curl jq openssh-client build-essential

echo "provision: Go toolchain"
GO_VERSION=1.26.5
if ! command -v go >/dev/null || [ "$(go version 2>/dev/null | grep -o 'go1\.[0-9]*' || true)" != "go1.26" ]; then
  arch=amd64
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz" -o /tmp/go.tgz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tgz
  echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' > /etc/profile.d/go.sh
  chmod +x /etc/profile.d/go.sh
fi

echo "provision: groups + service identity"
groupadd -f vmc
usermod -aG kvm,libvirt,vmc vagrant

echo "provision: services"
systemctl enable --now libvirtd
systemctl enable --now openvswitch-switch

echo "provision: persistent OVSDB socket access for the vmc group"
mkdir -p /etc/systemd/system/ovsdb-server.service.d
cat >/etc/systemd/system/ovsdb-server.service.d/vmc-socket-perms.conf <<'EOF'
[Service]
ExecStartPost=/bin/sh -c 'chgrp -R vmc /var/run/openvswitch && chmod -R g+rwx /var/run/openvswitch'
EOF
systemctl daemon-reload
systemctl restart ovsdb-server openvswitch-switch || true

echo "provision: OVS datapath detection"
if modprobe openvswitch 2>/dev/null; then
  echo kernel > /var/lib/vmc-datapath
  echo "provision: OVS kernel datapath available"
else
  if [ -e /dev/net/tun ]; then
    echo netdev > /var/lib/vmc-datapath
    echo "provision: using userspace netdev datapath"
  else
    echo "PROVISION-FAIL: no openvswitch module and no /dev/net/tun" >&2
    exit 1
  fi
fi

echo "provision: storage root"
mkdir -p /var/lib/vmc
chown vagrant:vmc /var/lib/vmc
chmod 2775 /var/lib/vmc

echo "provision: OK"
