#!/usr/bin/env bash
# Go/no-go spike: proves the real-substrate assumptions on THIS machine under
# the exact service identity, before any real-driver PR merges (plan §8/§9).
# Produces docs/spike-report.md content on stdout; commit the results.
set -uo pipefail

PASS=(); FAIL=()
check() { # name, command...
  local name=$1; shift
  if "$@" >/tmp/spike-last.log 2>&1; then
    PASS+=("$name"); echo "PASS  $name"
  else
    FAIL+=("$name"); echo "FAIL  $name  ($(tail -1 /tmp/spike-last.log))"
  fi
}

echo "== vm-control-plane substrate spike =="
echo "identity: $(id)"
echo

# 1. KVM acceleration — hard fail on TCG.
check "kvm-device-rw" test -r /dev/kvm -a -w /dev/kvm
check "kvm-accel" bash -c "virsh -c qemu:///system capabilities | grep -q \"domain type='kvm'\""

# 2. libvirt access under this UID (system socket, not session).
check "libvirt-connect" virsh -c qemu:///system version

# 3. Trivial domain define/start/destroy/undefine cycle (no disk).
cat >/tmp/spike-dom.xml <<'EOF'
<domain type='kvm'>
  <name>vmc-spike</name>
  <memory unit='MiB'>128</memory>
  <vcpu>1</vcpu>
  <os><type arch='x86_64'>hvm</type><boot dev='hd'/></os>
  <devices><console type='pty'/></devices>
</domain>
EOF
check "domain-define" virsh -c qemu:///system define /tmp/spike-dom.xml
check "domain-start" virsh -c qemu:///system start vmc-spike
check "domain-destroy" virsh -c qemu:///system destroy vmc-spike
check "domain-undefine" virsh -c qemu:///system undefine vmc-spike

# 4. qcow2 mechanics with explicit size + backing format.
check "qemu-img-base" qemu-img create -f qcow2 /tmp/spike-base.qcow2 1G
check "qemu-img-overlay" qemu-img create -f qcow2 -b /tmp/spike-base.qcow2 -F qcow2 /tmp/spike-overlay.qcow2 2G
check "qemu-img-info" bash -c "qemu-img info --output=json --backing-chain /tmp/spike-overlay.qcow2 | jq -e '.[0].\"virtual-size\" == 2147483648'"

# 5. OVS under this UID with the detected datapath.
DP=$(cat /var/lib/vmc-datapath 2>/dev/null || echo kernel)
if [ "$DP" = netdev ]; then
  check "ovs-bridge" sudo -n false 2>/dev/null && echo "skip" || \
    ovs-vsctl -- add-br vmc-spike-br -- set bridge vmc-spike-br datapath_type=netdev
else
  check "ovs-bridge" ovs-vsctl add-br vmc-spike-br
fi
check "ovs-external-ids" ovs-vsctl set bridge vmc-spike-br external_ids:vmc-owner=spike
check "ovs-restart-perms" bash -c "sudo systemctl restart openvswitch-switch && sleep 2 && ovs-vsctl list-br >/dev/null"
ovs-vsctl del-br vmc-spike-br 2>/dev/null

# 6. Management NAT network (defined by us, not libvirt's default).
cat >/tmp/spike-net.xml <<'EOF'
<network>
  <name>vmc-spike-net</name>
  <forward mode='nat'/>
  <ip address='192.168.213.1' netmask='255.255.255.0'>
    <dhcp><range start='192.168.213.10' end='192.168.213.99'/></dhcp>
  </ip>
</network>
EOF
check "mgmt-net-define" virsh -c qemu:///system net-define /tmp/spike-net.xml
check "mgmt-net-start" virsh -c qemu:///system net-start vmc-spike-net
check "mgmt-net-destroy" virsh -c qemu:///system net-destroy vmc-spike-net
check "mgmt-net-undefine" virsh -c qemu:///system net-undefine vmc-spike-net

# 7. go-libvirt exact-library probe (connection supervisor semantics).
check "golibvirt-probe" go run ./scripts/spike/golibvirt-probe

echo
echo "== summary: ${#PASS[@]} pass, ${#FAIL[@]} fail =="
[ ${#FAIL[@]} -eq 0 ] && echo "SPIKE: GO" || { echo "SPIKE: NO-GO — invoke the plan §9 failure branch"; exit 1; }
