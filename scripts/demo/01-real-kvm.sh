#!/usr/bin/env bash
# Capstone real-KVM demo (run INSIDE the Tier-1 substrate VM as root/service
# identity, from a VM-native clone). Boots a REAL Ubuntu guest through the
# full control plane and SSHes into it.
#
#   sudo -E env "PATH=$PATH" ./scripts/demo/01-real-kvm.sh
#
# Steps: seed the image cache, generate an ephemeral SSH key, launch the
# stack (embedded Postgres + control-plane + agent --driver libvirt), create
# a VM via vmctl, wait for Running, SSH in, then delete.
set -euo pipefail
cd "$(dirname "$0")/../.."

echo "== 1. seed image cache =="
./scripts/spike/seed-image.sh

echo "== 2. ephemeral SSH key =="
KEYDIR=$(mktemp -d)
ssh-keygen -t ed25519 -N "" -f "$KEYDIR/id" >/dev/null
PUB="$KEYDIR/id.pub"

echo "== 3. build =="
go build -o bin/ ./cmd/...

echo "== 4. launch the real-KVM stack (background) =="
go run ./scripts/dev --driver libvirt --nodes node-a,node-b --ssh-key-file "$PUB" >/tmp/vmc-dev.log 2>&1 &
DEV_PID=$!
trap 'kill $DEV_PID 2>/dev/null || true' EXIT

# Discover the control-plane address from the launcher log.
SERVER=""
for i in $(seq 1 30); do
  SERVER=$(grep -oE 'VMCTL_SERVER=127.0.0.1:[0-9]+' /tmp/vmc-dev.log | head -1 | cut -d= -f2 || true)
  [ -n "$SERVER" ] && break
  sleep 1
done
[ -n "$SERVER" ] || { echo "stack did not come up:"; cat /tmp/vmc-dev.log; exit 1; }
export VMCTL_SERVER="$SERVER"
echo "control plane at $SERVER"

echo "== 5. create a real VM =="
OP=$(bin/vmctl create vm real-1 --cpu 1 --memory 1GiB --image ubuntu-24.04 --disk 4GiB | awk 'NR==2{print $1}')
echo "operation $OP"
bin/vmctl op wait "$OP" --timeout 4m
bin/vmctl list vms

echo "== 6. wait for the mgmt IP + SSH =="
IP=""
for i in $(seq 1 40); do
  IP=$(virsh -c qemu:///system net-dhcp-leases vmc-mgmt 2>/dev/null | awk '/ipv4/{print $5}' | cut -d/ -f1 | head -1 || true)
  [ -n "$IP" ] && break
  sleep 3
done
[ -n "$IP" ] || { echo "no DHCP lease"; exit 1; }
echo "guest IP: $IP"
ssh -i "$KEYDIR/id" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 \
  ubuntu@"$IP" 'cloud-init status --wait && echo "REAL-KVM GUEST REACHED: $(hostname) / $(uname -r)"'

echo "== 7. delete =="
DOP=$(bin/vmctl delete vm real-1 | awk 'NR==2{print $1}')
bin/vmctl op wait "$DOP" --timeout 2m
echo "== demo complete: a real KVM guest was booted, reached over SSH, and deleted through the control plane =="
