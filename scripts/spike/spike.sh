#!/usr/bin/env bash
# Go/no-go spike: proves the real-substrate assumptions on THIS machine under
# the exact service identity, before any real-driver PR merges (plan §8/§9).
#
#   ./scripts/spike/spike.sh          # base battery (fast, no downloads)
#   ./scripts/spike/spike.sh --full   # + image boot / DHCP / key-only SSH
#
# Every check's command, output, and status are retained under the log dir;
# the transcript ends with GO / NO-GO. Resources are run-scoped
# (vmc-spike-<id>) and cleaned by trap — reruns are safe; foreign resources
# are never touched.
set -uo pipefail

RUN_ID="$$-$(date +%s)"
LOGDIR="${SPIKE_LOGDIR:-/tmp/vmc-spike-$RUN_ID}"
mkdir -p "$LOGDIR"
DOM="vmc-spike-$RUN_ID"
NET="vmc-spike-net-$RUN_ID"
BR="vmc-spike-br-$RUN_ID"
WORK="$LOGDIR/work"
mkdir -p "$WORK"
FULL=${1:-}

PASS=(); FAIL=()

cleanup() {
  # Only resources created by THIS invocation.
  virsh -c qemu:///system destroy "$DOM" >/dev/null 2>&1
  virsh -c qemu:///system undefine "$DOM" >/dev/null 2>&1
  virsh -c qemu:///system net-destroy "$NET" >/dev/null 2>&1
  virsh -c qemu:///system net-undefine "$NET" >/dev/null 2>&1
  ovs-vsctl --if-exists del-br "$BR" >/dev/null 2>&1
}
trap cleanup EXIT

check() { # name, command...
  local name=$1; shift
  local log="$LOGDIR/$name.log"
  { echo "\$ $*"; "$@"; } >"$log" 2>&1
  local rc=$?
  echo "exit=$rc" >>"$log"
  if [ $rc -eq 0 ]; then
    PASS+=("$name"); echo "PASS  $name"
  else
    FAIL+=("$name"); echo "FAIL  $name  ($(tail -2 "$log" | head -1))"
  fi
  return 0
}

{
  echo "== vm-control-plane substrate spike =="
  echo "run: $RUN_ID  logdir: $LOGDIR"
  echo "identity: $(id)"
  echo "host: $(uname -a)"
  echo "commit: $(git rev-parse HEAD 2>/dev/null || echo unknown)"
  echo
} | tee "$LOGDIR/provenance.txt"

# 1. KVM acceleration — hard fail on TCG.
check kvm-device-rw test -r /dev/kvm -a -w /dev/kvm
check kvm-accel bash -c "virsh -c qemu:///system capabilities | grep -q \"domain type='kvm'\""

# 2. libvirt access under this UID (system socket, not session).
check libvirt-connect virsh -c qemu:///system version

# 3. Trivial domain lifecycle — each step gated on the previous.
cat >"$WORK/dom.xml" <<EOF
<domain type='kvm'>
  <name>$DOM</name>
  <memory unit='MiB'>128</memory>
  <vcpu>1</vcpu>
  <os><type arch='x86_64'>hvm</type><boot dev='hd'/></os>
  <devices><console type='pty'/></devices>
</domain>
EOF
if check domain-define virsh -c qemu:///system define "$WORK/dom.xml" && [ ${#FAIL[@]} -eq 0 ]; then :; fi
if virsh -c qemu:///system dominfo "$DOM" >/dev/null 2>&1; then
  check domain-start   virsh -c qemu:///system start "$DOM"
  check domain-destroy virsh -c qemu:///system destroy "$DOM"
  check domain-undefine virsh -c qemu:///system undefine "$DOM"
fi

# 4. qcow2 mechanics: explicit backing format AND explicit size.
check qemu-img-base    qemu-img create -f qcow2 "$WORK/base.qcow2" 1G
check qemu-img-overlay qemu-img create -f qcow2 -b "$WORK/base.qcow2" -F qcow2 "$WORK/overlay.qcow2" 2G
check qemu-img-size    bash -c "qemu-img info --output=json --backing-chain '$WORK/overlay.qcow2' | jq -e '.[0].\"virtual-size\" == 2147483648' >/dev/null"

# 5. OVS under this UID with capability-detected datapath — bridge creation
# and configuration in ONE ovs-vsctl transaction (no partial states).
DP=$(cat /var/lib/vmc-datapath 2>/dev/null || echo kernel)
if [ "$DP" = netdev ]; then
  check ovs-bridge ovs-vsctl -- add-br "$BR" -- set bridge "$BR" datapath_type=netdev -- set bridge "$BR" external_ids:vmc-owner="spike-$RUN_ID"
else
  check ovs-bridge ovs-vsctl -- add-br "$BR" -- set bridge "$BR" external_ids:vmc-owner="spike-$RUN_ID"
fi
check ovs-owner-readback bash -c "ovs-vsctl get bridge '$BR' external_ids:vmc-owner | grep -q 'spike-$RUN_ID'"
check ovs-restart-perms bash -c "sudo systemctl restart openvswitch-switch && sleep 2 && ovs-vsctl list-br >/dev/null"

# 6. Management NAT network — run-scoped subnet from the run id.
OCT=$(( ( $$ % 200 ) + 20 ))
cat >"$WORK/net.xml" <<EOF
<network>
  <name>$NET</name>
  <forward mode='nat'/>
  <ip address='192.168.$OCT.1' netmask='255.255.255.0'>
    <dhcp><range start='192.168.$OCT.10' end='192.168.$OCT.99'/></dhcp>
  </ip>
</network>
EOF
check mgmt-net-define  virsh -c qemu:///system net-define "$WORK/net.xml"
check mgmt-net-start   virsh -c qemu:///system net-start "$NET"

# 7. go-libvirt exact-library probe (ambiguous-mutation window + poison +
# re-observe — the D8 evidence).
check golibvirt-probe go run ./scripts/spike/golibvirt-probe

# 8. --full: image boot, DHCP on the run-scoped NAT network, key-only SSH,
# storage exercised through QEMU, ownership-aware cleanup.
if [ "$FULL" = "--full" ]; then
  IMG_URL="https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img"
  IMG="$WORK/noble.img"
  check full-image-fetch bash -c "curl -fsSL -o '$IMG' '$IMG_URL'"
  check full-overlay qemu-img create -f qcow2 -b "$IMG" -F qcow2 "$WORK/vm.qcow2" 5G
  check full-keygen ssh-keygen -t ed25519 -N "" -f "$WORK/key"
  cat >"$WORK/user-data" <<EOF
#cloud-config
users:
  - name: spike
    ssh_authorized_keys:
      - $(cat "$WORK/key.pub")
ssh_pwauth: false
EOF
  printf 'instance-id: %s\nlocal-hostname: %s\n' "$DOM" "$DOM" >"$WORK/meta-data"
  check full-seed cloud-localds "$WORK/seed.iso" "$WORK/user-data" "$WORK/meta-data"
  cat >"$WORK/fulldom.xml" <<EOF
<domain type='kvm'>
  <name>$DOM</name>
  <memory unit='MiB'>1024</memory>
  <vcpu>1</vcpu>
  <os><type arch='x86_64'>hvm</type><boot dev='hd'/></os>
  <devices>
    <disk type='file' device='disk'><driver name='qemu' type='qcow2'/><source file='$WORK/vm.qcow2'/><target dev='vda' bus='virtio'/></disk>
    <disk type='file' device='cdrom'><driver name='qemu' type='raw'/><source file='$WORK/seed.iso'/><target dev='sda' bus='sata'/></disk>
    <interface type='network'><source network='$NET'/><model type='virtio'/></interface>
    <console type='pty'/>
  </devices>
</domain>
EOF
  check full-define virsh -c qemu:///system define "$WORK/fulldom.xml"
  check full-start  virsh -c qemu:///system start "$DOM"
  GUEST_IP=""
  for i in $(seq 1 60); do
    GUEST_IP=$(virsh -c qemu:///system net-dhcp-leases "$NET" 2>/dev/null | awk '/ipv4/ {print $5}' | cut -d/ -f1 | head -1)
    [ -n "$GUEST_IP" ] && break
    sleep 5
  done
  check full-dhcp test -n "$GUEST_IP"
  if [ -n "$GUEST_IP" ]; then
    check full-ssh bash -c "for i in \$(seq 1 30); do ssh -i '$WORK/key' -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 spike@'$GUEST_IP' 'cloud-init status --wait && echo guest-ok' && exit 0; sleep 5; done; exit 1"
    check full-storage-through-qemu bash -c "ssh -i '$WORK/key' -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null spike@'$GUEST_IP' 'echo data | sudo tee /var/spike-marker && sync'"
  fi
  check full-stop bash -c "virsh -c qemu:///system destroy '$DOM' && virsh -c qemu:///system undefine '$DOM'"
fi

{
  echo
  echo "== summary: ${#PASS[@]} pass, ${#FAIL[@]} fail =="
  for f in "${FAIL[@]:-}"; do [ -n "$f" ] && echo "  FAILED: $f (see $LOGDIR/$f.log)"; done
  if [ ${#FAIL[@]} -eq 0 ]; then echo "SPIKE: GO"; else echo "SPIKE: NO-GO — invoke the plan §9 failure branch"; fi
} | tee -a "$LOGDIR/provenance.txt"

[ ${#FAIL[@]} -eq 0 ]
