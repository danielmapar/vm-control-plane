#!/usr/bin/env bash
# Capstone real-KVM demo (run INSIDE the Tier-1 substrate VM as the service
# user — NOT root — from a VM-native clone). Boots a REAL Ubuntu guest
# through the full control plane and SSHes into it.
#
#   ./scripts/demo/01-real-kvm.sh
#
# Run as the unprivileged service user, whose libvirt/kvm/vmc group
# membership grants /dev/kvm, the libvirt system socket, and the storage
# root — no root needed. (Embedded PostgreSQL refuses to run as root, so
# sudo would break the stack; libvirt access comes from groups, not root.)
#
# Steps: seed the image cache, generate an ephemeral SSH key, launch the
# stack (embedded Postgres + control-plane + agent --driver libvirt), create
# a VM via vmctl, wait for Running, SSH in, then delete.
#
# SUBSTRATE CAVEAT: this drives *nested* KVM inside VirtualBox. The control
# plane reliably converges the domain to RUNNING and the guest pulls a DHCP
# lease, but VirtualBox's experimental nested VT-x can guru-meditate under a
# sustained guest boot (see docs/reviews/real-substrate-findings.md, "End-to-
# end capstone"). The proof that a guest boots all the way to SSH is the
# focused, fast integration test (TestITBootSSHTeardown, ~80s). This script
# retries SSH and reaps the guest promptly on any exit to stay inside the safe
# window; on bare-metal KVM the caveat disappears.
set -euo pipefail
cd "$(dirname "$0")/../.."

echo "== 1. seed image cache =="
./scripts/spike/seed-image.sh
# Warm the backing image into the page cache. The guest reads ~2GB of backing
# during boot; served from RAM those reads don't contend with anything on the
# substrate's single virtual disk. Disk-read contention during an L2 boot is a
# proven nested-VirtualBox destabilizer (it wedges the guest).
if [ -f /var/lib/vmc/cache/ubuntu-24.04.qcow2 ]; then
  cat /var/lib/vmc/cache/ubuntu-24.04.qcow2 >/dev/null 2>&1 || true
fi

echo "== 2. ephemeral SSH key =="
KEYDIR=$(mktemp -d)
ssh-keygen -t ed25519 -N "" -f "$KEYDIR/id" >/dev/null
PUB="$KEYDIR/id.pub"

echo "== 3. build =="
go build -o bin/ ./cmd/...

echo "== 4. launch the real-KVM stack (background) =="
DEVLOG=$(mktemp /tmp/vmc-dev.XXXXXX.log)
go run ./scripts/dev --driver libvirt --nodes node-a,node-b --ssh-key-file "$PUB" >"$DEVLOG" 2>&1 &
DEV_PID=$!
# On ANY exit, destroy the guest domain BEFORE tearing down the stack. A guest
# left running with nobody to reap it lingers — and under VirtualBox's nested
# virtualization a long-lived nested guest eventually trips a host guru
# meditation. Prompt teardown keeps the guest's lifetime short (like the
# integration test's ~80s), well inside the safe window.
trap 'virsh -c qemu:///system destroy real-1 >/dev/null 2>&1 || true; kill $DEV_PID 2>/dev/null || true' EXIT

# Discover the control-plane address from the launcher log.
SERVER=""
for i in $(seq 1 90); do
  SERVER=$(grep -oE 'VMCTL_SERVER=127.0.0.1:[0-9]+' "$DEVLOG" | head -1 | cut -d= -f2 || true)
  [ -n "$SERVER" ] && break
  sleep 1
done
[ -n "$SERVER" ] || { echo "stack did not come up:"; cat "$DEVLOG"; exit 1; }
export VMCTL_SERVER="$SERVER"
echo "control plane at $SERVER"

echo "== 5. create a real VM =="
OP=$(bin/vmctl create vm real-1 --cpu 1 --memory 1GiB --image ubuntu-24.04 --disk 4GiB | grep -oiE '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}' | tail -1)
echo "operation $OP"
bin/vmctl op wait "$OP" --timeout 4m
bin/vmctl list vms

echo "== 6. wait for the mgmt IP =="
# Match THIS guest's lease by the domain's own MAC (the integration test does
# the same via MgmtIP). Matching by hostname is unreliable: until cloud-init
# sets the hostname the lease shows "-", so a hostname match falls back to a
# STALE lease from a prior guest and probes a dead address — which is exactly
# what made earlier runs look like they "never answered SSH". Lease columns:
#   $1 date  $2 time  $3 mac  $4 proto  $5 ip/cidr  $6 hostname
MAC=$(virsh -c qemu:///system dumpxml real-1 2>/dev/null | grep -oiE '52:54:00:[0-9a-f:]+' | head -1)
IP=""
for i in $(seq 1 40); do
  IP=$(timeout 10 virsh -c qemu:///system net-dhcp-leases vmc-mgmt 2>/dev/null \
        | awk -v m="$MAC" 'tolower($3)==tolower(m){print $5}' | cut -d/ -f1 | head -1 || true)
  [ -n "$IP" ] && break
  sleep 3
done
[ -n "$IP" ] || { echo "no DHCP lease"; exit 1; }
echo "guest IP: $IP (mac $MAC)"

echo "== 6b. SSH into the guest (retry until cloud-init brings up sshd) =="
# A DHCP lease means the kernel is up, but sshd + the injected key land later
# in cloud-init — so a single early SSH gets 'connection refused'. Retry, the
# way the integration test's waitSSH does.
SSH_OK=""
for i in $(seq 1 36); do
  if OUT=$(timeout 15 ssh -i "$KEYDIR/id" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
        -o ConnectTimeout=5 -o BatchMode=yes ubuntu@"$IP" \
        'cloud-init status --wait >/dev/null 2>&1; echo "REAL-KVM GUEST REACHED: $(hostname) / $(uname -r)"' 2>/dev/null); then
    echo "$OUT"; SSH_OK=1; break
  fi
  sleep 5
done
[ -n "$SSH_OK" ] || { echo "guest never answered SSH within timeout"; exit 1; }

echo "== 7. stop the VM (ACPI shutdown), then list =="
SOP=$(bin/vmctl stop vm real-1 | grep -oiE '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}' | tail -1)
bin/vmctl op wait "$SOP" --timeout 2m
bin/vmctl list vms

echo "== 8. delete =="
DOP=$(bin/vmctl delete vm real-1 | grep -oiE '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}' | tail -1)
bin/vmctl op wait "$DOP" --timeout 2m
bin/vmctl list vms
echo "== demo complete: a real KVM guest was booted, reached over SSH, stopped, and deleted through the control plane =="
